package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"eva2/backend/internal/models"
)

// ListWindows returns every configured display window.
func (s *Store) ListWindows(ctx context.Context) ([]models.Window, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, cycle_anchor, created_at FROM windows ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing windows: %w", err)
	}
	defer rows.Close()

	var out []models.Window
	for rows.Next() {
		var w models.Window
		if err := rows.Scan(&w.ID, &w.Name, &w.CycleAnchor, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning window row: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWindow fetches one window by id, or ErrNotFound if it doesn't exist.
func (s *Store) GetWindow(ctx context.Context, id string) (models.Window, error) {
	var w models.Window
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, cycle_anchor, created_at FROM windows WHERE id = $1`, id,
	).Scan(&w.ID, &w.Name, &w.CycleAnchor, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Window{}, ErrNotFound
	}
	if err != nil {
		return models.Window{}, fmt.Errorf("fetching window %s: %w", id, err)
	}
	return w, nil
}
