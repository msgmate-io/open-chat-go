// Package apierror renders the JSON envelope every API-level answer uses, so
// that generated clients which call response.json() unconditionally see the
// real payload (or the real HTTP error) instead of a JSONDecodeError on an
// empty/plain-text body (open-chat-go-ci#854, open-chat-go-ci#865).
package apierror

import (
	"encoding/json"
	"net/http"

	"backend/logging"
)

// Response is the JSON error envelope. Code is a stable, machine readable
// identifier that the canary and generated clients can match on.
type Response struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// ResponseWithDetail extends the envelope with a Detail field carrying the
// underlying server-side cause of a rejection. It is the envelope of choice
// for 5xx responses: the Live Runtime Canary parses the body with
// response.json() and reports it, so a detail here is what lets the next
// canary run name the real create-interaction failure instead of dying in the
// decoder with an empty body (open-chat-go-ci#865).
type ResponseWithDetail struct {
	Response
	Detail string `json:"detail,omitempty"`
}

// Write writes a JSON error response with a stable machine readable code.
// A nil writer is tolerated (the write is skipped) so callers can pass through
// an optional writer without a nil guard at every site.
func Write(w http.ResponseWriter, status int, code string, message string) {
	WriteDetailed(w, status, code, message, "")
}

// WriteDetailed writes a JSON error response, optionally carrying the
// underlying server-side cause in the detail field. A non-empty detail keeps
// the generic client-facing message stable while still surfacing the root
// cause to generated clients and the canary.
func WriteDetailed(w http.ResponseWriter, status int, code string, message string, detail string) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(ResponseWithDetail{Response: Response{Error: message, Code: code}, Detail: detail}); err != nil {
		logging.Errorf("failed to encode api error response: %v", err)
	}
}

// WriteJSON writes a JSON success response with the same envelope discipline as
// Write: the Content-Type is set before WriteHeader, and the encoding error is
// logged instead of dropped. It reports whether the body was fully written.
//
// The bot surface (create/list/get/update/delete bot, save-config, agent
// dispatch, create-interaction) writes its success payloads through WriteJSON
// (ci#865): a naked json.NewEncoder(w).Encode(...) leaves the writer with an
// empty body if the connection drops between WriteHeader and Encode, and a
// generated client that calls response.json() on that empty 200 would silently
// continue instead of surfacing the failure.
func WriteJSON(w http.ResponseWriter, status int, payload interface{}) bool {
	if w == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logging.Errorf("failed to encode json response (status %d): %v", status, err)
		return false
	}
	return true
}
