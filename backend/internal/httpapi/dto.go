// This file defines the JSON "shapes" this API sends over the wire (DTO
// stands for "data transfer object"). They are kept separate from
// internal/models so that internal storage details (like Go's time.Time)
// never leak into the API by accident — every timestamp here is explicitly
// formatted as an RFC3339-with-milliseconds string before it goes out.
package httpapi

import (
	"eva2/backend/internal/models"
	"eva2/backend/internal/schedule"
)

type mediaDTO struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Type            string  `json:"type"`
	URL             *string `json:"url"`
	DurationSeconds int     `json:"duration_seconds"`
	CreatedAt       string  `json:"created_at"`
}

func toMediaDTO(m models.Media) mediaDTO {
	return mediaDTO{
		ID:              m.ID,
		Name:            m.Name,
		Type:            string(m.Type),
		URL:             m.URL,
		DurationSeconds: m.DurationSeconds,
		CreatedAt:       rfc3339Milli(m.CreatedAt),
	}
}

type playlistItemDTO struct {
	Position int       `json:"position"`
	MediaID  string    `json:"media_id"`
	Media    *mediaDTO `json:"media,omitempty"`
}

type playlistVersionDTO struct {
	ID          int64             `json:"id"`
	EffectiveAt string            `json:"effective_at"`
	Items       []playlistItemDTO `json:"items"`
}

func toPlaylistVersionDTO(v models.PlaylistVersion) playlistVersionDTO {
	items := make([]playlistItemDTO, len(v.Items))
	for i, it := range v.Items {
		dto := playlistItemDTO{Position: it.Position, MediaID: it.MediaID}
		if it.MediaDetail != nil {
			m := toMediaDTO(*it.MediaDetail)
			dto.Media = &m
		}
		items[i] = dto
	}
	return playlistVersionDTO{
		ID:          v.ID,
		EffectiveAt: rfc3339Milli(v.EffectiveAt),
		Items:       items,
	}
}

type windowStateDTO struct {
	ID               string               `json:"id"`
	Name             string               `json:"name"`
	CycleAnchor      string               `json:"cycle_anchor"`
	CurrentVersion   *playlistVersionDTO  `json:"current_version"`
	UpcomingVersions []playlistVersionDTO `json:"upcoming_versions"`
}

// splitCurrentAndUpcoming separates a window's full version history into
// "the one playing right now" and "changes already scheduled for later".
// Past versions (superseded long ago) are dropped — nothing on the API
// needs them, since playback only ever depends on the current version.
//
// selection reuses schedule.SelectVersion, the exact same rule the pure
// scheduler uses to pick which version governs playback at a given time,
// so this view can never disagree with what a window is actually showing.
func splitCurrentAndUpcoming(versions []models.PlaylistVersion, nowMS int64) (*models.PlaylistVersion, []models.PlaylistVersion) {
	bare := make([]schedule.Version, len(versions))
	for i, v := range versions {
		bare[i] = schedule.Version{ID: v.ID, EffectiveAtMS: v.EffectiveAt.UnixMilli()}
	}
	selected, found := schedule.SelectVersion(bare, nowMS)

	var current *models.PlaylistVersion
	var upcoming []models.PlaylistVersion
	for i := range versions {
		if found && versions[i].ID == selected.ID {
			v := versions[i]
			current = &v
			continue
		}
		if versions[i].EffectiveAt.UnixMilli() > nowMS {
			upcoming = append(upcoming, versions[i])
		}
	}
	return current, upcoming
}

func toWindowStateDTO(w models.Window, versions []models.PlaylistVersion, nowMS int64) windowStateDTO {
	current, upcoming := splitCurrentAndUpcoming(versions, nowMS)

	dto := windowStateDTO{
		ID:          w.ID,
		Name:        w.Name,
		CycleAnchor: rfc3339Milli(w.CycleAnchor),
	}
	if current != nil {
		cv := toPlaylistVersionDTO(*current)
		dto.CurrentVersion = &cv
	}
	dto.UpcomingVersions = make([]playlistVersionDTO, len(upcoming))
	for i, v := range upcoming {
		dto.UpcomingVersions[i] = toPlaylistVersionDTO(v)
	}
	return dto
}

type syncDTO struct {
	ID              int64     `json:"id"`
	MediaID         string    `json:"media_id"`
	Media           *mediaDTO `json:"media,omitempty"`
	StartsAt        string    `json:"starts_at"`
	DurationSeconds int       `json:"duration_seconds"`
}

func toSyncDTO(ev models.SyncEvent) syncDTO {
	dto := syncDTO{
		ID:              ev.ID,
		MediaID:         ev.MediaID,
		StartsAt:        rfc3339Milli(ev.StartsAt),
		DurationSeconds: ev.DurationSeconds,
	}
	if ev.MediaDetail != nil {
		m := toMediaDTO(*ev.MediaDetail)
		dto.Media = &m
	}
	return dto
}
