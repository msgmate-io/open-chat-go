package chats

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/database"
)

// TestTurnEndedIncompleteRetryable guards the ci#724 classifier: only a turn
// that stopped without producing a usable final message is retryable. Every
// other outcome either already has its own recovery path, is a deliberate stop,
// or is a transport failure that must not be amplified by a retry loop.
func TestTurnEndedIncompleteRetryable(t *testing.T) {
	// ci#747 (RC2): a turn that ran out of its per-chat tool budget stopped
	// *before* it could report, exactly like a turn with no usable tool call,
	// so it is retried too.
	retryable := []string{
		turnEndedNoUsableToolCall,
		"tool_call_cap_reached",
		"failed_tool_call_cap_reached",
	}
	for _, reason := range retryable {
		if !turnEndedIncompleteRetryable(map[string]interface{}{turnEndedMetadataKey: reason}) {
			t.Errorf("turn_ended=%q must be retryable", reason)
		}
	}

	neverRetry := []string{
		// A transport failure. The provider retry path owns this.
		"provider_error",
		// The user (or an interrupt) stopped the turn deliberately.
		"cancelled",
		// The turn is waiting for the user; the pending-action guard owns it.
		"confirmable_pause",
		// Unrelated terminal states that must never look "incomplete".
		"completed",
		"needs_human",
		"",
		"  ",
	}
	for _, reason := range neverRetry {
		if turnEndedIncompleteRetryable(map[string]interface{}{turnEndedMetadataKey: reason}) {
			t.Errorf("turn_ended=%q must never be retried", reason)
		}
	}

	// A missing key (a backend that does not stamp turn_ended), a non-string
	// value and nil metadata must all be treated as "not retryable": the retry
	// stays opt-in to a signal we actually understand.
	if turnEndedIncompleteRetryable(map[string]interface{}{}) {
		t.Error("a missing turn_ended must not be retryable")
	}
	if turnEndedIncompleteRetryable(map[string]interface{}{turnEndedMetadataKey: 7}) {
		t.Error("a non-string turn_ended must not be retryable")
	}
	if turnEndedIncompleteRetryable(nil) {
		t.Error("nil metadata must not be retryable")
	}
}

// TestDecideAutoContinueRetriesIncompleteTurn is the core ci#724 regression: a
// turn that ended with no usable tool call carries `finished: true`, so the
// loop used to treat it exactly like a clean completion. It must instead
// schedule a retry with the dedicated prompt and advance the retry counter.
func TestDecideAutoContinueRetriesIncompleteTurn(t *testing.T) {
	config := enabledConfig()
	message := finishedMessage(`{"finished":true,"turn_ended":"no_usable_tool_call"}`)

	state := autoContinueState{Turns: 1}
	decision := decideAutoContinue(config, state, message, decodeOrFail(t, message.MetaData), false)
	if !decision.Continue {
		t.Fatalf("an incomplete turn must be retried, got stop reason %q", decision.StopReason)
	}
	if decision.Reason != autoContinueIncompleteRetryReason {
		t.Fatalf("Reason = %q, want %q", decision.Reason, autoContinueIncompleteRetryReason)
	}
	if decision.Prompt != config.IncompletePrompt || decision.Prompt == "" {
		t.Fatalf("an incomplete retry must use the dedicated prompt, got %q", decision.Prompt)
	}
	if decision.Prompt == config.Prompt {
		t.Fatal("the incomplete retry must not reuse the generic continuation prompt")
	}
	if decision.IncompleteRetries != state.IncompleteRetries+1 {
		t.Fatalf("IncompleteRetries = %d, want %d", decision.IncompleteRetries, state.IncompleteRetries+1)
	}
	if decision.Delay <= 0 {
		t.Fatalf("an incomplete retry must carry the configured delay, got %v", decision.Delay)
	}

	// A clean completion at the same turn count still uses the generic
	// continuation and leaves the retry counter alone.
	clean := finishedMessage(`{"finished":true}`)
	cleanDecision := decideAutoContinue(config, autoContinueState{Turns: 2}, clean, decodeOrFail(t, clean.MetaData), false)
	if !cleanDecision.Continue || cleanDecision.Reason != "continuation" {
		t.Fatalf("a clean completion must keep the generic continuation, got %+v", cleanDecision)
	}
	if cleanDecision.Prompt != config.Prompt {
		t.Fatal("a clean completion must use the generic prompt")
	}
}

