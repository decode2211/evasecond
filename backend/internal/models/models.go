// Package models defines the shapes of the data this application works
// with: media clips, display windows, playlist versions, and sync events.
// These structs mirror the database tables and are what the rest of the
// backend (storage, HTTP handlers) passes around.
package models

import "time"

// MediaType is the kind of thing a Media item is: a still image, a video
// clip, or a "blank" placeholder (a deliberate empty slot in a playlist).
type MediaType string

const (
	MediaImage MediaType = "image"
	MediaVideo MediaType = "video"
	MediaBlank MediaType = "blank"
)

// Media is one piece of content that can appear in a window's playlist.
// URL is empty for blank media (there's nothing to load) and required for
// image or video media. DurationSeconds is how long it plays for each time
// it comes up — for video this is the configured play length, which may be
// shorter or longer than the video file's own natural length.
type Media struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Type            MediaType `json:"type"`
	URL             *string   `json:"url"`
	DurationSeconds int       `json:"duration_seconds"`
	CreatedAt       time.Time `json:"created_at"`
}

// Window is one on-screen display slot. It loops through its own playlist
// forever, restarting every CycleSeconds (5 hours) from CycleAnchor.
type Window struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CycleAnchor time.Time `json:"cycle_anchor"`
	CreatedAt   time.Time `json:"created_at"`
}

// PlaylistItem is one entry inside a specific PlaylistVersion: a position
// in the list and which media plays there. MediaDetail is filled in by the
// store layer when the API needs to hand the full media record to the
// frontend, so the browser doesn't have to make a second request.
type PlaylistItem struct {
	Position    int    `json:"position"`
	MediaID     string `json:"media_id"`
	MediaDetail *Media `json:"media,omitempty"`
}

// PlaylistVersion is one saved copy of a window's playlist, along with the
// moment it takes effect. See internal/schedule for why versions exist
// instead of editing playlists in place.
type PlaylistVersion struct {
	ID          int64          `json:"id"`
	WindowID    string         `json:"window_id"`
	EffectiveAt time.Time      `json:"effective_at"`
	CreatedAt   time.Time      `json:"created_at"`
	Items       []PlaylistItem `json:"items"`
}

// SyncEvent records one "show this media on every window at once" action.
// CancelledAt is set when an admin explicitly cancels it early, or when a
// newer sync event replaces it (only one sync is ever active at a time).
type SyncEvent struct {
	ID              int64      `json:"id"`
	MediaID         string     `json:"media_id"`
	StartsAt        time.Time  `json:"starts_at"`
	DurationSeconds int        `json:"duration_seconds"`
	CreatedAt       time.Time  `json:"created_at"`
	CancelledAt     *time.Time `json:"cancelled_at,omitempty"`
	MediaDetail     *Media     `json:"media,omitempty"`
}
