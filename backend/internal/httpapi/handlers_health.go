package httpapi

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

// handleHealth answers "is this server, and its connection to the
// database, working right now?" Render and other hosts poll this
// endpoint to decide whether the service is healthy enough to receive
// traffic.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbStatus := "ok"
	status := "ok"
	if err := s.Pool.Ping(r.Context()); err != nil {
		dbStatus = "error"
		status = "error"
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, healthResponse{Status: status, DB: dbStatus})
}
