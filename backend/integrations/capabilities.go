package integrations

import (
	"context"
	"encoding/json"
	"sync"
)

// capabilities.go adds the small, optional extension the CI-error relay needs
// (ci#607) on top of the base integration interface, without touching the
// external go-integration-interface module and without forcing every
// integration to implement anything new.
//
// An integration that wants to consume relayed events (today: `ci_failure`)
// registers an EventSink under its own name from its bootstrap/route
// registration. Integrations that do not implement the capability are
// completely unaffected: the sink map stays empty and lookups simply miss, so
// this extension is backwards compatible by construction.
//
// The contract is deliberately narrow and secret free:
//
//	type EventSink interface {
//	    HandleIntegrationEvent(ctx context.Context, kind string, payload json.RawMessage) (any, error)
//	}
//
// A sink receives the *raw* kind-specific payload plus the already-lower-cased
// kind and owns the interpretation. Routing, retries and rendering therefore
// stay in the consumer (the agent chooser / development lead), while external
// systems (CI) only emit a payload.

// EventSink is the optional capability an integration may provide to consume
// externally relayed, versioned integration events.
type EventSink interface {
	HandleIntegrationEvent(ctx context.Context, kind string, payload json.RawMessage) (any, error)
}

var (
	eventSinksMu sync.RWMutex
	eventSinks   = map[string]EventSink{}
)

// RegisterEventSink attaches an EventSink to an integration name. Registering a
// nil sink is a no-op, so integrations can call it unconditionally.
func RegisterEventSink(integrationName string, sink EventSink) {
	if sink == nil {
		return
	}
	name := normalizeIntegrationName(integrationName)
	if name == "" {
		return
	}
	eventSinksMu.Lock()
	defer eventSinksMu.Unlock()
	eventSinks[name] = sink
}

// EventSinkFor returns the EventSink registered for an integration, if any.
func EventSinkFor(integrationName string) (EventSink, bool) {
	eventSinksMu.RLock()
	defer eventSinksMu.RUnlock()
	sink, ok := eventSinks[normalizeIntegrationName(integrationName)]
	return sink, ok
}

// ClearEventSinks drops all registered sinks (bootstrap/tests only).
func ClearEventSinks() {
	eventSinksMu.Lock()
	defer eventSinksMu.Unlock()
	eventSinks = map[string]EventSink{}
}
