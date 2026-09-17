// Package httpapi is the web layer: it turns incoming HTTP requests into
// calls on internal/store and internal/schedule, and turns their answers
// back into JSON responses. It never contains scheduling math itself (that
// belongs in internal/schedule) and never writes raw SQL itself (that
// belongs in internal/store) — its only job is translating between "HTTP
// request" and "internal call".
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

// rfc3339Milli formats a time the way every response in this API reports
// timestamps: RFC3339 with millisecond precision and a "Z" for UTC, e.g.
// "2026-09-17T21:04:33.512Z". Millisecond precision (rather than the
// default which can show microseconds or drop trailing zeros) keeps every
// timestamp in every response the same shape, which makes them trivial to
// compare as plain strings and easy for the frontend to parse.
func rfc3339Milli(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// writeJSON sends v as a JSON response body with the given HTTP status
// code. It's a tiny helper, but funneling every response through one place
// means every handler sets the Content-Type header the same way and never
// forgets it.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON request body into v. It rejects unknown fields
// (DisallowUnknownFields) so a typo in a client's request — e.g. sending
// "media_Id" instead of "media_id" — fails loudly as a 400 instead of
// silently being ignored.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
