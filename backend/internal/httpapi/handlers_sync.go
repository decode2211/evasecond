package httpapi

import (
	"errors"
	"net/http"

	"eva2/backend/internal/store"
)

type createSyncRequest struct {
	MediaID         string `json:"media_id"`
	DurationSeconds int    `json:"duration_seconds"`
}

// handleCreateSync starts a sync-playback event: from now (plus a short
// lead time — see config.SyncLeadMS) until duration_seconds later, every
// window shows this one media item instead of its own playlist. If a sync
// is already running, it is cancelled and immediately replaced by this new
// one (see internal/store.CreateSync).
func (s *Server) handleCreateSync(w http.ResponseWriter, r *http.Request) {
	var req createSyncRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid_json", "request body must be valid JSON matching {media_id, duration_seconds}")
		return
	}
	if req.MediaID == "" {
		badRequest(w, "validation_error", "media_id is required")
		return
	}
	if req.DurationSeconds <= 0 {
		badRequest(w, "validation_error", "duration_seconds must be greater than 0")
		return
	}

	ev, err := s.Store.CreateSync(r.Context(), req.MediaID, req.DurationSeconds, s.Config.SyncLeadMS)
	switch {
	case errors.Is(err, store.ErrMediaNotFound):
		notFound(w, "media_not_found", "no media with that id exists")
		return
	case err != nil:
		s.Log.Error("creating sync", "error", err)
		internalError(w, "failed to start sync")
		return
	}

	writeJSON(w, http.StatusCreated, toSyncDTO(ev))
}

// handleCancelSync ends whatever sync is currently active or about to
// start. Every window immediately resumes its own normal playlist timeline
// (which was never paused — see the README's "resume model" section for
// why). It is not an error to cancel when nothing is syncing.
func (s *Server) handleCancelSync(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.CancelSync(r.Context()); err != nil {
		s.Log.Error("cancelling sync", "error", err)
		internalError(w, "failed to cancel sync")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
