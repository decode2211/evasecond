// These are integration tests: unlike schedule_test.go (which tests pure
// math with no I/O), these send real HTTP requests through the real router
// against a real Postgres database, to prove the handlers, the store's SQL,
// and the scheduler all work correctly wired together.
//
// They need a database to run against. Set TEST_DATABASE_URL to a Postgres
// connection string to run them (see docker-compose.yml for a ready-made
// local one), e.g.:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/eva2_test?sslmode=disable go test ./internal/httpapi/...
//
// Without it, these tests skip themselves with a clear message rather than
// failing.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"eva2/backend/internal/config"
	"eva2/backend/internal/db"
	"eva2/backend/internal/store"
)

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping HTTP integration tests. Set it to a Postgres connection string to run them, e.g. postgres://postgres:postgres@localhost:5432/eva2_test?sslmode=disable")
	}

	ctx := context.Background()
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))

	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, quietLog); err != nil {
		t.Fatalf("migrating test database: %v", err)
	}

	// Every test starts from a clean slate so tests don't leak state into
	// each other.
	if _, err := pool.Exec(ctx, `TRUNCATE sync_events, playlist_items, playlist_versions, windows, media RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncating test database: %v", err)
	}

	cfg := config.Config{
		Port:         "0",
		DatabaseURL:  url,
		CycleSeconds: 18000,
		SyncLeadMS:   1500,
		SeedOnStart:  false,
	}
	s := &Server{Store: store.New(pool), Pool: pool, Config: cfg, Log: quietLog}
	return s, NewRouter(s)
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
}

func insertTestWindow(t *testing.T, s *Server, id, name string) {
	t.Helper()
	_, err := s.Pool.Exec(context.Background(),
		`INSERT INTO windows (id, name, cycle_anchor) VALUES ($1, $2, $3)`,
		id, name, time.Now().UTC().Truncate(time.Minute),
	)
	if err != nil {
		t.Fatalf("inserting test window %s: %v", id, err)
	}
}

func createTestMedia(t *testing.T, s *Server, handler http.Handler, name, mediaType, url string, durationSeconds int) mediaDTO {
	t.Helper()
	body := createMediaRequest{Name: name, Type: mediaType, DurationSeconds: durationSeconds}
	if url != "" {
		body.URL = &url
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/media", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating test media: status %d, body %s", rec.Code, rec.Body.String())
	}
	var m mediaDTO
	decodeBody(t, rec, &m)
	return m
}

func TestAddPlaylistItem_CreatesNewVersionWithBothItems(t *testing.T) {
	s, handler := newTestServer(t)
	insertTestWindow(t, s, "W1", "Window 1")

	m1 := createTestMedia(t, s, handler, "Photo 1", "image", "https://example.com/1.jpg", 10)
	m2 := createTestMedia(t, s, handler, "Photo 2", "image", "https://example.com/2.jpg", 8)

	rec1 := doJSON(t, handler, http.MethodPost, "/api/windows/W1/items", addPlaylistItemRequest{MediaID: m1.ID})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first add: status %d, body %s", rec1.Code, rec1.Body.String())
	}
	var resp1 addPlaylistItemResponse
	decodeBody(t, rec1, &resp1)
	if len(resp1.Version.Items) != 1 || resp1.Version.Items[0].MediaID != m1.ID {
		t.Fatalf("first version items = %+v, want exactly [%s]", resp1.Version.Items, m1.ID)
	}
	if resp1.EffectiveAt == "" {
		t.Fatal("expected effective_at to be set on the response")
	}

	// Second add, back-to-back with the first — this is the scenario the
	// PENDING VERSIONS correction called out: the new version must be
	// built on top of the first (still possibly-pending) version, so both
	// items end up together in one final version rather than the second
	// add silently discarding the first.
	rec2 := doJSON(t, handler, http.MethodPost, "/api/windows/W1/items", addPlaylistItemRequest{MediaID: m2.ID})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second add: status %d, body %s", rec2.Code, rec2.Body.String())
	}
	var resp2 addPlaylistItemResponse
	decodeBody(t, rec2, &resp2)
	if len(resp2.Version.Items) != 2 {
		t.Fatalf("second version items = %+v, want 2 items (both M1 and M2)", resp2.Version.Items)
	}
	if resp2.Version.Items[0].MediaID != m1.ID || resp2.Version.Items[1].MediaID != m2.ID {
		t.Fatalf("second version items = %+v, want [%s, %s] in order", resp2.Version.Items, m1.ID, m2.ID)
	}
}

func TestAddPlaylistItem_UnknownWindow(t *testing.T) {
	s, handler := newTestServer(t)
	m := createTestMedia(t, s, handler, "Photo", "image", "https://example.com/1.jpg", 10)

	rec := doJSON(t, handler, http.MethodPost, "/api/windows/DOES-NOT-EXIST/items", addPlaylistItemRequest{MediaID: m.ID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorResponse
	decodeBody(t, rec, &errResp)
	if errResp.Error.Code != "window_not_found" {
		t.Fatalf("error code = %q, want %q", errResp.Error.Code, "window_not_found")
	}
}

func TestAddPlaylistItem_UnknownMedia(t *testing.T) {
	s, handler := newTestServer(t)
	insertTestWindow(t, s, "W1", "Window 1")

	rec := doJSON(t, handler, http.MethodPost, "/api/windows/W1/items", addPlaylistItemRequest{MediaID: "no-such-media"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorResponse
	decodeBody(t, rec, &errResp)
	if errResp.Error.Code != "media_not_found" {
		t.Fatalf("error code = %q, want %q", errResp.Error.Code, "media_not_found")
	}
}

func TestCreateMedia_ValidationErrors(t *testing.T) {
	_, handler := newTestServer(t)

	cases := []struct {
		name string
		body createMediaRequest
	}{
		{"unknown type", createMediaRequest{Name: "X", Type: "bogus", DurationSeconds: 10}},
		{"missing url for image", createMediaRequest{Name: "X", Type: "image", DurationSeconds: 10}},
		{"zero duration", createMediaRequest{Name: "X", Type: "blank", DurationSeconds: 0}},
		{"empty name", createMediaRequest{Name: "", Type: "blank", DurationSeconds: 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doJSON(t, handler, http.MethodPost, "/api/media", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			var errResp apiErrorResponse
			decodeBody(t, rec, &errResp)
			if errResp.Error.Code != "validation_error" {
				t.Fatalf("error code = %q, want %q", errResp.Error.Code, "validation_error")
			}
		})
	}
}

func TestSync_CreateAppearsInStateThenCancelRemovesIt(t *testing.T) {
	s, handler := newTestServer(t)
	m := createTestMedia(t, s, handler, "Sync Clip", "video", "https://example.com/clip.mp4", 30)

	createRec := doJSON(t, handler, http.MethodPost, "/api/sync", createSyncRequest{MediaID: m.ID, DurationSeconds: 20})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("creating sync: status %d, body %s", createRec.Code, createRec.Body.String())
	}
	var syncResp syncDTO
	decodeBody(t, createRec, &syncResp)
	if syncResp.MediaID != m.ID {
		t.Fatalf("sync media_id = %q, want %q", syncResp.MediaID, m.ID)
	}

	stateRec := doJSON(t, handler, http.MethodGet, "/api/state", nil)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("getting state: status %d, body %s", stateRec.Code, stateRec.Body.String())
	}
	var state stateBody
	decodeBody(t, stateRec, &state)
	if state.Sync == nil || state.Sync.MediaID != m.ID {
		t.Fatalf("state.sync = %+v, want an active sync for media %s", state.Sync, m.ID)
	}

	cancelRec := doJSON(t, handler, http.MethodDelete, "/api/sync", nil)
	if cancelRec.Code != http.StatusNoContent {
		t.Fatalf("cancelling sync: status %d, body %s", cancelRec.Code, cancelRec.Body.String())
	}

	stateRec2 := doJSON(t, handler, http.MethodGet, "/api/state", nil)
	var state2 stateBody
	decodeBody(t, stateRec2, &state2)
	if state2.Sync != nil {
		t.Fatalf("state.sync = %+v after cancel, want nil", state2.Sync)
	}
}

func TestSync_NewSyncReplacesPrevious(t *testing.T) {
	s, handler := newTestServer(t)
	m1 := createTestMedia(t, s, handler, "Clip 1", "video", "https://example.com/1.mp4", 30)
	m2 := createTestMedia(t, s, handler, "Clip 2", "video", "https://example.com/2.mp4", 30)

	doJSON(t, handler, http.MethodPost, "/api/sync", createSyncRequest{MediaID: m1.ID, DurationSeconds: 60})
	rec := doJSON(t, handler, http.MethodPost, "/api/sync", createSyncRequest{MediaID: m2.ID, DurationSeconds: 60})
	if rec.Code != http.StatusCreated {
		t.Fatalf("replacing sync: status %d, body %s", rec.Code, rec.Body.String())
	}

	stateRec := doJSON(t, handler, http.MethodGet, "/api/state", nil)
	var state stateBody
	decodeBody(t, stateRec, &state)
	if state.Sync == nil || state.Sync.MediaID != m2.ID {
		t.Fatalf("state.sync = %+v, want the newer sync for media %s", state.Sync, m2.ID)
	}
}

func TestGetState_ETagReturns304WhenUnchanged(t *testing.T) {
	s, handler := newTestServer(t)
	insertTestWindow(t, s, "W1", "Window 1")

	rec1 := doJSON(t, handler, http.MethodGet, "/api/state", nil)
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag header on the first response")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 when If-None-Match matches the current ETag", rec2.Code)
	}
}

func TestHealth(t *testing.T) {
	_, handler := newTestServer(t)
	rec := doJSON(t, handler, http.MethodGet, "/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}