// TestDecideAutoContinueIncompleteRetryIsBounded guards that the retry has its
// own budget: once the counter reaches the cap the loop stops with a dedicated
// reason instead of retrying forever.
func TestDecideAutoContinueIncompleteRetryIsBounded(t *testing.T) {
	config := enabledConfig()
	config.MaxIncompleteRetries = 3
	message := finishedMessage(`{"finished":true,"turn_ended":"no_usable_tool_call"}`)
	meta := decodeOrFail(t, message.MetaData)

	for retries := 0; retries < config.MaxIncompleteRetries; retries++ {
		decision := decideAutoContinue(config, autoContinueState{Turns: 1, IncompleteRetries: retries}, message, meta, false)
		if !decision.Continue || decision.IncompleteRetries != retries+1 {
			t.Fatalf("retry %d: expected a continuation with counter %d, got %+v", retries, retries+1, decision)
		}
	}

	exhausted := decideAutoContinue(config, autoContinueState{Turns: 1, IncompleteRetries: config.MaxIncompleteRetries}, message, meta, false)
	if exhausted.Continue {
		t.Fatal("the retry must stop once its budget is spent")
	}
	if exhausted.StopReason != autoContinueIncompleteExhaustedReason {
		t.Fatalf("StopReason = %q, want %q", exhausted.StopReason, autoContinueIncompleteExhaustedReason)
	}
	if exhausted.IncompleteRetries != config.MaxIncompleteRetries {
		t.Fatalf("a stopped retry must not advance the counter, got %d", exhausted.IncompleteRetries)
	}
}

// TestDecideAutoContinueIncompleteRetryZeroBudgetDisables guards the opt-out: a
// chat that configures 0 retries keeps the pre-ci#724 behaviour exactly.
func TestDecideAutoContinueIncompleteRetryZeroBudgetDisables(t *testing.T) {
	config := enabledConfig()
	config.MaxIncompleteRetries = 0
	message := finishedMessage(`{"finished":true,"turn_ended":"no_usable_tool_call"}`)

	decision := decideAutoContinue(config, autoContinueState{}, message, decodeOrFail(t, message.MetaData), false)
	if !decision.Continue {
		t.Fatalf("the generic continuation must still run: %+v", decision)
	}
	if decision.Reason != "continuation" || decision.Prompt != config.Prompt {
		t.Fatalf("with the retry disabled the generic continuation must be used, got %+v", decision)
	}
	if decision.IncompleteRetries != 0 {
		t.Fatalf("a disabled retry must not advance the counter, got %d", decision.IncompleteRetries)
	}
}

// TestDecideAutoContinueIncompleteRetryResetsOnCleanCompletion guards that the
// retry counter is consecutive, not cumulative: a single bad turn inside a long
// run must not permanently exhaust the budget.
func TestDecideAutoContinueIncompleteRetryResetsOnCleanCompletion(t *testing.T) {
	config := enabledConfig()
	config.MaxIncompleteRetries = 2
	incomplete := finishedMessage(`{"finished":true,"turn_ended":"no_usable_tool_call"}`)

	decision := decideAutoContinue(config, autoContinueState{IncompleteRetries: 2}, incomplete, decodeOrFail(t, incomplete.MetaData), false)
	if decision.Continue {
		t.Fatal("the budget must be exhausted before the reset is exercised")
	}

	clean := finishedMessage(`{"finished":true}`)
	reset := decideAutoContinue(config, autoContinueState{IncompleteRetries: 2}, clean, decodeOrFail(t, clean.MetaData), false)
	if !reset.Continue {
		t.Fatalf("a clean completion must continue the loop: %+v", reset)
	}
	if reset.IncompleteRetries != 0 {
		t.Fatalf("a clean completion must reset the retry counter, got %d", reset.IncompleteRetries)
	}
}

