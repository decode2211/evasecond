package httpapi

import (
	"net/http"
	"strings"

	"eva2/backend/internal/models"
)

// handleListMedia returns every media item that has ever been created, so
// the frontend's "add to window" control can offer a full picklist.
func (s *Server) handleListMedia(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListMedia(r.Context())
	if err != nil {
		s.Log.Error("listing media", "error", err)
		internalError(w, "failed to load media")
		return
	}
	dtos := make([]mediaDTO, len(items))
	for i, m := range items {
		dtos[i] = toMediaDTO(m)
	}
	writeJSON(w, http.StatusOK, dtos)
}

type createMediaRequest struct {
	Name            string  `json:"name"`
	Type            string  `json:"type"`
	URL             *string `json:"url"`
	DurationSeconds int     `json:"duration_seconds"`
}

// handleCreateMedia registers a brand new piece of content (image, video,
// or blank) that can then be added to any window's playlist. It only
// creates the media record — adding it to a specific window's playlist is
// a separate step (POST /api/windows/{id}/items), since the same media can
// be reused across many windows.
func (s *Server) handleCreateMedia(w http.ResponseWriter, r *http.Request) {
	var req createMediaRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequest(w, "invalid_json", "request body must be valid JSON matching {name, type, url, duration_seconds}")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		badRequest(w, "validation_error", "name is required")
		return
	}

	mediaType := models.MediaType(req.Type)
	switch mediaType {
	case models.MediaImage, models.MediaVideo:
		if req.URL == nil || strings.TrimSpace(*req.URL) == "" {
			badRequest(w, "validation_error", "url is required for image and video media")
			return
		}
	case models.MediaBlank:
		if req.URL != nil && strings.TrimSpace(*req.URL) != "" {
			badRequest(w, "validation_error", "blank media must not have a url")
			return
		}
		req.URL = nil
	default:
		badRequest(w, "validation_error", "type must be one of: image, video, blank")
		return
	}

	if req.DurationSeconds <= 0 {
		badRequest(w, "validation_error", "duration_seconds must be greater than 0")
		return
	}

	m, err := s.Store.CreateMedia(r.Context(), req.Name, mediaType, req.URL, req.DurationSeconds)
	if err != nil {
		s.Log.Error("creating media", "error", err)
		internalError(w, "failed to create media")
		return
	}
	writeJSON(w, http.StatusCreated, toMediaDTO(m))
}
