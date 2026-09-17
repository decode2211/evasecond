package httpapi

import (
	"net/http"
	"time"
)

type timeResponse struct {
	ServerTime string `json:"server_time"`
}

// handleTime answers "what time is it, according to the server?" Browsers
// call this (several times, taking the fastest round trip) to estimate how
// far their own clock is off from the server's, so every window's timeline
// math can be based on the same clock instead of each visitor's
// potentially-wrong system clock.
func (s *Server) handleTime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, timeResponse{ServerTime: rfc3339Milli(time.Now())})
}
