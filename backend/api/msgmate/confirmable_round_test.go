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
	// ci#1178: the in-turn park is opt-in, so this ci#1147 regression test has
	// to enable it explicitly to exercise the parking code path at all.
	t.Setenv(suspend.InTurnParkEnvKey, "1")
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
			ctx, registry, chatUUID, appended, toolCallDetails, currentMessages, ToolResultLimits{},
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
	// ci#1178: opt in to the park so the undecided/expiry fallback is exercised.
	t.Setenv(suspend.InTurnParkEnvKey, "1")
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
		ctx, registry, chatUUID, appended, nil, nil, ToolResultLimits{},
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

// TestInTurnParkDisabledByDefaultEndsTurn is the ci#1178 regression: with
// OCI_CONFIRMABLE_IN_TURN_PARK unset, a round that surfaced a pending confirmable
// call must NOT park the generation at all.
//
// Parking here blocks the turn on a decision that can only be produced from the
// pending confirmable_actions row the handler persists once this generation has
// RETURNED, so a live park cannot be approved: it burns the whole 10m suspension
// TTL (invisible below Debug), is dropped on expiry, and the later approval falls
// back to continuation=continuation_queued - which then parks again on its next
// confirmable call. With the flag off the round returns undecided immediately, so
// the caller records TurnEndedConfirmablePause and the approval starts an
// ordinary ~2s follow-up turn.
func TestInTurnParkDisabledByDefaultEndsTurn(t *testing.T) {
	t.Setenv(suspend.InTurnParkEnvKey, "")
	const chatUUID = "ci1178-default-off-chat"
	registry := suspend.NewWithTTL(time.Minute, nil)
	registry.Forget(chatUUID, "call_a")

	appended := []executedToolCall{
		{id: "call_a", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	out, _, decided := parkAndAwaitConfirmationRound(
		ctx, registry, chatUUID, appended, nil, nil, ToolResultLimits{},
	)
	elapsed := time.Since(started)

	if elapsed > time.Second {
		t.Fatalf("round took %s with the in-turn park disabled, want an immediate return (ci#1178: no park may block the generation)", elapsed)
	}
	if decided {
		t.Fatal("round reported decided with the in-turn park disabled, want the pre-ci#1116 pause behaviour")
	}
	if registry.Parked(chatUUID, "call_a") {
		t.Fatal("the call was parked with the in-turn park disabled: the park can never be decided, so it only burns the suspension TTL (ci#1178)")
	}
	if registry.Len() != 0 {
		t.Fatalf("registry holds %d suspension(s) after a disabled-park round, want 0", registry.Len())
	}
	for _, call := range out {
		if call.status != ToolCallStatusPendingConfirmation {
			t.Fatalf("call %s status = %q, want it left pending for the confirmation handler", call.id, call.status)
		}
	}
}

// TestParkExpiryForgetsKeySoLateDecisionCannotResume is the ci#1178 TTL-expiry
// regression with the flag ON: a park whose decision never arrives must expire,
// drop the key, and leave a late decision unable to resume the finished
// generation (the handler then logs continuation=continuation_queued). Since
// ci#1177 the park waits suspend.ParkWaitBudget(ttl) rather than the whole TTL,
// so the wait is bounded below the registry TTL and the durable suspension still
// outlives the park.
func TestParkExpiryForgetsKeySoLateDecisionCannotResume(t *testing.T) {
	t.Setenv(suspend.InTurnParkEnvKey, "1")
	const chatUUID = "ci1178-ttl-expiry-chat"
	registry := suspend.NewWithTTL(50*time.Millisecond, nil)
	registry.Forget(chatUUID, "call_a")

	appended := []executedToolCall{
		{id: "call_a", name: "coder_bash", status: ToolCallStatusPendingConfirmation},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	_, _, decided := parkAndAwaitConfirmationRound(
		ctx, registry, chatUUID, appended, nil, nil, ToolResultLimits{},
	)
	elapsed := time.Since(started)

	if decided {
		t.Fatal("an expired park was reported as decided; the turn must keep the pause behaviour")
	}
	// ci#1177: the in-turn park no longer waits for the whole registry TTL; it
	// waits for suspend.ParkWaitBudget(ttl), which stays strictly below the TTL
	// so the durable suspension outlives the park. Pin that bound instead of the
	// old full-TTL wait this regression test used to assert, allowing a
	// scheduler-tick tolerance below the budget.
	parkBudget := suspend.ParkWaitBudget(registry.TTL())
	if parkBudget >= registry.TTL() {
		t.Fatalf("park budget %s must stay below the registry TTL %s (ci#1177)", parkBudget, registry.TTL())
	}
	if tolerance := 5 * time.Millisecond; elapsed+tolerance < parkBudget {
		t.Fatalf("round returned after %s, want it to wait for the %s park budget (registry TTL %s)", elapsed, parkBudget, registry.TTL())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("round returned after %s, want it bounded by the registry TTL, not the turn context", elapsed)
	}
	if registry.Parked(chatUUID, "call_a") {
		t.Fatal("the expired suspension is still registered; a late click would deliver into a finished generation")
	}
	resumed, err := registry.ResumeWith(ctx, chatUUID, "call_a", suspend.Decision{
		Result:   "late approval",
		Approved: true,
		ActionID: "call_a",
	})
	if err != nil {
		t.Fatalf("ResumeWith on an expired park returned an error: %v", err)
	}
	if resumed {
		t.Fatal("a late decision resumed an expired park, want resumed=false so the handler queues a continuation instead (ci#1178)")
	}
}
