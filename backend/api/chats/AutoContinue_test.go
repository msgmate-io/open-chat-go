package chats

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"backend/database"
	"backend/workqueue"

	"github.com/hibiken/asynq"
)

// stubAutoContinueEnqueue replaces the enqueue indirection for one test and
// returns a pointer to the captured payloads.
func stubAutoContinueEnqueue(t *testing.T) *[]workqueue.BotReplyPayload {
	t.Helper()
	captured := []workqueue.BotReplyPayload{}
	previous := autoContinueEnqueue
	autoContinueEnqueue = func(_ *asynq.Client, _ *asynq.Inspector, payload workqueue.BotReplyPayload, _ ...asynq.Option) (*asynq.TaskInfo, error) {
		captured = append(captured, payload)
		return nil, nil
	}
	t.Cleanup(func() { autoContinueEnqueue = previous })
	return &captured
}

func TestResolveAutoContinueConfigDefaults(t *testing.T) {
	config := ResolveAutoContinueConfig(nil)
	if config.Enabled {
		t.Fatal("auto-continuation must be opt-in")
	}
	if config.MaxTurns != defaultAutoContinueMax {
		t.Fatalf("MaxTurns = %d, want %d", config.MaxTurns, defaultAutoContinueMax)
	}
	if config.Delay != defaultAutoContinueDelaySeconds*time.Second {
		t.Fatalf("Delay = %v, want %ds", config.Delay, defaultAutoContinueDelaySeconds)
	}
	if config.CompletionMarker != DefaultAutoContinueCompletionMarker || config.HumanMarker != DefaultAutoContinueHumanMarker {
		t.Fatalf("unexpected markers: %q / %q", config.CompletionMarker, config.HumanMarker)
	}
	if !strings.Contains(config.Prompt, DefaultAutoContinueCompletionMarker) {
		t.Fatalf("default prompt must name the completion marker: %q", config.Prompt)
	}
}

func TestResolveAutoContinueConfigExplicitKeys(t *testing.T) {
	config := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":                   true,
		"auto_continue_max":               float64(5),
		"auto_continue_delay_seconds":     "30",
		"auto_continue_prompt":            "keep going",
		"auto_continue_completion_marker": "DONE",
		"auto_continue_human_marker":      "HELP",
	})
	if !config.Enabled {
		t.Fatal("expected auto-continuation enabled")
	}
	if config.MaxTurns != 5 {
		t.Fatalf("MaxTurns = %d, want 5", config.MaxTurns)
	}
	if config.Delay != 30*time.Second {
		t.Fatalf("Delay = %v, want 30s", config.Delay)
	}
	if config.Prompt != "keep going" {
		t.Fatalf("Prompt = %q", config.Prompt)
	}
	if config.CompletionMarker != "DONE" || config.HumanMarker != "HELP" {
		t.Fatalf("markers = %q / %q", config.CompletionMarker, config.HumanMarker)
	}
}

// TestResolveAutoContinueConfigReadsAgentDispatchFallback guards that an
// in-app agent dispatch (POST /api/v1/agents/dispatch) enables the loop without
// any external watcher, using the dispatch markers.
func TestResolveAutoContinueConfigReadsAgentDispatchFallback(t *testing.T) {
	config := ResolveAutoContinueConfig(map[string]interface{}{
		"agent_dispatch": map[string]interface{}{
			"auto_continue":     true,
			"completion_marker": "<<FINISHED>>",
			"human_marker":      "<<ASK>>",
		},
	})
	if !config.Enabled {
		t.Fatal("a dispatch with auto_continue must enable the loop")
	}
	if config.CompletionMarker != "<<FINISHED>>" || config.HumanMarker != "<<ASK>>" {
		t.Fatalf("dispatch markers not honoured: %q / %q", config.CompletionMarker, config.HumanMarker)
	}
}

