package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/suspend"
)

// withRegistry swaps the process-wide suspended-turn registry for the duration of
// a test and restores it afterwards.
func withRegistry(t *testing.T, r *suspend.Registry) {
	t.Helper()
	previous := suspendedTurnRegistry
	suspendedTurnRegistry = r
	t.Cleanup(func() { suspendedTurnRegistry = previous })
}

// TestParkSuspendedTurnResumesOnce covers the happy path of ci#1116: a parked
// confirmation is resumed exactly once, and the second decision for the same
// tool call falls through to the queued continuation.
func TestParkSuspendedTurnResumesOnce(t *testing.T) {
	withRegistry(t, suspend.New())

	var got []suspend.Decision
	parked := parkSuspendedTurn("chat-1", "call-1", "msg-1", "bot", "conf-1",
		func(_ context.Context, decision suspend.Decision) error {
			got = append(got, decision)
			return nil
		})
	if !parked {
		t.Fatalf("parkSuspendedTurn() = false, want true")
	}
	if !suspendedTurnParked("chat-1", "call-1") {
		t.Fatalf("parked turn is not visible on the status path")
	}

	resumed, err := resumeParkedTurn(context.Background(), "chat-1", "call-1", suspend.Decision{
		Result:   "tool output",
		Approved: true,
		ActionID: "call-1",
	})
	if err != nil || !resumed {
		t.Fatalf("first resume = (%v, %v), want (true, nil)", resumed, err)
	}
	if len(got) != 1 || got[0].Result != "tool output" || !got[0].Approved {
		t.Fatalf("resume received %+v, want the approved decision with the tool result", got)
	}
	if suspendedTurnParked("chat-1", "call-1") {
		t.Fatalf("turn still reported parked after being resumed")
	}

	resumed, err = resumeParkedTurn(context.Background(), "chat-1", "call-1", suspend.Decision{Approved: true})
	if resumed || err != nil {
		t.Fatalf("second resume = (%v, %v), want (false, nil) so the handler queues a continuation", resumed, err)
	}
	if len(got) != 1 {
		t.Fatalf("resume ran %d times, want exactly 1", len(got))
	}
}

// TestResumeParkedTurnReject delivers a rejection into the same generation and
// reports it as resumed.
func TestResumeParkedTurnReject(t *testing.T) {
	withRegistry(t, suspend.New())

	var got suspend.Decision
	if !parkSuspendedTurn("chat-1", "call-1", "msg-1", "bot", "conf-1",
		func(_ context.Context, decision suspend.Decision) error {
			got = decision
			return nil
		}) {
		t.Fatalf("parkSuspendedTurn() = false, want true")
	}

	resumed, err := resumeParkedTurn(context.Background(), "chat-1", "call-1", suspend.Decision{
		Rejected: true,
		Reason:   "not now",
		ActionID: "call-1",
	})
	if err != nil || !resumed {
		t.Fatalf("resume = (%v, %v), want (true, nil)", resumed, err)
	}
	if !got.Rejected || got.Reason != "not now" {
		t.Fatalf("resume received %+v, want a rejection carrying the reason", got)
	}
}

// TestResumeParkedTurnExpiredFallsBack covers the expiry contract: once the TTL
// has passed the suspension is gone, so resume reports false and the caller keeps
// the queued EnqueueBotReply continuation (resumed:false on the HTTP response).
func TestResumeParkedTurnExpiredFallsBack(t *testing.T) {
	now := time.Unix(1700000000, 0)
	withRegistry(t, suspend.NewWithTTL(time.Minute, func() time.Time { return now }))

	called := 0
	if !parkSuspendedTurn("chat-1", "call-1", "msg-1", "bot", "conf-1",
		func(context.Context, suspend.Decision) error {
			called++
			return nil
		}) {
		t.Fatalf("parkSuspendedTurn() = false, want true")
	}

	now = now.Add(2 * time.Minute)

	resumed, err := resumeParkedTurn(context.Background(), "chat-1", "call-1", suspend.Decision{Approved: true})
	if resumed || err != nil {
		t.Fatalf("resume of an expired turn = (%v, %v), want (false, nil)", resumed, err)
	}
	if called != 0 {
		t.Fatalf("expired resume ran the parked generation %d times, want 0", called)
	}
}

// TestResumeParkedTurnUnknownToolCall documents the miss path for an unknown
// chat/tool_call pair.
func TestResumeParkedTurnUnknownToolCall(t *testing.T) {
	withRegistry(t, suspend.New())

	resumed, err := resumeParkedTurn(context.Background(), "chat-x", "call-x", suspend.Decision{Approved: true})
	if resumed || err != nil {
		t.Fatalf("resume of an unknown turn = (%v, %v), want (false, nil)", resumed, err)
	}
}

// TestResumeParkedTurnDeliverErrorIsReported keeps the error visible: a parked
// generation that already gave up must not be reported as resumed.
func TestResumeParkedTurnDeliverErrorIsReported(t *testing.T) {
	withRegistry(t, suspend.New())

	boom := errors.New("generation gone")
	if !parkSuspendedTurn("chat-1", "call-1", "msg-1", "bot", "conf-1",
		func(context.Context, suspend.Decision) error { return boom }) {
		t.Fatalf("parkSuspendedTurn() = false, want true")
	}

	resumed, err := resumeParkedTurn(context.Background(), "chat-1", "call-1", suspend.Decision{Approved: true})
	if !resumed {
		t.Fatalf("resume = false, want true so the handler reports the delivery attempt")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("resume error = %v, want %v", err, boom)
	}
}

// TestParkSuspendedTurnWithoutResume makes sure a turn without a resume func is
// never parked (the handler then keeps the queued continuation).
func TestParkSuspendedTurnWithoutResume(t *testing.T) {
	withRegistry(t, suspend.New())

	if parkSuspendedTurn("chat-1", "call-1", "msg-1", "bot", "conf-1", nil) {
		t.Fatalf("parkSuspendedTurn(nil) = true, want false")
	}
	if suspendedTurns().Len() != 0 {
		t.Fatalf("registry holds %d turns, want 0", suspendedTurns().Len())
	}
}