// TestDecideAutoContinueNeverRetriesOtherTurnOutcomes is the negative matrix: an
// incomplete-retry decision must never be taken for an outcome the retry does
// not own, and the retry counter must stay untouched for all of them.
func TestDecideAutoContinueNeverRetriesOtherTurnOutcomes(t *testing.T) {
	config := enabledConfig()
	// ci#747 (RC2): the cap-exhausted outcomes are deliberately absent from
	// this list - they are retryable now (see TestTurnEndedIncompleteRetryable).
	for _, reason := range []string{
		"cancelled",
		"provider_error",
		"confirmable_pause",
	} {
		message := finishedMessage(`{"finished":true,"turn_ended":"` + reason + `"}`)
		decision := decideAutoContinue(config, autoContinueState{Turns: 1}, message, decodeOrFail(t, message.MetaData), false)
		if decision.Reason == autoContinueIncompleteRetryReason {
			t.Errorf("turn_ended=%q must not take the incomplete-retry path", reason)
		}
		if decision.IncompleteRetries != 0 {
			t.Errorf("turn_ended=%q must not advance the retry counter, got %d", reason, decision.IncompleteRetries)
		}
		if decision.Prompt == config.IncompletePrompt {
			t.Errorf("turn_ended=%q must not receive the incomplete prompt", reason)
		}
	}
}

// TestDecideAutoContinueIncompleteTurnRespectsStops guards the precedence rule:
// an agent that explicitly declared completion (or requested a human) has
// finished, so the incomplete marker must not re-run it.
func TestDecideAutoContinueIncompleteTurnRespectsStops(t *testing.T) {
	config := enabledConfig()
	incomplete := `,"turn_ended":"no_usable_tool_call"`

	completed := finishedMessage(`{"finished":true,"task_completed":true` + incomplete + `}`)
	decision := decideAutoContinue(config, autoContinueState{Turns: 1}, completed, decodeOrFail(t, completed.MetaData), false)
	if decision.Continue {
		t.Fatalf("an explicitly completed task must not be retried: %+v", decision)
	}

	marker := textMessage("all done "+DefaultAutoContinueCompletionMarker, `{"finished":true`+incomplete+`}`)
	markerDecision := decideAutoContinue(config, autoContinueState{Turns: 1}, marker, decodeOrFail(t, marker.MetaData), false)
	if markerDecision.Continue {
		t.Fatalf("a completion marker must not be overridden by the incomplete signal: %+v", markerDecision)
	}

	stopped := autoContinueState{Turns: 1, Stopped: true, StopReason: "interrupted_by_user"}
	stoppedDecision := decideAutoContinue(config, stopped, finishedMessage(`{"finished":true`+incomplete+`}`),
		decodeOrFail(t, finishedMessage(`{"finished":true`+incomplete+`}`).MetaData), false)
	if stoppedDecision.Continue {
		t.Fatal("a deliberately stopped loop must not be revived by the retry")
	}
}