func TestResolveAutoContinueConfigClampsDelay(t *testing.T) {
	config := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":               true,
		"auto_continue_delay_seconds": 999999,
	})
	if config.Delay != maxAutoContinueDelaySeconds*time.Second {
		t.Fatalf("Delay = %v, want the %ds cap", config.Delay, maxAutoContinueDelaySeconds)
	}
	negative := ResolveAutoContinueConfig(map[string]interface{}{
		"auto_continue":               true,
		"auto_continue_delay_seconds": -5,
	})
	if negative.Delay != 0 {
		t.Fatalf("negative delay must clamp to zero, got %v", negative.Delay)
	}
}

func TestIsStopMarker(t *testing.T) {
	config := ResolveAutoContinueConfig(nil)
	if reason, stopped := config.IsStopMarker("all done\n<<TASK_COMPLETE>>"); !stopped || reason != "completed" {
		t.Fatalf("expected completed stop, got (%q, %v)", reason, stopped)
	}
	if reason, stopped := config.IsStopMarker("blocked\n<<NEED_HUMAN>>\nmigration decision"); !stopped || reason != "needs_human" {
		t.Fatalf("expected needs_human stop, got (%q, %v)", reason, stopped)
	}
	if _, stopped := config.IsStopMarker("still working"); stopped {
		t.Fatal("a normal reply must not stop the loop")
	}
}

func finishedMessage(meta string) database.Message {
	text := ""
	return database.Message{
		DataType: "text",
		Text:     &text,
		MetaData: database.JSONRaw(meta),
	}
}

func textMessage(text string, meta string) database.Message {
	message := finishedMessage(meta)
	message.Text = &text
	return message
}

func enabledConfig() AutoContinueConfig {
	return ResolveAutoContinueConfig(map[string]interface{}{"auto_continue": true})
}

func TestDecideAutoContinueSchedulesCleanCompletion(t *testing.T) {
	message := finishedMessage(`{"finished":true}`)
	decision := decideAutoContinue(enabledConfig(), autoContinueState{}, message, decodeOrFail(t, message.MetaData), false)
	if !decision.Continue {
		t.Fatalf("expected a continuation, got stop reason %q", decision.StopReason)
	}
	if decision.Prompt == "" || decision.Delay == 0 {
		t.Fatalf("expected prompt and delay, got %+v", decision)
	}
}

func TestDecideAutoContinueRefusesFailedOrPendingTurns(t *testing.T) {
	failed := finishedMessage(`{"finished":true,"error":true}`)
	if decision := decideAutoContinue(enabledConfig(), autoContinueState{}, failed, decodeOrFail(t, failed.MetaData), false); decision.Continue {
		t.Fatal("a failed turn must not auto-continue")
	}

	pending := finishedMessage(`{"finished":true,"confirmable_actions":[{"action_id":"a1","status":"pending"}]}`)
	decision := decideAutoContinue(enabledConfig(), autoContinueState{}, pending, decodeOrFail(t, pending.MetaData), false)
	if decision.Continue {
		t.Fatal("a turn that waits on the user must not auto-continue")
	}
	if decision.StopReason != "pending_action" {
		t.Fatalf("StopReason = %q, want pending_action", decision.StopReason)
	}

	unfinished := finishedMessage(`{}`)
	if decision := decideAutoContinue(enabledConfig(), autoContinueState{}, unfinished, decodeOrFail(t, unfinished.MetaData), false); decision.Continue {
		t.Fatal("an unfinished turn must not auto-continue")
	}
}

