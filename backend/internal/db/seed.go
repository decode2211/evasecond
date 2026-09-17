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
//
// An earlier version of this list pointed at Wikimedia Commons thumbnail
// URLs and Google's old "gtv-videos-bucket" sample-video bucket. Both broke:
// the Wikimedia thumbnail path 404'd (guessed thumbnail resolutions aren't
// stable), and the Google bucket now returns 403 Forbidden — it's been
// locked down. Every URL below was re-verified by hand with `curl -I`
// (including with an Origin header, the way a real browser request looks)
// immediately before being added here:
//   - images: placehold.co, which returns 200, Content-Type: image/jpeg,
//     and an explicit "Access-Control-Allow-Origin: *" on every request.
//   - videos: MDN's CC0 sample-video host, which returns 200,
//     Content-Type: video/mp4, "Access-Control-Allow-Origin: *" once a
//     request carries an Origin header, "Accept-Ranges: bytes", and replies
//     206 Partial Content to an actual Range request — everything a
//     <video> element needs to seek and play cross-origin.
type seedMediaRow struct {
	id, name, mediaType, url string
	durationSeconds          int
}

var seedMediaRows = []seedMediaRow{
	{"M1", "Sample Image 1", "image", "https://placehold.co/800x600/orange/white.jpg?text=M1", 10},
	{"M2", "Sample Video 1 (flower)", "video", "https://interactive-examples.mdn.mozilla.net/media/cc0-videos/flower.mp4", 15},
	{"M3", "Sample Image 2", "image", "https://placehold.co/800x600/teal/white.jpg?text=M3", 8},
	{"M4", "Sample Video 2 (friday)", "video", "https://interactive-examples.mdn.mozilla.net/media/cc0-videos/friday.mp4", 20},
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
