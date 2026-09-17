package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedMedia is the placeholder demo content used when the assignment brief
// does not supply its own example media list (see README "Assumptions").
// The image and video URLs point at Wikimedia Commons and Google's public
// sample-video bucket, both of which serve their files with permissive
// CORS headers, so a browser on a different origin (like our Vercel
// frontend) is allowed to load them.
type seedMediaRow struct {
	id, name, mediaType, url string
	durationSeconds          int
}

var seedMediaRows = []seedMediaRow{
	{"M1", "Cat Photo", "image", "https://upload.wikimedia.org/wikipedia/commons/3/3a/Cat03.jpg", 10},
	{"M2", "Big Buck Bunny (clip)", "video", "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4", 15},
	{"M3", "Whale Shark Photo", "image", "https://upload.wikimedia.org/wikipedia/commons/thumb/b/b4/Whale_shark_Georgia_aquarium.jpg/1280px-Whale_shark_Georgia_aquarium.jpg", 8},
	{"M4", "Elephants Dream (clip)", "video", "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/ElephantsDream.mp4", 20},
}

// seedBlank is inserted with a NULL url, since "blank" is a deliberate
// empty slot rather than a piece of content to load.
const (
	seedBlankID              = "BLANK"
	seedBlankName            = "Blank"
	seedBlankDurationSeconds = 5
)

// seedWindowPlaylists maps each demo window to the ordered list of media
// IDs it plays. The same media may repeat (M2 appears in both W1 and W3),
// which the schema allows.
var seedWindowPlaylists = map[string][]string{
	"W1": {"M1", "M2", "M3"},
	"W2": {"M2", "BLANK", "M4"},
	"W3": {"M3", "M1", "M4", "M2"},
}

var seedWindowNames = map[string]string{
	"W1": "Window 1",
	"W2": "Window 2",
	"W3": "Window 3",
}

// SeedIfEmpty inserts the demo windows and media described above, but only
// if the database has no windows yet. This makes it safe to call on every
// server startup: the first run populates the demo data, every run after
// that is a no-op.
func SeedIfEmpty(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	var windowCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM windows`).Scan(&windowCount); err != nil {
		return fmt.Errorf("checking existing window count: %w", err)
	}
	if windowCount > 0 {
		log.Info("skipping seed: windows already exist")
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("starting seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, m := range seedMediaRows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO media (id, name, type, url, duration_seconds) VALUES ($1, $2, $3, $4, $5)`,
			m.id, m.name, m.mediaType, m.url, m.durationSeconds,
		); err != nil {
			return fmt.Errorf("seeding media %s: %w", m.id, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO media (id, name, type, url, duration_seconds) VALUES ($1, $2, 'blank', NULL, $3)`,
		seedBlankID, seedBlankName, seedBlankDurationSeconds,
	); err != nil {
		return fmt.Errorf("seeding blank media: %w", err)
	}

	// All three windows share one anchor moment so their cycles are easy
	// to reason about relative to each other. Truncating to the minute
	// just keeps the seeded timestamp tidy in logs and debugging output.
	cycleAnchor := time.Now().UTC().Truncate(time.Minute)

	for windowID, playlist := range seedWindowPlaylists {
		if _, err := tx.Exec(ctx,
			`INSERT INTO windows (id, name, cycle_anchor) VALUES ($1, $2, $3)`,
			windowID, seedWindowNames[windowID], cycleAnchor,
		); err != nil {
			return fmt.Errorf("seeding window %s: %w", windowID, err)
		}

		var versionID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO playlist_versions (window_id, effective_at) VALUES ($1, $2) RETURNING id`,
			windowID, cycleAnchor,
		).Scan(&versionID); err != nil {
			return fmt.Errorf("seeding playlist version for %s: %w", windowID, err)
		}

		for position, mediaID := range playlist {
			if _, err := tx.Exec(ctx,
				`INSERT INTO playlist_items (version_id, position, media_id) VALUES ($1, $2, $3)`,
				versionID, position, mediaID,
			); err != nil {
				return fmt.Errorf("seeding playlist item %d for %s: %w", position, windowID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing seed transaction: %w", err)
	}

	log.Info("seeded demo data", "windows", len(seedWindowPlaylists), "media", len(seedMediaRows)+1, "cycle_anchor", cycleAnchor)
	return nil
}