func TestDecideAutoContinueStopsOnMarkers(t *testing.T) {
	message := textMessage("done\n<<TASK_COMPLETE>>", `{"finished":true}`)
	decision := decideAutoContinue(enabledConfig(), autoContinueState{}, message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue {
		t.Fatal("a completion marker must stop the loop")
	}
	if decision.StopReason != "completed" {
		t.Fatalf("StopReason = %q, want completed", decision.StopReason)
	}

	human := textMessage("need a decision\n<<NEED_HUMAN>>", `{"finished":true}`)
	decision = decideAutoContinue(enabledConfig(), autoContinueState{}, human, decodeOrFail(t, human.MetaData), false)
	if decision.Continue || decision.StopReason != "needs_human" {
		t.Fatalf("expected needs_human stop, got %+v", decision)
	}
}

func TestDecideAutoContinueRespectsBudgetAndActiveTurn(t *testing.T) {
	message := finishedMessage(`{"finished":true}`)
	config := enabledConfig()
	config.MaxTurns = 2

	exhausted := decideAutoContinue(config, autoContinueState{Turns: 2}, message, decodeOrFail(t, message.MetaData), false)
	if exhausted.Continue || exhausted.StopReason != autoContinueExhaustedReason {
		t.Fatalf("expected budget exhaustion, got %+v", exhausted)
	}

	active := decideAutoContinue(config, autoContinueState{Turns: 1}, message, decodeOrFail(t, message.MetaData), true)
	if active.Continue {
		t.Fatal("a live turn must never be overlapped")
	}

	stopped := decideAutoContinue(config, autoContinueState{Turns: 1, Stopped: true, StopReason: "interrupted_by_user"}, message, decodeOrFail(t, message.MetaData), false)
	if stopped.Continue || stopped.StopReason != "interrupted_by_user" {
		t.Fatalf("a stopped loop must stay stopped, got %+v", stopped)
	}
}

func TestDecideAutoContinueDisabledProducesNoDecision(t *testing.T) {
	message := finishedMessage(`{"finished":true}`)
	decision := decideAutoContinue(AutoContinueConfig{}, autoContinueState{}, message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue || decision.StopReason != "" {
		t.Fatalf("a disabled loop must be a no-op, got %+v", decision)
	}
}

// TestDecideAutoContinueStopsOnTaskCompletedFlag guards the machine-readable
// completion sentinel (ci#537 hardening): a reply that sets `task_completed`
// stops the loop exactly like the text marker, and it takes precedence over a
// still-remaining budget.
func TestDecideAutoContinueStopsOnTaskCompletedFlag(t *testing.T) {
	message := finishedMessage(`{"finished":true,"task_completed":true}`)
	decision := decideAutoContinue(enabledConfig(), autoContinueState{Turns: 1}, message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue {
		t.Fatal("a task_completed flag must stop the loop")
	}
	if decision.StopReason != "completed" {
		t.Fatalf("StopReason = %q, want completed", decision.StopReason)
	}

	// A reply that merely mentions the flag in prose (string, not bool) must
	// not stop the loop.
	notFlag := finishedMessage(`{"finished":true,"task_completed":"no"}`)
	if decision := decideAutoContinue(enabledConfig(), autoContinueState{}, notFlag, decodeOrFail(t, notFlag.MetaData), false); !decision.Continue {
		t.Fatalf("a non-boolean task_completed must not stop the loop, got %+v", decision)
	}
}

// TestDecideAutoContinueRefusesOverlappingContinuation guards the durable
// no-overlap rule: a continuation already scheduled for this exact bot turn
// must not be doubled up, while a fresh bot turn clears the outstanding marker.
func TestDecideAutoContinueRefusesOverlappingContinuation(t *testing.T) {
	message := finishedMessage(`{"finished":true}`)
	message.ID = 42
	outstanding := autoContinueState{Turns: 1, Pending: true, PendingSourceMessageID: 42}

	decision := decideAutoContinue(enabledConfig(), outstanding, message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue {
		t.Fatal("an outstanding continuation must not be scheduled twice")
	}
	if decision.StopReason != continuationInFlightReason {
		t.Fatalf("StopReason = %q, want %q", decision.StopReason, continuationInFlightReason)
	}

	// A newer bot message proves the outstanding continuation ran, so the loop
	// resumes.
	fresh := finishedMessage(`{"finished":true}`)
	fresh.ID = 43
	if decision := decideAutoContinue(enabledConfig(), outstanding, fresh, decodeOrFail(t, fresh.MetaData), false); !decision.Continue {
		t.Fatalf("a new bot turn must clear the outstanding marker, got %+v", decision)
	}
}

// TestMaybeAutoContinueInteractionIsIdempotentPerTurn guards that the loop
// advances exactly one turn per completed bot message: re-evaluating the same
// completion (a duplicate/retried event) is a no-op and never a second prompt.
func TestMaybeAutoContinueInteractionIsIdempotentPerTurn(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-idem@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-idem-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true,"auto_continue_delay_seconds":0}`)}
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

	message := textMessage("working on it", `{"finished":true}`)
	message.ChatId = chat.ID
	message.SenderId = bot.ID
	message.ReceiverId = owner.ID
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message); !forwarded {
		t.Fatal("expected the first evaluation to continue the loop")
	}
	if len(*captured) != 1 {
		t.Fatalf("expected exactly one scheduled bot reply after the first evaluation, got %d", len(*captured))
	}

	// A duplicate completion event for the same bot message must not schedule a
	// second, overlapping continuation and must not latch the loop off.
	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message); forwarded {
		t.Fatal("a duplicate completion event must not schedule another turn")
	}
	if len(*captured) != 1 {
		t.Fatalf("expected no additional scheduled bot reply, got %d", len(*captured))
	}
	state := loadAutoContinueState(DB, chat.ID)
	if state.Turns != 1 || state.Stopped {
		t.Fatalf("a duplicate event must leave the loop continuing, got %+v", state)
	}
}

// TestMaybeAutoContinueInteractionResumesAfterOutstandingTurn guards that a
// genuinely new bot turn clears the outstanding marker and advances the loop,
// so the no-overlap guard never wedges a healthy interaction.
func TestMaybeAutoContinueInteractionResumesAfterOutstandingTurn(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-resume@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-resume-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true,"auto_continue_delay_seconds":0}`)}
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

	first := textMessage("step one", `{"finished":true}`)
	first.ChatId = chat.ID
	first.SenderId = bot.ID
	first.ReceiverId = owner.ID
	if err := DB.Create(&first).Error; err != nil {
		t.Fatalf("failed to create first message: %v", err)
	}
	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, first); !forwarded {
		t.Fatal("expected the first turn to continue")
	}

	// A new bot turn (the scheduled continuation actually ran) must advance the
	// loop again.
	second := textMessage("step two", `{"finished":true}`)
	second.ChatId = chat.ID
	second.SenderId = bot.ID
	second.ReceiverId = owner.ID
	if err := DB.Create(&second).Error; err != nil {
		t.Fatalf("failed to create second message: %v", err)
	}
	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, second); !forwarded {
		t.Fatal("expected the next bot turn to continue the loop")
	}
	if len(*captured) != 2 {
		t.Fatalf("expected two scheduled bot replies, got %d", len(*captured))
	}
	if state := loadAutoContinueState(DB, chat.ID); state.Turns != 2 {
		t.Fatalf("expected the turn counter to reach 2, got %d", state.Turns)
	}
}

