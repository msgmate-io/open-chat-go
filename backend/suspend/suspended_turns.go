// Package suspend holds the in-process registry of tool-confirmation
// suspensions (ci#1116).
//
// A generation that parks on a confirmable tool call must not be finished: the
// approve/reject decision has to be delivered back into the *same* generation so
// the tool result lands in the same message slice (currentMessages /
// executedToolResults) and the transcript keeps its shape - one turn, one user
// question, one tool result - instead of a finished turn plus a queued new one.
//
// The registry is the hand-off point between the parked generation
// (backend/api/msgmate) and the confirmation handler
// (backend/api/tools/confirmable_actions.go). It therefore lives in this leaf
// package: backend/api/tools already imports backend/api/msgmate, so a registry
// owned by either of them would create an import cycle.
//
// The registry is memory-only. It never writes through the DB pool, so the
// ci#811 untracked-DB-writer guard needs no debt-list entry; the optional
// sweeper goroutine is still self-draining (StartSweeper returns a stop func and
// also stops on ctx cancel). An expired or unknown suspension simply degrades to
// the existing queued EnqueueBotReply continuation.
package suspend

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultTTL bounds how long a parked turn waits for a decision. It is well
// inside the bot-reply task timeout, so a parked turn always resolves before the
// worker would be killed.
const DefaultTTL = 10 * time.Minute

// Decision is the user's answer delivered into a parked turn.
type Decision struct {
	// Result is the text the model sees as the tool result.
	Result string
	// Approved marks an approval (executed, or executed with a reason).
	Approved bool
	// Rejected marks an explicit rejection (as opposed to an approval).
	Rejected bool
	// Reason carries an optional approve-with-reason / reject reason.
	Reason string
	// ActionID echoes the confirmation id the decision belongs to.
	ActionID string
}

// SuspendedTurn is one parked tool call plus everything needed to continue the
// generation it belongs to.
type SuspendedTurn struct {
	ChatUUID       string
	ToolCallID     string
	MessageUUID    string
	BotUsername    string
	ConfirmationID string
	CreatedAt      time.Time
	// Resume continues the parked generation with the user's decision. It is
	// called at most once per parked turn (Take removes the record).
	Resume func(ctx context.Context, decision Decision) error
}

func turnKey(chatUUID, toolCallID string) string { return chatUUID + "/" + toolCallID }

// Registry tracks parked turns. The zero value is not usable; use New.
type Registry struct {
	mu    sync.Mutex
	turns map[string]*SuspendedTurn
	ttl   time.Duration
	now   func() time.Time
}

// New returns an empty registry with the default TTL.
func New() *Registry { return NewWithTTL(DefaultTTL, nil) }

// NewWithTTL returns an empty registry with an explicit TTL and clock (tests).
func NewWithTTL(ttl time.Duration, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Registry{turns: map[string]*SuspendedTurn{}, now: now, ttl: ttl}
}

// TTL reports the configured suspension lifetime.
func (r *Registry) TTL() time.Duration { return r.ttl }

// Park registers a suspended turn. It reports false (and stores nothing) for a
// record that cannot be resumed, so the caller keeps the queued-continuation
// behaviour instead of parking a turn nobody can wake.
func (r *Registry) Park(t *SuspendedTurn) bool {
	if t == nil || t.ChatUUID == "" || t.ToolCallID == "" || t.Resume == nil {
		return false
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = r.now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	r.turns[turnKey(t.ChatUUID, t.ToolCallID)] = t
	return true
}

// Take removes and returns the suspended turn for the key. Removing on take
// guarantees a decision resumes the generation exactly once: a second click
// misses and must not queue a second continuation for the same turn.
func (r *Registry) Take(chatUUID, toolCallID string) (*SuspendedTurn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := turnKey(chatUUID, toolCallID)
	turn, ok := r.turns[key]
	if !ok {
		return nil, false
	}
	delete(r.turns, key)
	if !r.now().Before(turn.CreatedAt.Add(r.ttl)) {
		return nil, false
	}
	return turn, true
}

// Peek reports the suspended turn for the key without consuming it (status).
func (r *Registry) Peek(chatUUID, toolCallID string) (*SuspendedTurn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, ok := r.turns[turnKey(chatUUID, toolCallID)]
	if !ok {
		return nil, false
	}
	if !r.now().Before(turn.CreatedAt.Add(r.ttl)) {
		return nil, false
	}
	return turn, true
}

// Parked reports whether a live suspension exists for the key. The interaction
// status path uses it to report "waiting for confirmation" on the existing turn
// instead of treating the turn as finished.
func (r *Registry) Parked(chatUUID, toolCallID string) bool {
	_, ok := r.Peek(chatUUID, toolCallID)
	return ok
}

// Forget drops a suspension without delivering anything (stop, shutdown).
func (r *Registry) Forget(chatUUID, toolCallID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.turns, turnKey(chatUUID, toolCallID))
}

