// Package chatstate defines the shared chat state vocabulary and the registry
// of live state providers for external chat backends. It intentionally has no
// dependencies on API packages so both the chat status endpoints and the
// integration modules can use it without import cycles.
package chatstate

import (
	"strings"
	"sync"
)

// State is the high-level chat state reported by the chat states APIs.
type State string

const (
	StateIdle              State = "idle"
	StateActive            State = "active"
	StateFinished          State = "finished"
	StateFailed            State = "failed"
	StateNeedsConfirmation State = "needs_confirmation"
)

// BackendState is the live activity level an external chat backend reports
// for a chat.
type BackendState string

const (
	BackendStateIdle    BackendState = "idle"
	BackendStateRunning BackendState = "running"
)

// BackendStateFunc reports the live activity of an external chat backend for
// a single chat. ok is false when the backend has no state information for
// the chat.
type BackendStateFunc func(chatUUID string) (BackendState, bool)

var (
	backendStateMu       sync.RWMutex
	backendStateRegistry = map[string]BackendStateFunc{}
)

// RegisterBackendStateProvider registers the live state provider for an
// external chat backend (keyed by the shared config "backend" value).
func RegisterBackendStateProvider(backend string, fn BackendStateFunc) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" || fn == nil {
		return
	}
	backendStateMu.Lock()
	defer backendStateMu.Unlock()
	backendStateRegistry[backend] = fn
}

// LookupBackendStateProvider returns the registered live state provider for a
// chat backend.
func LookupBackendStateProvider(backend string) (BackendStateFunc, bool) {
	backend = strings.ToLower(strings.TrimSpace(backend))
	backendStateMu.RLock()
	defer backendStateMu.RUnlock()
	fn, ok := backendStateRegistry[backend]
	return fn, ok
}
