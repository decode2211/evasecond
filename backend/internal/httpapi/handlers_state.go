package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// stateBody is everything a client needs to render every window and know
// about any sync in progress. server_time is deliberately its own field
// rather than baked into the hash used for the ETag (see handleGetState):
// the rest of the payload only changes when something real changes
// (a playlist update, a new sync), but server_time changes every single
// request, so including it in the ETag would defeat the point of caching.
type stateBody struct {
	CycleSeconds int64            `json:"cycle_seconds"`
	Windows      []windowStateDTO `json:"windows"`
	Sync         *syncDTO         `json:"sync"`
	ServerTime   string           `json:"server_time"`
}

// handleGetState is the single endpoint the frontend polls every couple of
// seconds to learn about playlist changes and sync events. It supports
// HTTP's conditional-GET mechanism (ETag / If-None-Match): if nothing has
// changed since the client's last request, the server sends back a tiny
// 304 "Not Modified" instead of re-sending the whole payload, which keeps
// frequent polling cheap.
func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	windows, err := s.Store.ListWindows(ctx)
	if err != nil {
		s.Log.Error("listing windows", "error", err)
		internalError(w, "failed to load windows")
		return
	}
	versionsByWindow, err := s.Store.ListAllVersions(ctx)
	if err != nil {
		s.Log.Error("listing playlist versions", "error", err)
		internalError(w, "failed to load playlists")
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

	body := stateBody{CycleSeconds: s.Config.CycleSeconds}
	for _, win := range windows {
		body.Windows = append(body.Windows, toWindowStateDTO(win, versionsByWindow[win.ID], nowMS))
	}
	if syncEvent != nil {
		sd := toSyncDTO(*syncEvent)
		body.Sync = &sd
	}

	etag, err := computeETag(body)
	if err != nil {
		s.Log.Error("computing state etag", "error", err)
		internalError(w, "failed to compute state")
		return
	}

	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body.ServerTime = rfc3339Milli(now)
	writeJSON(w, http.StatusOK, body)
}

// computeETag hashes everything in body EXCEPT server_time (which is left
// as the zero value here on purpose, before the caller fills it in for the
// real response) so the ETag only changes when the actual playback-
// relevant state changes.
func computeETag(body stateBody) (string, error) {
	body.ServerTime = ""
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%q", hex.EncodeToString(sum[:])), nil
}
