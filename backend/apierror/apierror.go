// Package apierror renders the JSON error envelope every API-level rejection
// uses, so that generated clients which call response.json() unconditionally
// see the real HTTP error instead of a JSONDecodeError on an empty/plain-text
// body (open-chat-go-ci#854, open-chat-go-ci#865).
package apierror

import (
	"encoding/json"
	"net/http"

	"backend/logging"
)

// Response is the JSON envelope. Code is a stable, machine readable identifier.
type Response struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// Write writes a JSON error response with a stable machine readable code.
func Write(w http.ResponseWriter, status int, code string, message string) {
	if w == nil {
		return
	}
	if !WriteJSON(w, status, Response{Error: message, Code: code}) {
		logging.Errorf("failed to encode api error response %q (status %d, code %q)", message, status, code)
	}
}

// WriteJSON writes a JSON success/error payload with the JSON content type and
// returns false only when the payload cannot be encoded (a client-facing
// failure of the same kind as Write: a response the caller could not parse).
// Encode errors are logged by the caller, which keeps this helper free of a
// logging import cycle.
func WriteJSON(w http.ResponseWriter, status int, payload interface{}) bool {
	if w == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return false
	}
	return true
}
