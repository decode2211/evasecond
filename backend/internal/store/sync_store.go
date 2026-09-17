package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"eva2/backend/internal/models"
)

// activeOrUpcomingSyncSQL selects a sync event that either is playing right
// now or is still to come (its lead time hasn't elapsed yet) and has not
// been cancelled. "Not yet ended" is expressed directly in SQL
// (starts_at + duration has not passed) rather than in Go, so the database
// clock — not the application server's clock — decides the answer.
const activeOrUpcomingSyncSQL = `
	SELECT se.id, se.media_id, se.starts_at, se.duration_seconds, se.created_at, se.cancelled_at,
	       m.name, m.type, m.url, m.duration_seconds, m.created_at
	FROM sync_events se
	JOIN media m ON m.id = se.media_id
	WHERE se.cancelled_at IS NULL
	  AND se.starts_at + (se.duration_seconds || ' seconds')::interval > now()
	ORDER BY se.created_at DESC
	LIMIT 1
`

func scanSyncEvent(row pgx.Row) (*models.SyncEvent, error) {
	var (
		ev    models.SyncEvent
		media models.Media
	)
	err := row.Scan(
		&ev.ID, &ev.MediaID, &ev.StartsAt, &ev.DurationSeconds, &ev.CreatedAt, &ev.CancelledAt,
		&media.Name, &media.Type, &media.URL, &media.DurationSeconds, &media.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning sync event: %w", err)
	}
	media.ID = ev.MediaID
	ev.MediaDetail = &media
	return &ev, nil
}

// GetActiveOrUpcomingSync returns the current sync event, whether it has
// already started or is still in its lead-time window, or nil if there is
// none. Including upcoming (not-yet-started) syncs here is deliberate: the
// frontend needs to learn about a sync before it starts so every browser is
// ready to switch at the same instant.
func (s *Store) GetActiveOrUpcomingSync(ctx context.Context) (*models.SyncEvent, error) {
	return scanSyncEvent(s.pool.QueryRow(ctx, activeOrUpcomingSyncSQL))
}

// CreateSync cancels whatever sync is currently active or upcoming (only
// one sync is ever in effect at a time) and starts a new one, timed to
// begin leadMS milliseconds from now so every connected browser has a
// chance to receive it before it starts.
func (s *Store) CreateSync(ctx context.Context, mediaID string, durationSeconds int, leadMS int64) (models.SyncEvent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.SyncEvent{}, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var mediaExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id = $1)`, mediaID).Scan(&mediaExists); err != nil {
		return models.SyncEvent{}, fmt.Errorf("checking media %s exists: %w", mediaID, err)
	}
	if !mediaExists {
		return models.SyncEvent{}, ErrMediaNotFound
	}

	if _, err := tx.Exec(ctx, `
		UPDATE sync_events SET cancelled_at = now()
		WHERE cancelled_at IS NULL
		  AND starts_at + (duration_seconds || ' seconds')::interval > now()
	`); err != nil {
		return models.SyncEvent{}, fmt.Errorf("cancelling previous sync: %w", err)
	}

	startsAt := time.Now().UTC().Add(time.Duration(leadMS) * time.Millisecond)

	var newID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO sync_events (media_id, starts_at, duration_seconds) VALUES ($1, $2, $3) RETURNING id`,
		mediaID, startsAt, durationSeconds,
	).Scan(&newID); err != nil {
		return models.SyncEvent{}, fmt.Errorf("inserting sync event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return models.SyncEvent{}, fmt.Errorf("committing sync event: %w", err)
	}

	ev, err := s.GetActiveOrUpcomingSync(ctx)
	if err != nil {
		return models.SyncEvent{}, err
	}
	if ev == nil || ev.ID != newID {
		return models.SyncEvent{}, fmt.Errorf("newly created sync event %d not found after commit", newID)
	}
	return *ev, nil
}

// CancelSync cancels whatever sync is currently active or upcoming. It is
// not an error to call this when no sync is active — it simply does
// nothing in that case.
func (s *Store) CancelSync(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sync_events SET cancelled_at = now()
		WHERE cancelled_at IS NULL
		  AND starts_at + (duration_seconds || ' seconds')::interval > now()
	`)
	if err != nil {
		return fmt.Errorf("cancelling sync: %w", err)
	}
	return nil
}