// TestMaybeAutoContinueInteractionStopsOnTaskCompletedFlag guards the
// end-to-end machine-readable completion: the loop stops deterministically and
// the completion notification is not suppressed.
func TestMaybeAutoContinueInteractionStopsOnTaskCompletedFlag(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-flag@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-flag-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true}`)}
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

	message := textMessage("all done", `{"finished":true,"task_completed":true}`)
	message.ChatId = chat.ID
	message.SenderId = bot.ID
	message.ReceiverId = owner.ID
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message); forwarded {
		t.Fatal("a task_completed flag must not schedule another turn")
	}
	if len(*captured) != 0 {
		t.Fatalf("expected no scheduled bot reply, got %d", len(*captured))
	}
	state := loadAutoContinueState(DB, chat.ID)
	if !state.Stopped || state.StopReason != "completed" {
		t.Fatalf("expected the loop to be stopped as completed, got %+v", state)
	}
}

// TestAutoContinueBudgetPersistsAcrossTurns guards the per-chat budget: the
// counter survives in ChatSettings and the loop is exhausted once it reaches the
// cap, so a restart can never grant unlimited turns.
func TestAutoContinueBudgetPersistsAcrossTurns(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-budget@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-budget-bot@example.com")
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 2, Pending: true, PendingSourceMessageID: 7}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	config := enabledConfig()
	config.MaxTurns = 2
	message := finishedMessage(`{"finished":true}`)
	message.ID = 99

	decision := decideAutoContinue(config, loadAutoContinueState(DB, chat.ID), message, decodeOrFail(t, message.MetaData), false)
	if decision.Continue {
		t.Fatal("the loop must not continue past its budget")
	}
	if decision.StopReason != autoContinueExhaustedReason {
		t.Fatalf("StopReason = %q, want %q", decision.StopReason, autoContinueExhaustedReason)
	}
}

// TestAutoContinueStatusProjection guards the agent/status-facing projection:
// only an opted-in interaction reports loop state.
func TestAutoContinueStatusProjection(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-status@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-status-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true,"auto_continue_max":7}`)}
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

	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 2}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	status := AutoContinueStatusForChat(DB, chat)
	if !status.Enabled || status.Turns != 2 || status.Max != 7 || status.StopReason != "" {
		t.Fatalf("unexpected loop status: %+v", status)
	}

	response := populateAutoContinueStatus(DB, chat, InteractionStatusResponse{ChatUUID: chat.UUID})
	if !response.AutoContinue || response.AutoContinueTurns != 2 || response.AutoContinueMax != 7 {
		t.Fatalf("unexpected status response: %+v", response)
	}

	plain := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&plain).Error; err != nil {
		t.Fatalf("failed to create plain chat: %v", err)
	}
	if status := AutoContinueStatusForChat(DB, plain); status.Enabled {
		t.Fatalf("a non-opted-in chat must not report a loop: %+v", status)
	}
	if response := populateAutoContinueStatus(DB, plain, InteractionStatusResponse{ChatUUID: plain.UUID}); response.AutoContinue {
		t.Fatalf("a non-opted-in chat must not be annotated: %+v", response)
	}
}