// TestDecideAutoContinueIncompleteTurnStillRefusesPendingAction guards that the
// retry cannot bypass the pending-action guard: a turn that paused on a user
// action still waits for the user, not for a retry.
func TestDecideAutoContinueIncompleteTurnStillRefusesPendingAction(t *testing.T) {
	config := enabledConfig()
	message := finishedMessage(`{"finished":true,"turn_ended":"no_usable_tool_call","confirmable_actions":[{"action_id":"a1","status":"pending"}]}`)

	decision := decideAutoContinue(config, autoContinueState{Turns: 1}, message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue {
		t.Fatalf("a pending action must still block the continuation: %+v", decision)
	}
	if decision.StopReason != "pending_action" {
		t.Fatalf("StopReason = %q, want pending_action", decision.StopReason)
	}
}

// TestAutoContinueConfigCarriesIncompleteRetryPolicy guards the configuration
// surface: the default budget, the chat-level override (including 0, which
// disables the retry), the dispatch-level override and the clamp.
func TestAutoContinueConfigCarriesIncompleteRetryPolicy(t *testing.T) {
	byDefault := ResolveAutoContinueConfig(map[string]interface{}{"auto_continue": true})
	if byDefault.MaxIncompleteRetries != defaultAutoContinueMaxIncompleteRetries {
		t.Fatalf("default MaxIncompleteRetries = %d, want %d", byDefault.MaxIncompleteRetries, defaultAutoContinueMaxIncompleteRetries)
	}
	if byDefault.IncompletePrompt == "" || byDefault.IncompletePrompt == byDefault.Prompt {
		t.Fatal("the resolved config must carry a dedicated incomplete prompt")
	}

	configured := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":                     true,
		autoContinueMaxIncompleteRetriesKey: 5,
		autoContinueIncompletePromptKey:     "finish and report",
	})
	if configured.MaxIncompleteRetries != 5 {
		t.Fatalf("MaxIncompleteRetries = %d, want the configured 5", configured.MaxIncompleteRetries)
	}
	if configured.IncompletePrompt != "finish and report" {
		t.Fatalf("IncompletePrompt = %q, want the configured value", configured.IncompletePrompt)
	}

	disabled := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":                     true,
		autoContinueMaxIncompleteRetriesKey: 0,
	})
	if disabled.MaxIncompleteRetries != 0 {
		t.Fatalf("an explicit 0 must disable the retry, got %d", disabled.MaxIncompleteRetries)
	}

	clamped := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":                     true,
		autoContinueMaxIncompleteRetriesKey: 1000,
	})
	if clamped.MaxIncompleteRetries != maxAutoContinueIncompleteRetries {
		t.Fatalf("MaxIncompleteRetries = %d, want it clamped to %d", clamped.MaxIncompleteRetries, maxAutoContinueIncompleteRetries)
	}

	negative := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":                     true,
		autoContinueMaxIncompleteRetriesKey: -3,
	})
	if negative.MaxIncompleteRetries != 0 {
		t.Fatalf("a negative budget must disable the retry, got %d", negative.MaxIncompleteRetries)
	}
}

// TestAutoContinueIncompleteRetriesPersist guards that the consecutive retry
// counter survives a restart (it is stored in the chat settings), and that a
// reset is written through rather than left stale.
func TestAutoContinueIncompleteRetriesPersist(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "incomplete-retries@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "incomplete-retries-bot@example.com")
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 1, IncompleteRetries: 2}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if state := loadAutoContinueState(DB, chat.ID); state.IncompleteRetries != 2 {
		t.Fatalf("IncompleteRetries = %d, want it to survive the round trip", state.IncompleteRetries)
	}

	// A clean completion writes the reset through, so a restart cannot resume
	// with a stale, already-spent budget.
	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 2}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if state := loadAutoContinueState(DB, chat.ID); state.IncompleteRetries != 0 {
		t.Fatalf("IncompleteRetries = %d, want the reset to persist", state.IncompleteRetries)
	}

	// The raw settings must not keep a stale key either.
	settings := database.ChatSettings{}
	if err := DB.Where("chat_id = ?", chat.ID).First(&settings).Error; err != nil {
		t.Fatalf("load settings: %v", err)
	}
	stored := map[string]interface{}{}
	if err := json.Unmarshal(settings.ConfigData, &stored); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if _, present := stored[autoContinueIncompleteRetriesStateKey]; present {
		t.Fatalf("a reset must not leave a stale %s key in the settings", autoContinueIncompleteRetriesStateKey)
	}
}

