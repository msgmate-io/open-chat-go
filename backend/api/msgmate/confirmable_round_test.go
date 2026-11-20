package msgmate

import (
	"context"
	"testing"
	"time"

	"backend/suspend"
)

// TestPendingConfirmationCallsReturnsEveryPendingCall pins the ci#1147 helper:
// a single model round can surface several confirmable calls and every one of
// them has to be reported, not just the first. Returning only the first left
// the sibling calls on their pending placeholder forever.
func TestPendingConfirmationCallsReturnsEveryPendingCall(t *testing.T) {
	calls := []executedToolCall{
		{id: "call_done", status: ToolCallStatusSucceeded},
		{id: "call_a", status: ToolCallStatusPendingConfirmation},
		{id: "call_failed", status: ToolCallStatusFailed},
		{id: "call_b", status: ToolCallStatusPendingConfirmation},
	}

	pending := pendingConfirmationCalls(calls)
	if len(pending) != 2 {
		t.Fatalf("pendingConfirmationCalls returned %d calls, want 2 (ci#1147: every confirmable call of the round must be parked)", len(pending))
	}
	if pending[0].id != "call_a" || pending[1].id != "call_b" {
		t.Fatalf("pendingConfirmationCalls returned %q,%q, want call_a,call_b in emission order", pending[0].id, pending[1].id)
	}

	if got := pendingConfirmationCalls(nil); len(got) != 0 {
		t.Fatalf("pendingConfirmationCalls(nil) = %d calls, want 0", len(got))
	}
}

// TestParkAndAwaitConfirmationRoundParksEveryPendingCall is the ci#1147
// regression: a round with two pending confirmable calls must park BOTH of them
// and continue the same turn once the user decided them. The old code parked
// only the first call and fell through to the terminal pause with no follow-up
// assistant message, even though the user had approved the calls.
//
// Only the second call is approved first; the round must not resume on the
// first park it finds, and the second decision must reach the same generation.
func TestParkAndAwaitConfirmationRoundParksEveryPendingCall(t *testing.T) {
	const chatUUID = "ci1147-round-chat"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Clear any stale suspension from a previous run of this test file.
	registry := suspend.Default()
	for _, id := range []string{"call_a", "call_b"} {
		registry.Forget(chatUUID, id)
	}

	appended := []executedToolCall{
		{id: "call_a", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
		{id: "call_b", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
	}
	toolCallDetails := []map[string]interface{}{
		{"id": "call_a", "status": ToolCallStatusPendingConfirmation},
		{"id": "call_b", "status": ToolCallStatusPendingConfirmation},
	}
	currentMessages := []map[string]interface{}{
		{"role": "user", "content": "do both"},
		{"role": "tool", "tool_call_id": "call_a", "content": "waiting for confirmation"},
		{"role": "tool", "tool_call_id": "call_b", "content": "waiting for confirmation"},
	}

	type roundResult struct {
		appended  []executedToolCall
		messages  []map[string]interface{}
		decided   bool
		panicked  interface{}
		completed bool
	}
	done := make(chan roundResult, 1)
	go func() {
		res := roundResult{}
		defer func() {
			res.panicked = recover()
			done <- res
		}()
		appendedOut, messagesOut, decided := parkAndAwaitConfirmationRound(
			ctx, chatUUID, appended, toolCallDetails, currentMessages, ToolResultLimits{},
		)
		res.appended = appendedOut
		res.messages = messagesOut
		res.decided = decided
		res.completed = true
	}()

	// Decide both parks, in parking order, as the confirmation handler would.
	for i, id := range []string{"call_a", "call_b"} {
		if !waitForPark(t, registry, chatUUID, id) {
			t.Fatalf("call %s (#%d) was never parked: only the first pending call of the round is parked (ci#1147)", id, i+1)
		}
		approved, err := registry.ResumeWith(ctx, chatUUID, id, suspend.Decision{
			Result:   "executed " + id,
			Approved: true,
			ActionID: id,
		})
		if err != nil || !approved {
			t.Fatalf("resuming park %s failed (resumed=%v): %v", id, approved, err)
		}
	}

	res := <-done
	if res.panicked != nil {
		t.Fatalf("parkAndAwaitConfirmationRound panicked: %v", res.panicked)
	}
	if !res.completed {
		t.Fatal("parkAndAwaitConfirmationRound did not complete")
	}
	if !res.decided {
		t.Fatal("the round was not reported as decided; the turn would fall through to the terminal pause with no follow-up assistant message (ci#1147)")
	}
	for _, call := range res.appended {
		if call.status != ToolCallStatusSucceeded {
			t.Fatalf("call %s kept status %q, want %q (every resumed call must be patched back)", call.id, call.status, ToolCallStatusSucceeded)
		}
		if call.result != "executed "+call.id {
			t.Fatalf("call %s result = %q, want the decision result", call.id, call.result)
		}
	}
	for _, details := range toolCallDetails {
		id, _ := details["id"].(string)
		if details["status"] != ToolCallStatusSucceeded {
			t.Fatalf("tool_call_details[%s] status = %v, want %q", id, details["status"], ToolCallStatusSucceeded)
		}
	}
	for _, message := range res.messages {
		if role, _ := message["role"].(string); role != "tool" {
			continue
		}
		id, _ := message["tool_call_id"].(string)
		if message["content"] != "executed "+id {
			t.Fatalf("transcript tool message %s content = %v, want the decision result", id, message["content"])
		}
	}
}

// TestParkAndAwaitConfirmationRoundUndecidedKeepsPause pins the fallback: when
// a park expires or the turn is cancelled, the round is NOT decided, so the
// caller keeps the pre-ci#1116 terminal pause behaviour instead of re-parking a
// still pending sibling.
func TestParkAndAwaitConfirmationRoundUndecidedKeepsPause(t *testing.T) {
	const chatUUID = "ci1147-undecided-chat"
	registry := suspend.Default()
	registry.Forget(chatUUID, "call_a")

	appended := []executedToolCall{
		{id: "call_a", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
		{id: "call_b", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	out, _, decided := parkAndAwaitConfirmationRound(
		ctx, chatUUID, appended, nil, nil, ToolResultLimits{},
	)
	if decided {
		t.Fatal("an undecided round was reported as decided; the turn must keep the pause behaviour")
	}
	for _, call := range out {
		if call.status != ToolCallStatusPendingConfirmation {
			t.Fatalf("call %s status = %q, want it left pending", call.id, call.status)
		}
	}
}

func waitForPark(t *testing.T, registry *suspend.Registry, chatUUID, toolCallID string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if registry.Parked(chatUUID, toolCallID) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}