// TestAutoContinueStateRoundTrip verifies that the loop state survives in the
// chat's server-side settings and that stopping is authoritative.
func TestAutoContinueStateRoundTrip(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-state@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-state-bot@example.com")
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if state := loadAutoContinueState(DB, chat.ID); state.Stopped || state.Turns != 0 {
		t.Fatalf("expected a fresh state, got %+v", state)
	}
	if err := saveAutoContinueState(DB, chat.ID, autoContinueState{Turns: 3}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if state := loadAutoContinueState(DB, chat.ID); state.Turns != 3 || state.Stopped {
		t.Fatalf("unexpected reloaded state: %+v", state)
	}
	if err := StopAutoContinueForChat(DB, chat.ID, "interrupted_by_user"); err != nil {
		t.Fatalf("stop loop: %v", err)
	}
	state := loadAutoContinueState(DB, chat.ID)
	if !state.Stopped || state.StopReason != "interrupted_by_user" {
		t.Fatalf("unexpected stopped state: %+v", state)
	}
	if state.Turns != 3 {
		t.Fatalf("stopping must not reset the budget counter, got %d", state.Turns)
	}
}

// TestMaybeAutoContinueInteractionSchedulesNextTurn verifies the whole loop
// step: the continuation prompt is persisted as the owner's message, the bot
// reply is scheduled, the turn counter advances and the completion
// notification is suppressed (the helper reports "continuing").
func TestMaybeAutoContinueInteractionSchedulesNextTurn(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-loop@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-loop-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true,"auto_continue_delay_seconds":0}`)}
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

	message := textMessage("working on it", `{"finished":true}`)
	message.ChatId = chat.ID
	message.SenderId = bot.ID
	message.ReceiverId = owner.ID
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message)
	if !forwarded {
		t.Fatal("expected the loop to continue and suppress the completion notification")
	}
	if len(*captured) != 1 {
		t.Fatalf("expected one scheduled bot reply, got %d", len(*captured))
	}
	var continuation database.Message
	if err := DB.Where("chat_id = ? AND sender_id = ? AND meta_data IS NOT NULL", chat.ID, owner.ID).First(&continuation).Error; err != nil {
		t.Fatalf("expected a persisted continuation prompt: %v", err)
	}
	if continuation.ReceiverId != bot.ID {
		t.Fatalf("continuation must be addressed to the bot, got %d", continuation.ReceiverId)
	}
	metadata := map[string]interface{}{}
	if err := json.Unmarshal(continuation.MetaData, &metadata); err != nil {
		t.Fatalf("failed to decode continuation metadata: %v", err)
	}
	if scheduled, _ := metadata[autoContinueLabel].(bool); !scheduled {
		t.Fatalf("expected the %q marker on the continuation prompt, got %v", autoContinueLabel, metadata)
	}
	var reloaded database.Chat
	if err := DB.First(&reloaded, chat.ID).Error; err != nil {
		t.Fatalf("failed to reload chat: %v", err)
	}
	if reloaded.LatestMessageId == nil || *reloaded.LatestMessageId != continuation.ID {
		t.Fatal("the continuation prompt must become the chat's latest message")
	}
	if state := loadAutoContinueState(DB, chat.ID); state.Turns != 1 {
		t.Fatalf("expected the turn counter to advance to 1, got %d", state.Turns)
	}
}

// TestMaybeAutoContinueInteractionStopsOnCompletionMarker guards that the loop
// ends deterministically and the completion notification is not suppressed.
func TestMaybeAutoContinueInteractionStopsOnCompletionMarker(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-done@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-done-bot@example.com")

	shared := database.SharedChatConfig{ConfigData: json.RawMessage(`{"auto_continue":true}`)}
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

	message := textMessage("all done\n<<TASK_COMPLETE>>", `{"finished":true}`)
	message.ChatId = chat.ID
	message.SenderId = bot.ID
	message.ReceiverId = owner.ID
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message); forwarded {
		t.Fatal("a completion marker must not schedule another turn")
	}
	if len(*captured) != 0 {
		t.Fatalf("expected no scheduled bot reply, got %d", len(*captured))
	}
	state := loadAutoContinueState(DB, chat.ID)
	if !state.Stopped || state.StopReason != "completed" {
		t.Fatalf("expected the loop to be stopped as completed, got %+v", state)
	}
}

// TestMaybeAutoContinueInteractionIgnoresDisabledChat guards the opt-in gate: a
// chat without the setting never schedules a turn and never touches settings.
func TestMaybeAutoContinueInteractionIgnoresDisabledChat(t *testing.T) {
	captured := stubAutoContinueEnqueue(t)

	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "auto-continue-off@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "auto-continue-off-bot@example.com")
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	message := finishedMessage(`{"finished":true}`)
	message.ChatId = chat.ID
	message.SenderId = bot.ID
	message.ReceiverId = owner.ID
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	if forwarded := maybeAutoContinueInteraction(context.Background(), DB, nil, nil, chat, *bot, *owner, message); forwarded {
		t.Fatal("a non-opted-in chat must not auto-continue")
	}
	if len(*captured) != 0 {
		t.Fatalf("expected no scheduled bot reply, got %d", len(*captured))
	}
	if state := loadAutoContinueState(DB, chat.ID); state.Stopped || state.Turns != 0 {
		t.Fatalf("expected no loop state, got %+v", state)
	}
}
