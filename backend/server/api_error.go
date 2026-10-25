package server

import (
	"encoding/json"
	"net/http"

	"backend/logging"
)

// APIErrorResponse is the JSON envelope every API-level rejection uses. The
// middleware in this package guards *every* /api/v1 route, so a rejection coming
// from an authentication/authorization check must look exactly like a rejection
// coming from a handler: a JSON body with a machine readable code. Generated
// clients (and the live-runtime canary) call response.json() unconditionally, so
// a plain-text body makes an expected 4xx look like a client bug
// (open-chat-go-ci#865).
type APIErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// writeAPIError writes a JSON error response with a stable machine readable
// code. It replaces http.Error on the middleware reject paths.
func writeAPIError(w http.ResponseWriter, status int, code string, message string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(APIErrorResponse{Error: message, Code: code}); err != nil {
		logging.Errorf("failed to encode api error response: %v", err)
	}
}
