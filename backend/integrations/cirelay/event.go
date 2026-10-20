package cirelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"backend/nativecoder"
)

// event.go adds the generic ingestion hook the ticket asks for: instead of a
// dedicated ci_failure-only endpoint an external system may POST a small,
// versioned `integration_event` envelope to a single, kind-dispatched path.
// The core resolves the kind and hands the kind-specific payload to the same
// routing path as the dedicated hook, so the CI side stays a thin caller of the
// contract (payload + auth only, no dispatch/routing logic).
//
// The envelope is deliberately minimal and secret free:
//
//	{
//	  "schema_version": 1,
//	  "kind": "ci_failure",
//	  "payload": { "repo": "...", "pr_number": 123, ... }
//	}
//
// Unknown kinds are rejected with 422 instead of being guessed at, so the
// envelope can grow new event types without breaking this hook (backwards
// compatible: the ci_failure-only RelayHandler keeps working unchanged).

const (
	// EventsPath is the generic, kind-dispatched ingestion endpoint.
	EventsPath = "/api/integrations/event"
	// EventSchemaVersion is the envelope contract version understood here. A
	// newer envelope version is rejected rather than misinterpreted.
	EventSchemaVersion = 1
	// EventKindIntegration is the envelope kind a wrapped event may advertise.
	EventKindIntegration = "integration_event"
)

// ErrUnsupportedEventKind is returned for an envelope whose kind this relay
// does not (yet) route. Callers map it to 422 rather than 502.
var ErrUnsupportedEventKind = errors.New("unsupported integration_event kind")

// ErrInvalidEvent marks a kind payload the relay understands the shape of but
// rejects (missing repo, unknown schema version, ...). Also mapped to 422.
var ErrInvalidEvent = errors.New("invalid integration_event payload")

// Event is the generic ingestion envelope: a kind plus its raw payload. The
// payload is only decoded by the handler for that kind, so a new kind cannot
// affect the existing ci_failure path.
type Event struct {
	SchemaVersion int             `json:"schema_version,omitempty"`
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// CanonicalKind is the lower-cased, trimmed kind used for dispatch.
func (e Event) CanonicalKind() string { return strings.ToLower(strings.TrimSpace(e.Kind)) }

// Validate rejects an envelope that cannot be dispatched.
func (e Event) Validate() error {
	if e.SchemaVersion < 0 {
		return fmt.Errorf("invalid integration_event schema_version %d", e.SchemaVersion)
	}
	if e.SchemaVersion > EventSchemaVersion {
		return fmt.Errorf("unsupported integration_event schema_version %d (max %d)", e.SchemaVersion, EventSchemaVersion)
	}
	if e.CanonicalKind() == "" {
		return errors.New("integration_event is missing kind")
	}
	if len(e.Payload) == 0 {
		return errors.New("integration_event is missing payload")
	}
	return nil
}

// ParseEnvelope decodes and validates a generic ingestion envelope.
func ParseEnvelope(raw []byte) (Event, error) {
	var event Event
	if len(raw) == 0 {
		return event, errors.New("empty integration_event payload")
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, fmt.Errorf("invalid integration_event payload: %w", err)
	}
	if err := event.Validate(); err != nil {
		return event, err
	}
	return event, nil
}

// IsCIFailure reports whether the envelope carries a CI failure event (the
// canonical kind or its accepted `ci_error` alias).
func (e Event) IsCIFailure() bool {
	switch e.CanonicalKind() {
	case nativecoder.CIFailureEventKind, nativecoder.CIFailureEventAliasKind:
		return true
	}
	return false
}

// Ingest validates the envelope, dispatches on its kind and returns the routing
// resolution. Only CI-failure kinds are routed today; anything else yields
// ErrUnsupportedEventKind so the caller can answer 422.
func (r Relay) Ingest(ctx context.Context, event Event) (Resolution, error) {
	if err := event.Validate(); err != nil {
		return Resolution{}, err
	}
	if !event.IsCIFailure() {
		return Resolution{}, fmt.Errorf("%w: %q", ErrUnsupportedEventKind, event.Kind)
	}
	ciEvent, err := ParseEvent(event.Payload)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	return r.Handle(ctx, ciEvent)
}

// EventsHandler is the generic HTTP ingestion hook: POST EventsPath with the
// relay bearer token and an integration_event envelope. Like RelayHandler it
// performs no routing of its own; unsupported kinds are 422, other failures are
// 502 and the relay token is never logged or echoed back.
func EventsHandler(relay Relay, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if token != "" && !validToken(req.Header.Get("Authorization"), token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		raw, err := readLimited(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		event, err := ParseEnvelope(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := relay.Ingest(req.Context(), event)
		if err != nil {
			if errors.Is(err, ErrUnsupportedEventKind) || errors.Is(err, ErrInvalidEvent) {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, "relay failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
}
