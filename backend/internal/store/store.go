// Package store is the only part of the backend that writes SQL. Every
// other package asks it for data (media, windows, playlists, sync events)
// or asks it to save changes, and never talks to Postgres directly. Keeping
// all the SQL in one place makes it much easier to see, in one spot,
// exactly what the database is being asked to do.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a database connection pool with the specific queries this
// application needs.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store backed by the given connection pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// newID generates a short, unique, random identifier for a new row, e.g.
// "media_9f2c1a8b". It doesn't need to be unguessable (this isn't a
// security token) — just unique enough that two inserts never collide.
func newID(prefix string) string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf) // crypto/rand.Read never fails in practice on any supported OS
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}
