package suspend

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func fakeClock(start time.Time) (*time.Time, func() time.Time) {
	cur := start
	return &cur, func() time.Time { return cur }
}

func parkedTurn(chat, call string, resume func(ctx context.Context, d Decision) error) *SuspendedTurn {
	return &SuspendedTurn{
		ChatUUID:       chat,
		ToolCallID:     call,
		MessageUUID:    "msg-1",
		BotUsername:    "bot",
		ConfirmationID: call,
		Resume:         resume,
	}
}

// A parked turn is taken exactly once: the decision resumes the generation and a
// second click must miss (so it cannot queue a second continuation).
func TestTakeReturnsSuspendedTurnExactlyOnce(t *testing.T) {
	now, clock := fakeClock(time.Unix(1000, 0))
	_ = now
	r := NewWithTTL(time.Minute, clock)
	if !r.Park(parkedTurn("chat", "call", func(context.Context, Decision) error { return nil })) {
		t.Fatal("Park rejected a resumable turn")
	}
	if !r.Parked("chat", "call") {
		t.Fatal("Parked() = false for a live suspension")
	}
	turn, ok := r.Take("chat", "call")
	if !ok || turn == nil {
		t.Fatal("first Take missed")
	}
	if turn.ChatUUID != "chat" || turn.ToolCallID != "call" || turn.MessageUUID != "msg-1" {
		t.Fatalf("unexpected turn identity: %+v", turn)
	}
	if _, ok := r.Take("chat", "call"); ok {
		t.Fatal("second Take must miss (decision can resume only once)")
	}
	if r.Parked("chat", "call") {
		t.Fatal("Parked() = true after Take")
	}
	if r.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", r.Len())
	}
}

// A turn past its TTL is gone: Take misses so the handler falls back to the
// queued EnqueueBotReply continuation.
func TestExpiredTurnMisses(t *testing.T) {
	_, clock := fakeClock(time.Unix(1000, 0))
	r := NewWithTTL(time.Minute, clock)
	r.Park(parkedTurn("chat", "call", func(context.Context, Decision) error { return nil }))
	clock = func() time.Time { return time.Unix(1000, 0).Add(2 * time.Minute) }
	r.now = clock
	if _, ok := r.Take("chat", "call"); ok {
		t.Fatal("Take must miss for an expired suspension")
	}
	if r.Parked("chat", "call") {
		t.Fatal("Parked() = true for an expired suspension")
	}
}

// Park rejects records that could never be resumed, so the caller keeps the
// queued continuation instead of parking an unwakeable turn.
func TestParkRejectsUnresumableRecords(t *testing.T) {
	r := New()
	if r.Park(nil) {
		t.Fatal("Park(nil) = true")
	}
	if r.Park(&SuspendedTurn{ToolCallID: "call", Resume: func(context.Context, Decision) error { return nil }}) {
		t.Fatal("Park without ChatUUID = true")
	}
	if r.Park(&SuspendedTurn{ChatUUID: "chat", Resume: func(context.Context, Decision) error { return nil }}) {
		t.Fatal("Park without ToolCallID = true")
	}
	if r.Park(&SuspendedTurn{ChatUUID: "chat", ToolCallID: "call"}) {
		t.Fatal("Park without Resume = true")
	}
	if r.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", r.Len())
	}
}

// Chat scoping: the same tool-call id in another chat is a different turn.
func TestRegistryScopesByChat(t *testing.T) {
	r := New()
	r.Park(parkedTurn("chat-a", "call", func(context.Context, Decision) error { return nil }))
	if _, ok := r.Take("chat-b", "call"); ok {
		t.Fatal("Take across chats hit")
	}
	if _, ok := r.Take("chat-a", "call"); !ok {
		t.Fatal("Take in the owning chat missed")
	}
}

// Expire evicts old records and returns them for logging.
func TestExpireDropsAndReturnsOldTurns(t *testing.T) {
	base := time.Unix(1000, 0)
	current := base
	r := NewWithTTL(time.Minute, func() time.Time { return current })
	r.Park(parkedTurn("chat", "old", func(context.Context, Decision) error { return nil }))
	current = base.Add(30 * time.Second)
	r.Park(parkedTurn("chat", "new", func(context.Context, Decision) error { return nil }))
	current = base.Add(2 * time.Minute)
	expired := r.Expire()
	if len(expired) != 2 {
		t.Fatalf("Expire() returned %d turns, want 2", len(expired))
	}
	if r.Len() != 0 {
		t.Fatalf("Len() = %d after Expire, want 0", r.Len())
	}
}

// The sweeper drops expired entries on its ticker and stops cleanly, so the
// ci#811 untracked-writer rule is satisfied without a debt-list entry.
func TestStartSweeperDropsOldEntriesAndStops(t *testing.T) {
	base := time.Unix(1000, 0)
	current := base
	r := NewWithTTL(40*time.Millisecond, func() time.Time { return current })
	r.Park(parkedTurn("chat", "call", func(context.Context, Decision) error { return nil }))
	stop := r.StartSweeper(context.Background())
	current = base.Add(time.Hour)
	deadline := time.Now().Add(2 * time.Second)
	for r.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.Len() != 0 {
		t.Fatal("sweeper did not evict the expired turn")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not drain the sweeper goroutine")
	}
	stop() // idempotent
}

// StartSweeper also stops on ctx cancellation (server teardown).
func TestStartSweeperStopsOnContextCancel(t *testing.T) {
	r := NewWithTTL(40*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stop := r.StartSweeper(ctx)
	cancel()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() blocked after ctx cancel")
	}
}

// The approve path resumes the parked generation (Resume invoked once) and a
// miss reports false so the handler falls back to the queued continuation.
func TestResumeCallsParkedGenerationOnce(t *testing.T) {
	r := New()
	var calls int32
	var got Decision
	turn := parkedTurn("chat", "call", func(_ context.Context, d Decision) error {
		atomic.AddInt32(&calls, 1)
		got = d
		return nil
	})
	if !r.Park(turn) {
		t.Fatal("Park failed")
	}
	decision := Decision{Result: "executed output", Approved: true, ActionID: "call"}
	// handler: Take then Resume
	suspended, ok := r.Take("chat", "call")
	if !ok {
		t.Fatal("approve path Take missed")
	}
	if err := suspended.Resume(context.Background(), decision); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("Resume called %d times, want 1", calls)
	}
	if got.Result != "executed output" || !got.Approved || got.Rejected {
		t.Fatalf("unexpected decision: %+v", got)
	}
	// miss path: no live suspension left, handler must fall back (resumed=false)
	if _, ok := r.Take("chat", "call"); ok {
		t.Fatal("fallback path Take must miss after resume")
	}
}

// A rejection is delivered as a rejection decision into the same turn.
func TestResumeDeliversRejection(t *testing.T) {
	r := New()
	var got Decision
	r.Park(parkedTurn("chat", "call", func(_ context.Context, d Decision) error {
		got = d
		return nil
	}))
	turn, ok := r.Take("chat", "call")
	if !ok {
		t.Fatal("Take missed")
	}
	if err := turn.Resume(context.Background(), Decision{Rejected: true, Reason: "no"}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !got.Rejected || got.Reason != "no" {
		t.Fatalf("unexpected decision: %+v", got)
	}
}

// Forget drops a suspension without delivering a decision.
func TestForget(t *testing.T) {
	r := New()
	r.Park(parkedTurn("chat", "call", func(context.Context, Decision) error { return nil }))
	r.Forget("chat", "call")
	if _, ok := r.Take("chat", "call"); ok {
		t.Fatal("Take hit after Forget")
	}
}
