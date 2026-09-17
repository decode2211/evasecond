package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"eva2/backend/internal/models"
)

// ErrNotFound is returned by any lookup-by-id method when no row matches.
// httpapi translates this into a 404 response.
var ErrNotFound = errors.New("not found")

// ListMedia returns every media item, newest first.
func (s *Store) ListMedia(ctx context.Context) ([]models.Media, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, type, url, duration_seconds, created_at
		 FROM media ORDER BY created_at DESC, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing media: %w", err)
	}
	defer rows.Close()

	var out []models.Media
	for rows.Next() {
		var m models.Media
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning media row: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMediaByIDs fetches media rows for exactly the given ids, as a map for
// easy lookup while building playlist responses. Missing ids are simply
// absent from the result (callers that require every id to exist should
// check the map length).
func (s *Store) GetMediaByIDs(ctx context.Context, ids []string) (map[string]models.Media, error) {
	out := map[string]models.Media{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, type, url, duration_seconds, created_at
		 FROM media WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		return nil, fmt.Errorf("fetching media by ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m models.Media
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning media row: %w", err)
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

// GetMedia fetches one media item by id, or ErrNotFound if it doesn't exist.
func (s *Store) GetMedia(ctx context.Context, id string) (models.Media, error) {
	var m models.Media
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, type, url, duration_seconds, created_at FROM media WHERE id = $1`, id,
	).Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Media{}, ErrNotFound
	}
	if err != nil {
		return models.Media{}, fmt.Errorf("fetching media %s: %w", id, err)
	}
	return m, nil
}

// CreateMedia inserts a new media row and returns it with its generated id.
// Callers (internal/httpapi) are responsible for validating type/url/
// duration before calling this — this method assumes the input is already
// valid and simply persists it.
func (s *Store) CreateMedia(ctx context.Context, name string, mediaType models.MediaType, url *string, durationSeconds int) (models.Media, error) {
	m := models.Media{
		ID:              newID("media"),
		Name:            name,
		Type:            mediaType,
		URL:             url,
		DurationSeconds: durationSeconds,
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO media (id, name, type, url, duration_seconds)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING created_at`,
		m.ID, m.Name, m.Type, m.URL, m.DurationSeconds,
	).Scan(&m.CreatedAt)
	if err != nil {
		return models.Media{}, fmt.Errorf("inserting media: %w", err)
	}
	return m, nil
}
