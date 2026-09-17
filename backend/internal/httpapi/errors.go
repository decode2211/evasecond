package httpapi

import "net/http"

// apiError is the shape of every error response this API sends:
// {"error": {"code": "...", "message": "..."}}. Code is a short, stable,
// machine-readable label a client can branch on; message is a
// human-readable explanation for logs or debugging.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorResponse struct {
	Error apiError `json:"error"`
}

// writeError sends a consistent {"error": {...}} JSON body with the given
// HTTP status code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiErrorResponse{Error: apiError{Code: code, Message: message}})
}

func badRequest(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusBadRequest, code, message)
}

func notFound(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusNotFound, code, message)
}

func internalError(w http.ResponseWriter, message string) {
	writeError(w, http.StatusInternalServerError, "internal_error", message)
}
