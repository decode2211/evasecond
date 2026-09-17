package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"eva2/backend/internal/models"
	"eva2/backend/internal/schedule"
)

// ErrMediaNotFound is returned when a caller references a media id that
// does not exist.
var ErrMediaNotFound = errors.New("media not found")

// querier is the subset of pgxpool.Pool's interface that both a plain pool
// and an in-progress transaction (pgx.Tx) implement. Writing helpers
// against this interface lets the same query code run either standalone or
// as part of a larger transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// listVersionsForWindow fetches every playlist version ever created for a
// window (past, current, and any already-scheduled future ones), each with
// its items and the full media details for those items. It is used both
// standalone (GET /api/windows/{id}/now) and inside the transaction that
// adds a new playlist item (where it needs to see pending versions too).
func listVersionsForWindow(ctx context.Context, q querier, windowID string) ([]models.PlaylistVersion, error) {
	rows, err := q.Query(ctx, `
		SELECT pv.id, pv.effective_at, pv.created_at,
		       pi.position, pi.media_id,
		       m.name, m.type, m.url, m.duration_seconds, m.created_at
		FROM playlist_versions pv
		JOIN playlist_items pi ON pi.version_id = pv.id
		JOIN media m ON m.id = pi.media_id
		WHERE pv.window_id = $1
		ORDER BY pv.effective_at, pv.id, pi.position
	`, windowID)
	if err != nil {
		return nil, fmt.Errorf("listing playlist versions for window %s: %w", windowID, err)
	}
	defer rows.Close()

	byID := map[int64]*models.PlaylistVersion{}
	var order []int64
	for rows.Next() {
		var (
			versionID   int64
			effectiveAt time.Time
			createdAt   time.Time
			item        models.PlaylistItem
			media       models.Media
		)
		if err := rows.Scan(
			&versionID, &effectiveAt, &createdAt,
			&item.Position, &item.MediaID,
			&media.Name, &media.Type, &media.URL, &media.DurationSeconds, &media.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning playlist version row: %w", err)
		}
		media.ID = item.MediaID
		item.MediaDetail = &media

		v, ok := byID[versionID]
		if !ok {
			v = &models.PlaylistVersion{ID: versionID, WindowID: windowID, EffectiveAt: effectiveAt, CreatedAt: createdAt}
			byID[versionID] = v
			order = append(order, versionID)
		}
		v.Items = append(v.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]models.PlaylistVersion, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// ListVersionsForWindow is the exported, standalone form of
// listVersionsForWindow, run directly against the pool.
func (s *Store) ListVersionsForWindow(ctx context.Context, windowID string) ([]models.PlaylistVersion, error) {
	return listVersionsForWindow(ctx, s.pool, windowID)
}

// ListAllVersions fetches every window's playlist versions in one round
// trip and groups them by window id, for GET /api/state (which needs the
// whole system's state at once, not just one window's).
func (s *Store) ListAllVersions(ctx context.Context) (map[string][]models.PlaylistVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pv.window_id, pv.id, pv.effective_at, pv.created_at,
		       pi.position, pi.media_id,
		       m.name, m.type, m.url, m.duration_seconds, m.created_at
		FROM playlist_versions pv
		JOIN playlist_items pi ON pi.version_id = pv.id
		JOIN media m ON m.id = pi.media_id
		ORDER BY pv.window_id, pv.effective_at, pv.id, pi.position
	`)
	if err != nil {
		return nil, fmt.Errorf("listing all playlist versions: %w", err)
	}
	defer rows.Close()

	versionsByID := map[int64]*models.PlaylistVersion{}
	orderByWindow := map[string][]int64{}
	for rows.Next() {
		var (
			windowID    string
			versionID   int64
			effectiveAt time.Time
			createdAt   time.Time
			item        models.PlaylistItem
			media       models.Media
		)
		if err := rows.Scan(
			&windowID, &versionID, &effectiveAt, &createdAt,
			&item.Position, &item.MediaID,
			&media.Name, &media.Type, &media.URL, &media.DurationSeconds, &media.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning playlist version row: %w", err)
		}
		media.ID = item.MediaID
		item.MediaDetail = &media

		v, ok := versionsByID[versionID]
		if !ok {
			v = &models.PlaylistVersion{ID: versionID, WindowID: windowID, EffectiveAt: effectiveAt, CreatedAt: createdAt}
			versionsByID[versionID] = v
			orderByWindow[windowID] = append(orderByWindow[windowID], versionID)
		}
		v.Items = append(v.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[string][]models.PlaylistVersion{}
	for windowID, ids := range orderByWindow {
		list := make([]models.PlaylistVersion, 0, len(ids))
		for _, id := range ids {
			list = append(list, *versionsByID[id])
		}
		out[windowID] = list
	}
	return out, nil
}

// toScheduleVersions converts store-shaped playlist versions into the
// plain, DB-free types the schedule package works with.
func toScheduleVersions(versions []models.PlaylistVersion) []schedule.Version {
	out := make([]schedule.Version, len(versions))
	for i, v := range versions {
		items := make([]schedule.Item, len(v.Items))
		for j, it := range v.Items {
			durationMS := int64(0)
			if it.MediaDetail != nil {
				durationMS = int64(it.MediaDetail.DurationSeconds) * 1000
			}
			items[j] = schedule.Item{MediaID: it.MediaID, DurationMS: durationMS}
		}
		out[i] = schedule.Version{ID: v.ID, EffectiveAtMS: v.EffectiveAt.UnixMilli(), Items: items}
	}
	return out
}

// latestVersion returns the version that governs playback the furthest in
// the future among the given versions: the one with the largest
// EffectiveAt, breaking ties by the largest id. This is "the version to
// build on top of" when adding a new item, per NextVersionEffectiveAt's
// contract in the schedule package.
func latestVersion(versions []models.PlaylistVersion) (models.PlaylistVersion, bool) {
	var best models.PlaylistVersion
	found := false
	for _, v := range versions {
		if !found || v.EffectiveAt.After(best.EffectiveAt) ||
			(v.EffectiveAt.Equal(best.EffectiveAt) && v.ID > best.ID) {
			best = v
			found = true
		}
	}
	return best, found
}

// AddPlaylistItem appends one media item to a window's playlist. It does
// this by creating a brand new playlist version (old items + the new one)
// rather than editing anything in place, so the change can be scheduled to
// start at a clean boundary instead of yanking whatever is already on
// screen. See internal/schedule.NextVersionEffectiveAt for exactly how that
// boundary is chosen.
//
// The whole operation runs inside one transaction that locks the window row
// (SELECT ... FOR UPDATE), so two people adding items to the same window at
// almost the same moment can't race each other into reading the same
// "latest version" and each building a new version that ignores the
// other's addition.
func (s *Store) AddPlaylistItem(ctx context.Context, windowID string, mediaID string, cycleSeconds int64) (models.PlaylistVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return models.PlaylistVersion{}, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var cycleAnchor time.Time
	err = tx.QueryRow(ctx, `SELECT cycle_anchor FROM windows WHERE id = $1 FOR UPDATE`, windowID).Scan(&cycleAnchor)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.PlaylistVersion{}, ErrNotFound
	}
	if err != nil {
		return models.PlaylistVersion{}, fmt.Errorf("locking window %s: %w", windowID, err)
	}

	var mediaExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id = $1)`, mediaID).Scan(&mediaExists); err != nil {
		return models.PlaylistVersion{}, fmt.Errorf("checking media %s exists: %w", mediaID, err)
	}
	if !mediaExists {
		return models.PlaylistVersion{}, ErrMediaNotFound
	}

	existing, err := listVersionsForWindow(ctx, tx, windowID)
	if err != nil {
		return models.PlaylistVersion{}, err
	}

	now := time.Now().UTC()
	scheduleWindow := schedule.Window{ID: windowID, CycleAnchorMS: cycleAnchor.UnixMilli()}
	effectiveAtMS := schedule.NextVersionEffectiveAt(
		scheduleWindow, toScheduleVersions(existing), cycleSeconds*1000, now.UnixMilli(),
	)
	effectiveAt := time.UnixMilli(effectiveAtMS).UTC()

	base, hasBase := latestVersion(existing)
	newItemIDs := make([]string, 0, len(base.Items)+1)
	if hasBase {
		for _, it := range base.Items {
			newItemIDs = append(newItemIDs, it.MediaID)
		}
	}
	newItemIDs = append(newItemIDs, mediaID)

	var newVersionID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO playlist_versions (window_id, effective_at) VALUES ($1, $2) RETURNING id`,
		windowID, effectiveAt,
	).Scan(&newVersionID); err != nil {
		return models.PlaylistVersion{}, fmt.Errorf("inserting new playlist version: %w", err)
	}

	for position, id := range newItemIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO playlist_items (version_id, position, media_id) VALUES ($1, $2, $3)`,
			newVersionID, position, id,
		); err != nil {
			return models.PlaylistVersion{}, fmt.Errorf("inserting playlist item %d: %w", position, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.PlaylistVersion{}, fmt.Errorf("committing new playlist version: %w", err)
	}

	all, err := s.ListVersionsForWindow(ctx, windowID)
	if err != nil {
		return models.PlaylistVersion{}, err
	}
	for _, v := range all {
		if v.ID == newVersionID {
			return v, nil
		}
	}
	return models.PlaylistVersion{}, fmt.Errorf("newly created version %d not found after commit", newVersionID)
}