// TestAutoContinueStatusReportsIncompleteRetries guards the agent/status-facing
// projection: a watcher must be able to tell a live incomplete-turn retry from
// a genuinely finished run.
func TestAutoContinueStatusReportsIncompleteRetries(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "incomplete-status@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "incomplete-status-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true,"auto_continue_max":9,"auto_continue_max_incomplete_retries":4}`)}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	chat := database.Chat{
		User1Id:        owner.ID,
		User2Id:        bot.ID,
		ChatType:       "interaction",
		SharedConfigId: &shared.ID,
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	chat.SharedConfig = &shared

	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 3, IncompleteRetries: 2}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	status := AutoContinueStatusForChat(DB, chat)
	if status.IncompleteRetries != 2 || status.MaxIncompleteRetries != 4 {
		t.Fatalf("unexpected incomplete-retry status: %+v", status)
	}

	response := populateAutoContinueStatus(DB, chat, InteractionStatusResponse{ChatUUID: chat.UUID})
	if response.AutoContinueIncompleteRetries != 2 || response.AutoContinueMaxIncompleteRetries != 4 {
		t.Fatalf("unexpected incomplete-retry response: %+v", response)
	}
}

// TestDecideAutoContinueRetriesCapExhaustedTurn is the ci#747 (RC2) regression:
// a turn aborted by the per-chat tool budget (or its failed-call budget) used to
// end the loop as a failure. It must instead schedule a bounded retry with the
// partial-report instruction, and it must stop with an explicit reason once that
// bounded budget is used up - never park the interaction silently.
func TestDecideAutoContinueRetriesCapExhaustedTurn(t *testing.T) {
	config := enabledConfig()
	if config.MaxIncompleteRetries <= 0 {
		t.Fatalf("the default config must allow at least one cap-recovery retry, got %d", config.MaxIncompleteRetries)
	}

	for _, reason := range []string{"tool_call_cap_reached", "failed_tool_call_cap_reached"} {
		message := finishedMessage(`{"finished":true,"turn_ended":"` + reason + `"}`)
		meta := decodeOrFail(t, message.MetaData)

		state := autoContinueState{Turns: 1}
		decision := decideAutoContinue(config, state, message, meta, false)
		if !decision.Continue {
			t.Fatalf("turn_ended=%q must be recovered, got stop reason %q", reason, decision.StopReason)
		}
		if decision.Reason != autoContinueIncompleteRetryReason {
			t.Fatalf("turn_ended=%q Reason = %q, want %q", reason, decision.Reason, autoContinueIncompleteRetryReason)
		}
		if decision.Prompt != config.IncompletePrompt || decision.Prompt == "" {
			t.Fatalf("turn_ended=%q must use the partial-report retry prompt", reason)
		}
		if decision.Prompt == config.Prompt {
			t.Fatalf("turn_ended=%q must not reuse the generic continuation prompt", reason)
		}
		if decision.IncompleteRetries != state.IncompleteRetries+1 {
			t.Fatalf("turn_ended=%q IncompleteRetries = %d, want %d", reason, decision.IncompleteRetries, state.IncompleteRetries+1)
		}
		if decision.Delay <= 0 {
			t.Fatalf("turn_ended=%q must carry the configured delay, got %v", reason, decision.Delay)
		}

		// The bounded budget still bites: once spent, the loop stops with an
		// explicit stop reason rather than continuing forever.
		exhausted := decideAutoContinue(config, autoContinueState{Turns: 1, IncompleteRetries: config.MaxIncompleteRetries}, message, meta, false)
		if exhausted.Continue {
			t.Fatalf("turn_ended=%q must stop once the bounded retry budget is spent", reason)
		}
		if exhausted.StopReason != autoContinueIncompleteExhaustedReason {
			t.Fatalf("turn_ended=%q stop reason = %q, want %q", reason, exhausted.StopReason, autoContinueIncompleteExhaustedReason)
		}
	}

	// A clean completion right after a cap-retry returns to the generic
	// continuation and clears the retry debt.
	clean := finishedMessage(`{"finished":true}`)
	cleanDecision := decideAutoContinue(config, autoContinueState{Turns: 2, IncompleteRetries: 1}, clean, decodeOrFail(t, clean.MetaData), false)
	if !cleanDecision.Continue || cleanDecision.Reason != "continuation" || cleanDecision.IncompleteRetries != 0 {
		t.Fatalf("a clean completion must reset the cap-recovery debt, got %+v", cleanDecision)
	}
}

// TestIncompletePromptDemandsPartialReportOnInfraFailure pins the ci#747 (RC2)
// wording requirement: a retry after failing tool calls must ask for a partial
// summary plus the blocker instead of another blind retry.
func TestIncompletePromptDemandsPartialReportOnInfraFailure(t *testing.T) {
	config := enabledConfig()
	prompt := config.IncompletePrompt
	for _, want := range []string{"blocker", "tool calls"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the incomplete/partial-report prompt must mention %q, got %q", want, prompt)
		}
	}
}
