package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"eva2/backend/internal/schedule"
	"eva2/backend/internal/store"
)

type addPlaylistItemRequest struct {
	MediaID string `json:"media_id"`
}

type addPlaylistItemResponse struct {
	Version     playlistVersionDTO `json:"version"`
	EffectiveAt string             `json:"effective_at"`
}

// handleAddPlaylistItem appends one media item to a window's playlist.
// Rather than editing the playlist that's currently playing, it creates a
// brand new version scheduled to take over at the next natural boundary
// (see internal/schedule.NextVersionEffectiveAt), so nothing already on
// screen gets interrupted mid-item. The response's effective_at tells the
// caller exactly when the change will take effect, so the UI can show
// something like "applies at 14:32:10".
func (s *Server) handleAddPlaylistItem(w http.ResponseWriter, r *http.Request) {
	windowID := r.PathValue("id")

	var req addPlaylistItemRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid_json", "request body must be valid JSON matching {media_id}")
		return
	}
	req.MediaID = strings.TrimSpace(req.MediaID)
	if req.MediaID == "" {
		badRequest(w, "validation_error", "media_id is required")
		return
	}

	version, err := s.Store.AddPlaylistItem(r.Context(), windowID, req.MediaID, s.Config.CycleSeconds)
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFound(w, "window_not_found", "no window with that id exists")
		return
	case errors.Is(err, store.ErrMediaNotFound):
		notFound(w, "media_not_found", "no media with that id exists")
		return
	case err != nil:
		s.Log.Error("adding playlist item", "window_id", windowID, "error", err)
		internalError(w, "failed to add playlist item")
		return
	}

	dto := toPlaylistVersionDTO(version)
	writeJSON(w, http.StatusCreated, addPlaylistItemResponse{
		Version:     dto,
		EffectiveAt: dto.EffectiveAt,
	})
}

type windowNowResponse struct {
	WindowID       string    `json:"window_id"`
	ServerTime     string    `json:"server_time"`
	Ok             bool      `json:"ok"`
	MediaID        string    `json:"media_id,omitempty"`
	Media          *mediaDTO `json:"media,omitempty"`
	OffsetMS       int64     `json:"offset_ms,omitempty"`
	DurationMS     int64     `json:"duration_ms,omitempty"`
	ItemEndsAt     string    `json:"item_ends_at,omitempty"`
	CycleEndsAt    string    `json:"cycle_ends_at,omitempty"`
	SyncedPlayback bool      `json:"synced_playback"`
}

// handleWindowNow answers "what is this one window showing right now?",
// computed server-side. It exists mainly for debugging and verification —
// the frontend normally computes this itself (from GET /api/state) so
// playback doesn't depend on a constant stream of requests — but having
// the server able to compute and report the same answer is what proves the
// two implementations agree.
func (s *Server) handleWindowNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	windowID := r.PathValue("id")

	win, err := s.Store.GetWindow(ctx, windowID)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w, "window_not_found", "no window with that id exists")
		return
	}
	if err != nil {
		s.Log.Error("fetching window", "window_id", windowID, "error", err)
		internalError(w, "failed to load window")
		return
	}

	versions, err := s.Store.ListVersionsForWindow(ctx, windowID)
	if err != nil {
		s.Log.Error("listing playlist versions", "window_id", windowID, "error", err)
		internalError(w, "failed to load playlist")
		return
	}

	syncEvent, err := s.Store.GetActiveOrUpcomingSync(ctx)
	if err != nil {
		s.Log.Error("loading sync state", "error", err)
		internalError(w, "failed to load sync state")
		return
	}

	now := time.Now().UTC()
	nowMS := now.UnixMilli()

	scheduleVersions := make([]schedule.Version, len(versions))
	for i, v := range versions {
		items := make([]schedule.Item, len(v.Items))
		for j, it := range v.Items {
			durationMS := int64(0)
			if it.MediaDetail != nil {
				durationMS = int64(it.MediaDetail.DurationSeconds) * 1000
			}
			items[j] = schedule.Item{MediaID: it.MediaID, DurationMS: durationMS}
		}
		scheduleVersions[i] = schedule.Version{ID: v.ID, EffectiveAtMS: v.EffectiveAt.UnixMilli(), Items: items}
	}

	resp := windowNowResponse{
		WindowID:   windowID,
		ServerTime: rfc3339Milli(now),
	}

	var activeSync *schedule.ActiveSync
	if syncEvent != nil {
		mediaDurationMS := int64(0)
		if syncEvent.MediaDetail != nil {
			mediaDurationMS = int64(syncEvent.MediaDetail.DurationSeconds) * 1000
		}
		activeSync = &schedule.ActiveSync{
			MediaID:         syncEvent.MediaID,
			StartsAtMS:      syncEvent.StartsAt.UnixMilli(),
			DurationMS:      int64(syncEvent.DurationSeconds) * 1000,
			MediaDurationMS: mediaDurationMS,
		}
	}

	if syncState := schedule.ApplySync(activeSync, nowMS); syncState.Active {
		resp.Ok = true
		resp.SyncedPlayback = true
		resp.MediaID = syncState.MediaID
		resp.OffsetMS = syncState.OffsetMS
		if syncEvent.MediaDetail != nil {
			dto := toMediaDTO(*syncEvent.MediaDetail)
			resp.Media = &dto
			resp.DurationMS = int64(syncEvent.MediaDetail.DurationSeconds) * 1000
		}
	} else {
		state := schedule.Now(schedule.Window{ID: windowID, CycleAnchorMS: win.CycleAnchor.UnixMilli()}, scheduleVersions, s.Config.CycleSeconds*1000, nowMS)
		resp.Ok = state.Ok
		if state.Ok {
			resp.MediaID = state.MediaID
			resp.OffsetMS = state.OffsetMS
			resp.DurationMS = state.DurationMS
			resp.ItemEndsAt = rfc3339Milli(time.UnixMilli(state.ItemEndsAtMS).UTC())
			resp.CycleEndsAt = rfc3339Milli(time.UnixMilli(state.CycleEndsAtMS).UTC())
			for _, v := range versions {
				if v.ID == state.VersionID {
					for _, it := range v.Items {
						if it.MediaID == state.MediaID && it.MediaDetail != nil {
							dto := toMediaDTO(*it.MediaDetail)
							resp.Media = &dto
							break
						}
					}
					break
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