// Expire drops every suspension older than the TTL and returns the evicted
// records so the caller can log that those turns fall back to the queued
// continuation.
func (r *Registry) Expire() []*SuspendedTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.expireLocked()
}

// Len reports the number of live suspensions (metrics/status).
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked()
	return len(r.turns)
}

// StartSweeper evicts expired suspensions on a ticker until ctx is cancelled or
// the returned stop func is called (whichever comes first). The goroutine is
// self-draining: stop is idempotent and StartSweeper's goroutine exits on it, so
// server teardown can wire it without a ci#811 debt-list entry. The sweeper only
// touches in-memory state - it never writes through the DB pool.
func (r *Registry) StartSweeper(ctx context.Context) (stop func()) {
	interval := r.ttl / 4
	if interval <= 0 {
		interval = time.Minute
	}
	sweepCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				for _, turn := range r.Expire() {
					// Lazy expiry is authoritative for the handler; the sweep
					// only keeps the map from growing.
					_ = turn
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// expireLocked evicts expired suspensions and returns them.
func (r *Registry) expireLocked() []*SuspendedTurn {
	now := r.now()
	var expired []*SuspendedTurn
	for key, turn := range r.turns {
		if !now.Before(turn.CreatedAt.Add(r.ttl)) {
			expired = append(expired, turn)
			delete(r.turns, key)
		}
	}
	return expired
}

// defaultRegistry is the process-wide registry the parked generation
// (backend/api/msgmate) and the confirmation handler
// (backend/api/tools/confirmable_actions.go) share.
var defaultRegistry = New()

// Default returns the process-wide registry.
func Default() *Registry { return defaultRegistry }

// ResumeWith takes the suspension for the key and delivers the decision into the
// parked generation. It reports true only when a live suspension was found and
// its Resume func ran; false means the turn is unknown or expired, so the caller
// must fall back to the queued EnqueueBotReply continuation. It never panics on a
// Resume error - the error is returned so the caller can log it and still fall
// back if the generation could not be continued.
func (r *Registry) ResumeWith(ctx context.Context, chatUUID, toolCallID string, decision Decision) (bool, error) {
	turn, ok := r.Take(chatUUID, toolCallID)
	if !ok {
		return false, nil
	}
	if turn.Resume == nil {
		return false, nil
	}
	return true, turn.Resume(ctx, decision)
}

// Waiter is the parked-generation side of a suspension: the generation blocks in
// Await while the confirmation handler delivers the executed tool result through
// Deliver. Both sides share one buffered channel, so a decision that arrives just
// before the generation starts waiting is not lost.
type Waiter struct {
	ch       chan Decision
	mu       sync.Mutex
	consumed bool
}

// NewWaiter returns a waiter for one parked turn.
func NewWaiter() *Waiter { return &Waiter{ch: make(chan Decision, 1)} }

// Deliver hands the decision to the parked generation. It fails when the decision
// was already delivered or when the generation stopped waiting (expiry, stop),
// which tells the confirmation handler to fall back to the queued continuation
// instead of claiming the turn was resumed.
func (w *Waiter) Deliver(_ context.Context, decision Decision) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.consumed {
		return errors.New("suspended turn already resumed")
	}
	select {
	case w.ch <- decision:
		w.consumed = true
		return nil
	default:
		return errors.New("suspended turn is no longer waiting")
	}
}

// Await blocks until the decision arrives or ctx ends (expiry, stop, shutdown).
// It reports false when the generation must give up and finish the turn as it did
// before ci#1116.
func (w *Waiter) Await(ctx context.Context) (Decision, bool) {
	select {
	case decision := <-w.ch:
		return decision, true
	case <-ctx.Done():
		return Decision{}, false
	}
}

// ParkWaiter parks a suspension whose resume delivers into the returned waiter.
// The caller then awaits it with a TTL-bounded context.
func ParkWaiter(r *Registry, chatUUID, toolCallID, messageUUID, botUsername, confirmationID string) (*Waiter, bool) {
	waiter := NewWaiter()
	if !r.Park(&SuspendedTurn{
		ChatUUID:       chatUUID,
		ToolCallID:     toolCallID,
		MessageUUID:    messageUUID,
		BotUsername:    botUsername,
		ConfirmationID: confirmationID,
		Resume:         waiter.Deliver,
	}) {
		return nil, false
	}
	return waiter, true
}
