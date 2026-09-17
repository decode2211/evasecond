package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"eva2/backend/internal/config"
	"eva2/backend/internal/store"
)

// Server holds everything the HTTP handlers need: access to the database
// (through Store and, for the health check, the raw pool), the validated
// configuration, and a logger. One Server is created at startup and its
// methods are wired up as handlers in NewRouter.
type Server struct {
	Store  *store.Store
	Pool   *pgxpool.Pool
	Config config.Config
	Log    *slog.Logger
}

// NewRouter builds the complete HTTP handler for this application: every
// route this API exposes, wrapped in CORS handling. It uses Go's standard
// net/http.ServeMux with method-aware patterns (e.g. "GET /api/state"),
// available since Go 1.22, instead of a third-party web framework.
func NewRouter(s *Server) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/time", s.handleTime)
	mux.HandleFunc("GET /api/state", s.handleGetState)

	mux.HandleFunc("GET /api/media", s.handleListMedia)
	mux.HandleFunc("POST /api/media", s.handleCreateMedia)

	mux.HandleFunc("POST /api/windows/{id}/items", s.handleAddPlaylistItem)
	mux.HandleFunc("GET /api/windows/{id}/now", s.handleWindowNow)

	mux.HandleFunc("POST /api/sync", s.handleCreateSync)
	mux.HandleFunc("DELETE /api/sync", s.handleCancelSync)

	return withCORS(s.Config.CORSOrigins, mux)
}
