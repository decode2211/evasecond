// Package config reads the settings this server needs from environment
// variables (things like "which database to connect to" or "which port to
// listen on"), checks that they make sense, and fails fast with a clear
// message if something required is missing or invalid. Reading all of this
// in one place, once, at startup means the rest of the program can just
// trust the values instead of re-checking them everywhere.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every setting the server needs to run.
type Config struct {
	// Port is the TCP port the HTTP server listens on, e.g. "8080".
	Port string

	// DatabaseURL is the Postgres connection string, e.g.
	// "postgres://user:pass@host:5432/dbname?sslmode=require".
	DatabaseURL string

	// CORSOrigins is the list of frontend URLs allowed to call this API
	// from a browser (e.g. "https://my-app.vercel.app").
	CORSOrigins []string

	// CycleSeconds is how long, in seconds, each window's playlist cycle
	// lasts before it restarts from item 1. The assignment fixes this at
	// 5 hours (18000 seconds), but it's kept configurable for testing.
	CycleSeconds int64

	// SyncLeadMS is how many milliseconds in the future a sync event's
	// start time is set, so every connected browser has time to receive
	// the instruction before the moment it's supposed to happen.
	SyncLeadMS int64

	// SeedOnStart, when true, inserts the placeholder demo windows and
	// media the first time the server runs against an empty database.
	SeedOnStart bool
}

// Load reads and validates configuration from environment variables.
// It returns a descriptive error instead of panicking, so main.go can log
// a clean message and exit rather than printing a stack trace.
func Load() (Config, error) {
	cfg := Config{
		Port:         getEnv("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		CycleSeconds: 18000,
		SyncLeadMS:   1500,
		SeedOnStart:  true,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (e.g. postgres://user:pass@host:5432/dbname?sslmode=require)")
	}

	originsRaw := getEnv("CORS_ORIGINS", "")
	if originsRaw != "" {
		for _, o := range strings.Split(originsRaw, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				cfg.CORSOrigins = append(cfg.CORSOrigins, o)
			}
		}
	}

	if v := os.Getenv("CYCLE_SECONDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("CYCLE_SECONDS must be a positive integer, got %q", v)
		}
		cfg.CycleSeconds = n
	}

	if v := os.Getenv("SYNC_LEAD_MS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("SYNC_LEAD_MS must be a non-negative integer, got %q", v)
		}
		cfg.SyncLeadMS = n
	}

	if v := os.Getenv("SEED_ON_START"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("SEED_ON_START must be true or false, got %q", v)
		}
		cfg.SeedOnStart = b
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
