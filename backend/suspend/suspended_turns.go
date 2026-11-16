// Package suspend holds the in-process registry of tool-confirmation
// suspensions (ci#1116).
//
// A generation that parks on a confirmable tool call must not be finished: the
// approve/reject decision should be delivered back into the *same* generation so
// the tool result lands in the same message slice (currentMessages /
// executedToolResults) and the transcript keeps its shape - one turn, one user
// question, one tool result - instead of a finished turn plus a queued new one.
// The registry is the hand-off point between the parked generation
// (backend/api/msgmate) and the confirmation handler
// (backend/api/tools/confirmable_actions.go).
//
// It is deliberately memory-only and goroutine-free: expiry is evaluated lazily
// on lookup and on Resume, so no background writer needs a self-draining stop
// func wired into the server teardown (ci#811). An expired or unknown
// suspension simply degrades to the existing queued EnqueueBotReply
// continuation.
package suspend

import (
	"sync"
	"time"
)

// DefaultTTL bounds how long a parked generation waits for a decision. It is
// well inside the bot-reply task timeout, so a parked turn always resolves
// before the worker would be killed.
const DefaultTTL = 10 * time.Minute

// Decision is the user's answer delivered into a parked generation.
type Decision struct {
	// Result is the text the model sees as the tool result.
	Result string
	// Rejected marks an explicit rejection (as opposed to an approval).
	Rejected bool
	// Reason carries an optional approve-with-reason / reject reason.
	Reason string
}

// Key identifies one parked tool call. ChatUUID scopes the suspension to a
// chat, ToolCallID is the provider tool-call id that the confirmation widget
// already carries as its action id.
type Key struct {
	ChatUUID   string
	ToolCallID string
}

type registration struct {
	decisions chan Decision
	expiresAt time.Time
	// resolved marks a suspension whose decision was already delivered. The
	// record is kept for a short retention window so a duplicate click on the
	// same action is answered as "resumed" instead of degrading to a second
	// queued continuation for the same turn.
	resolved bool
}

// resolvedRetention is how long a resolved suspension stays known. It only has
// to outlive a double click on the confirmation widget.
const resolvedRetention = 2 * time.Minute

// Registry tracks parked turns. The zero value is not usable; use New.
type Registry struct {
	mu    sync.Mutex
	turns map[Key]*registration
	now   func() time.Time
	ttl   time.Duration
}

// New returns an empty registry with the default TTL.
func New() *Registry {
	return NewWithTTL(DefaultTTL, nil)
}

// NewWithTTL returns an empty registry with an explicit TTL and clock (tests).
func NewWithTTL(ttl time.Duration, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{turns: map[Key]*registration{}, now: now, ttl: ttl}
}

// TTL reports the configured suspension lifetime.
func (r *Registry) TTL() time.Duration { return r.ttl }

// Park registers a suspension and returns the channel a decision arrives on.
// A second Park for the same key replaces the previous suspension; the older
// generation is left to time out and fall back to the queued continuation.
func (r *Registry) Park(chatUUID, toolCallID string) <-chan Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	reg := &registration{decisions: make(chan Decision, 1), expiresAt: r.now().Add(r.ttl)}
	r.turns[Key{ChatUUID: chatUUID, ToolCallID: toolCallID}] = reg
	return reg.decisions
}

// Resume delivers a decision into the parked generation. It reports whether a
// live suspension existed; false means the caller must fall back to the queued
// EnqueueBotReply continuation.
func (r *Registry) Resume(chatUUID, toolCallID string, decision Decision) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := Key{ChatUUID: chatUUID, ToolCallID: toolCallID}
	reg, ok := r.turns[key]
	if !ok {
		return false
	}
	if !r.now().Before(reg.expiresAt) {
		delete(r.turns, key)
		return false
	}
	// A double click resumes the same turn: the first decision wins and the
	// caller must not queue a second continuation for it.
	if reg.resolved {
		return true
	}
	reg.resolved = true
	// Keep the resolved record only for the double-click window; the parked
	// generation owns the decision from here on.
	reg.expiresAt = r.now().Add(resolvedRetention)
	select {
	case reg.decisions <- decision:
	default:
	}
	return true
}

// Forget drops a suspension without delivering anything (stop, shutdown).
func (r *Registry) Forget(chatUUID, toolCallID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.turns, Key{ChatUUID: chatUUID, ToolCallID: toolCallID})
}

// defaultRegistry is the process-wide registry the parked generation
// (backend/api/msgmate) and the confirmation handler
// (backend/api/tools/confirmable_actions.go) share. It is created eagerly so
// both sides can use it without an init order dependency, and it needs no
// teardown: expiry is lazy and it starts no goroutine (ci#811).
var defaultRegistry = New()

// Default returns the process-wide registry.
func Default() *Registry { return defaultRegistry }

// Parked reports whether a live suspension exists for the key. The interaction
// status path uses it to report "waiting for confirmation" on the existing turn
// instead of treating the turn as finished. A suspension that was already
// resumed is not parked: that generation is awake again.
func (r *Registry) Parked(chatUUID, toolCallID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.turns[Key{ChatUUID: chatUUID, ToolCallID: toolCallID}]
	return ok && !reg.resolved && r.now().Before(reg.expiresAt)
}

// Len reports the number of live (un-resumed) suspensions (metrics/status).
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	live := 0
	for _, reg := range r.turns {
		if !reg.resolved {
			live++
		}
	}
	return live
}

// WaitDecided blocks the parked generation until a decision arrives or the
// suspension expires. expired reports that nothing was delivered, so the caller
// parks the turn terminally (and leaves the queued continuation to pick it up).
func (r *Registry) WaitDecided(decisions <-chan Decision, deadline time.Time) (decision Decision, ok, expired bool) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case d, open := <-decisions:
		if !open {
			return Decision{}, false, true
		}
		return d, true, false
	case <-timer.C:
		return Decision{}, false, true
	}
}

// sweepLocked evicts expired suspensions.
func (r *Registry) sweepLocked() {
	now := r.now()
	for key, reg := range r.turns {
		if !now.Before(reg.expiresAt) {
			delete(r.turns, key)
		}
	}
}
