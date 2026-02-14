package chats

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"backend/database"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// This file implements the native in-product auto-continuation loop (ci#537):
// a self-driving interaction that keeps producing turns after each clean
// completion until the agent declares the task done (completion marker) or asks
// for a human decision (human marker), bounded by a per-chat budget.
//
// It is the product-side counterpart of the external dispatcher loop in
// trigger-open-chat-coding-interaction.yaml: instead of an outside watcher
// re-prompting the agent after every turn, the core schedules the next turn
// itself at the automated-turn completion choke point
// (maybeDispatchInteractionCompletedNotification in MessageSend.go).
//
// Opt-in is per interaction and resolved from the chat's shared config:
//
//	auto_continue                  bool   — opt in (default false)
//	auto_continue_max              int    — hard cap of continuations
//	auto_continue_delay_seconds    int    — quiet period before the wake-up
//	auto_continue_prompt           string — continuation instruction
//	auto_continue_completion_marker string— sentinel that stops the loop
//	auto_continue_human_marker     string — sentinel that stops the loop
//
// The `agent_dispatch` metadata recorded by POST /api/v1/agents/dispatch
// (auto_continue / completion_marker / human_marker) is honoured as a fallback,
// so an in-app dispatched agent self-drives without any external watcher.
//
// Loop state (turn counter, stop reason) lives in database.ChatSettings so it
// survives restarts. Stopping is authoritative: an interrupt (user stop) or an
// approved/rejected confirmation clears the loop, and a per-chat stop flag
// prevents a scheduled wake-up from resuming a stopped interaction.
const (
	// autoContinueConfigKey is the shared-config key opting an interaction into
	// the in-product continuation loop.
	autoContinueConfigKey = "auto_continue"
	// autoContinueMaxKey caps the number of continuations per interaction.
	autoContinueMaxKey = "auto_continue_max"
	// autoContinueDelayKey is the quiet period in seconds before the wake-up.
	autoContinueDelayKey = "auto_continue_delay_seconds"
	// autoContinuePromptKey overrides the continuation instruction.
	autoContinuePromptKey = "auto_continue_prompt"
	// autoContinueCompletionMarkerKey overrides the default completion sentinel.
	autoContinueCompletionMarkerKey = "auto_continue_completion_marker"
	// autoContinueHumanMarkerKey overrides the default human sentinel.
	autoContinueHumanMarkerKey = "auto_continue_human_marker"

	// agentDispatchConfigKey is the shared-config block written by the trusted
	// agent-dispatch surface; its options are honoured as a fallback so a
	// dispatched agent keeps working in-product.
	agentDispatchConfigKey = "agent_dispatch"

	// DefaultAutoContinueCompletionMarker is the sentinel an agent emits to
	// declare the whole task complete (stops the loop). It matches the
	// dispatcher workflow's default so a single convention covers both paths.
	DefaultAutoContinueCompletionMarker = "<<TASK_COMPLETE>>"
	// DefaultAutoContinueHumanMarker is the sentinel an agent emits to request a
	// human decision (stops the loop).
	DefaultAutoContinueHumanMarker = "<<NEED_HUMAN>>"

	// defaultAutoContinueMax bounds the loop when no explicit budget is set.
	defaultAutoContinueMax = 30
	// defaultAutoContinueDelaySeconds is the default quiet period between turns.
	defaultAutoContinueDelaySeconds = 10
	// maxAutoContinueDelaySeconds caps a configured delay so a misconfigured
	// value cannot park a loop for an unreasonable time.
	maxAutoContinueDelaySeconds = 3600

	// autoContinueLabel is the mode label recorded on continuation prompts.
	autoContinueLabel = "auto_continue"

	// autoContinueExhaustedReason is recorded when the loop stops on its budget.
	autoContinueExhaustedReason = "budget_exhausted"
)

// autoContinueSettingsSchema documents the state keys stored in
// database.ChatSettings for the loop.
const (
	autoContinueTurnsStateKey      = "auto_continue_turns"
	autoContinueStoppedStateKey    = "auto_continue_stopped"
	autoContinueStopReasonStateKey = "auto_continue_stop_reason"
)

// AutoContinueConfig is the resolved auto-continuation policy of an interaction.
type AutoContinueConfig struct {
	Enabled          bool
	MaxTurns         int
	Delay            time.Duration
	Prompt           string
	CompletionMarker string
	HumanMarker      string
}

// ResolveAutoContinueConfig resolves the loop policy from a chat shared config.
// Explicit top-level keys win; the `agent_dispatch` block recorded by an in-app
// agent dispatch is used as a fallback so a dispatched agent self-drives
// without an external watcher.
func ResolveAutoContinueConfig(config map[string]interface{}) AutoContinueConfig {
	resolved := AutoContinueConfig{
		MaxTurns:         defaultAutoContinueMax,
		Delay:            defaultAutoContinueDelaySeconds * time.Second,
		CompletionMarker: DefaultAutoContinueCompletionMarker,
		HumanMarker:      DefaultAutoContinueHumanMarker,
	}
	if config == nil {
		resolved.Prompt = defaultAutoContinuePrompt(resolved)
		return resolved
	}

	dispatch := map[string]interface{}{}
	if raw, ok := config[agentDispatchConfigKey].(map[string]interface{}); ok {
		dispatch = raw
	}

	resolved.Enabled = configBool(config, autoContinueConfigKey) || configBool(dispatch, autoContinueConfigKey)

	if value, ok := configInt(config, autoContinueMaxKey); ok {
		resolved.MaxTurns = value
	} else if value, ok := configInt(dispatch, autoContinueMaxKey); ok {
		// Honour the budget attached by POST /api/v1/agents/dispatch.
		resolved.MaxTurns = value
	}
	if value, ok := configInt(config, autoContinueDelayKey); ok {
		resolved.Delay = time.Duration(clampAutoContinueDelay(value)) * time.Second
	} else if value, ok := configInt(dispatch, autoContinueDelayKey); ok {
		resolved.Delay = time.Duration(clampAutoContinueDelay(value)) * time.Second
	}
	if value := configString(config, autoContinuePromptKey); value != "" {
		resolved.Prompt = value
	}
	if value := configString(config, autoContinueCompletionMarkerKey); value != "" {
		resolved.CompletionMarker = value
	} else if value := configString(dispatch, completionMarkerConfigKey); value != "" {
		resolved.CompletionMarker = value
	}
	if value := configString(config, autoContinueHumanMarkerKey); value != "" {
		resolved.HumanMarker = value
	} else if value := configString(dispatch, humanMarkerConfigKey); value != "" {
		resolved.HumanMarker = value
	}
	if resolved.MaxTurns <= 0 {
		resolved.MaxTurns = defaultAutoContinueMax
	}
	if resolved.Prompt == "" {
		resolved.Prompt = defaultAutoContinuePrompt(resolved)
	}
	return resolved
}

// completionMarkerConfigKey is the dispatch metadata key for the completion
// sentinel.
const completionMarkerConfigKey = "completion_marker"

// humanMarkerConfigKey is the dispatch metadata key for the human sentinel.
const humanMarkerConfigKey = "human_marker"

// defaultAutoContinuePrompt renders the continuation instruction, naming the
// sentinels the agent must emit to end the loop.
func defaultAutoContinuePrompt(config AutoContinueConfig) string {
	return fmt.Sprintf(
		"Continue with the task now: pick up exactly where you left off and make concrete progress this turn; do not ask whether to continue. "+
			"When the WHOLE task is complete and verified, end your reply with the line %s. "+
			"If and only if you are blocked on a decision only a human can make, end with the line %s and state exactly what you need.",
		config.CompletionMarker, config.HumanMarker,
	)
}

// IsStopMarker reports whether an agent reply declares the loop done, either
// because the task is complete or because a human decision is required, and
// returns the recorded stop reason ("completed" or "needs_human").
func (c AutoContinueConfig) IsStopMarker(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	if marker := strings.TrimSpace(c.HumanMarker); marker != "" && strings.Contains(text, marker) {
		return "needs_human", true
	}
	if marker := strings.TrimSpace(c.CompletionMarker); marker != "" && strings.Contains(text, marker) {
		return "completed", true
	}
	return "", false
}

// autoContinueState is the persisted loop state of an interaction.
type autoContinueState struct {
	Turns      int
	Stopped    bool
	StopReason string
}

// autoContinueDecision is the outcome of the loop decision for one finished
// automated turn.
type autoContinueDecision struct {
	Continue   bool
	Reason     string
	Prompt     string
	Delay      time.Duration
	Turns      int
	MaxTurns   int
	StopReason string
}

// decideAutoContinue decides whether a finished automated turn should schedule
// another turn. It is deliberately pure (no I/O) so the whole policy is unit
// testable: it never continues a failed turn, a turn that still waits on the
// user (a pending action), a turn that emitted a stop marker, a chat with a
// live turn, or one whose budget is exhausted.
func decideAutoContinue(config AutoContinueConfig, state autoContinueState, message database.Message, meta map[string]interface{}, activeTurn bool) autoContinueDecision {
	decision := autoContinueDecision{Turns: state.Turns, MaxTurns: config.MaxTurns}
	if !config.Enabled {
		return decision
	}
	if state.Stopped {
		decision.StopReason = state.StopReason
		if decision.StopReason == "" {
			decision.StopReason = "stopped"
		}
		return decision
	}
	// A live turn must never be overlapped by a scheduled wake-up.
	if activeTurn {
		decision.StopReason = "turn_in_flight"
		return decision
	}
	if !isInteractionCompletionMetadata(message, meta) {
		decision.StopReason = "not_a_clean_completion"
		return decision
	}
	if len(pendingActionsForMessage(message)) > 0 {
		decision.StopReason = "pending_action"
		return decision
	}
	if reason, stopped := config.IsStopMarker(messageText(message)); stopped {
		decision.StopReason = reason
		return decision
	}
	if state.Turns >= config.MaxTurns {
		decision.StopReason = autoContinueExhaustedReason
		return decision
	}
	decision.Continue = true
	decision.Reason = "continuation"
	decision.Prompt = config.Prompt
	decision.Delay = config.Delay
	return decision
}

// maybeAutoContinueInteraction is the completion choke-point hook. It schedules
// the next turn of a self-driving interaction and reports whether the loop is
// continuing (in which case the interaction is not actually complete yet and
// the completion notification must be suppressed).
//
// It is best-effort: failures are logged and never affect message delivery.
func maybeAutoContinueInteraction(ctx context.Context, db *gorm.DB, client *asynq.Client, inspector *asynq.Inspector, chat database.Chat, sender, receiver database.User, message database.Message) bool {
	if db == nil {
		return false
	}
	if !sender.IsAutomated || receiver.IsAutomated || receiver.ID == 0 {
		return false
	}
	if !isInteractionChatType(chat.ChatType) {
		return false
	}
	chat = hydrateChatParticipants(db, chat)
	config := ResolveAutoContinueConfig(sharedConfigMapForChat(db, chat))
	if !config.Enabled {
		return false
	}
	meta, _ := decodeMessageMetadata(message.MetaData)
	state := loadAutoContinueState(db, chat.ID)
	activeTurn := chatHasLiveBotReply(inspector, chat.UUID)

	decision := decideAutoContinue(config, state, message, meta, activeTurn)
	if !decision.Continue {
		recordAutoContinueStop(db, chat, state, decision.StopReason)
		return false
	}

	if err := scheduleAutoContinuePrompt(ctx, db, client, inspector, chat, receiver, decision); err != nil {
		log.Printf("auto-continue scheduling failed for chat %s: %v", chat.UUID, err)
		return false
	}
	return true
}

// scheduleAutoContinuePrompt persists the continuation prompt as the owner's
// message and schedules the agent's next turn after the configured delay.
func scheduleAutoContinuePrompt(ctx context.Context, db *gorm.DB, client *asynq.Client, inspector *asynq.Inspector, chat database.Chat, owner database.User, decision autoContinueDecision) error {
	bot := interactionAutomatedParticipant(chat)
	if bot == nil {
		return fmt.Errorf("interaction has no automated participant")
	}
	prompt := strings.TrimSpace(decision.Prompt)
	if prompt == "" {
		return fmt.Errorf("continuation prompt is empty")
	}

	metadata := map[string]interface{}{
		autoContinueLabel:            true,
		autoContinueTurnsStateKey:    decision.Turns + 1,
		autoContinueMaxKey:           decision.MaxTurns,
		"auto_continue_scheduled_by": "in_product_loop",
	}
	if decision.Delay > 0 {
		metadata["auto_continue_delay_seconds"] = int(decision.Delay / time.Second)
	}
	if previous := strings.TrimSpace(chatLatestMessageUUID(db, chat.ID)); previous != "" {
		metadata["auto_continue_previous_message_uuid"] = previous
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	text := prompt
	continuation := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: bot.ID,
		Text:       &text,
		DataType:   "text",
		MetaData:   database.JSONRaw(encoded),
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&continuation).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).Update("latest_message_id", continuation.ID).Error
	}); err != nil {
		return err
	}

	opts := []asynq.Option{}
	if decision.Delay > 0 {
		opts = append(opts, asynq.ProcessIn(decision.Delay))
	}
	if _, err := autoContinueEnqueue(client, inspector, workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: continuation.UUID,
		BotUserID:   bot.ID,
	}, opts...); err != nil {
		return err
	}

	return saveAutoContinueState(db, chat.ID, autoContinueState{
		Turns:   decision.Turns + 1,
		Stopped: false,
	})
}

// interactionAutomatedParticipant returns the automated participant of a chat.
func interactionAutomatedParticipant(chat database.Chat) *database.User {
	if chat.User1.IsAutomated {
		return &chat.User1
	}
	if chat.User2.IsAutomated {
		return &chat.User2
	}
	return nil
}

// hydrateChatParticipants loads the chat's participants and shared config when
// the caller only passed a partially populated chat, so the loop can always
// resolve the automated participant and the loop policy.
func hydrateChatParticipants(db *gorm.DB, chat database.Chat) database.Chat {
	if db == nil || chat.ID == 0 {
		return chat
	}
	if chat.User1.ID != 0 && chat.User2.ID != 0 && chat.SharedConfig != nil {
		return chat
	}
	loaded := database.Chat{}
	if err := db.Preload("User1").Preload("User2").Preload("SharedConfig").First(&loaded, chat.ID).Error; err != nil {
		return chat
	}
	return loaded
}

// chatLatestMessageUUID returns the UUID of the chat's most recent message.
func chatLatestMessageUUID(db *gorm.DB, chatID uint) string {
	message, err := latestMessageForChat(db, chatID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(message.UUID)
}

// AutoContinueStatus is the exported, agent-facing projection of an
// interaction's auto-continuation loop (ci#537), so the trusted agent-dispatch
// surface can report whether a managed agent keeps working by itself.
type AutoContinueStatus struct {
	Enabled    bool
	Turns      int
	Max        int
	StopReason string
}

// AutoContinueStatusForChat resolves the loop status of a chat for an
// out-of-package caller (the agent-dispatch surface).
func AutoContinueStatusForChat(db *gorm.DB, chat database.Chat) AutoContinueStatus {
	status := AutoContinueStatus{}
	if db == nil || !isInteractionChatType(chat.ChatType) {
		return status
	}
	config := ResolveAutoContinueConfig(sharedConfigMapForChat(db, chat))
	if !config.Enabled {
		return status
	}
	state := loadAutoContinueState(db, chat.ID)
	status.Enabled = true
	status.Turns = state.Turns
	status.Max = config.MaxTurns
	status.StopReason = state.StopReason
	return status
}

// populateAutoContinueStatus annotates an interaction status response with the
// auto-continuation loop's live policy and state, so a client (and a watching
// agent) can tell that the interaction keeps working by itself and why it
// stopped. A chat that never opted in is left untouched.
func populateAutoContinueStatus(db *gorm.DB, chat database.Chat, response InteractionStatusResponse) InteractionStatusResponse {
	if db == nil || !isInteractionChatType(chat.ChatType) {
		return response
	}
	config := ResolveAutoContinueConfig(sharedConfigMapForChat(db, chat))
	if !config.Enabled {
		return response
	}
	state := loadAutoContinueState(db, chat.ID)
	response.AutoContinue = true
	response.AutoContinueTurns = state.Turns
	response.AutoContinueMax = config.MaxTurns
	response.AutoContinueStopReason = state.StopReason
	return response
}

// autoContinueEnqueue is the enqueue indirection so tests can assert the
// scheduled continuation without a queue.
var autoContinueEnqueue = func(client *asynq.Client, inspector *asynq.Inspector, payload workqueue.BotReplyPayload, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return workqueue.EnqueueBotReply(client, inspector, payload, opts...)
}

// chatHasLiveBotReply reports whether a bot reply task is currently queued or
// running for the chat. A live turn must never be overlapped by a scheduled
// continuation.
func chatHasLiveBotReply(inspector *asynq.Inspector, chatUUID string) bool {
	if inspector == nil || chatUUID == "" {
		return false
	}
	task, err := inspector.GetTaskInfo(workqueue.QueueDefault, workqueue.BotReplyTaskID(chatUUID))
	if err != nil || task == nil {
		return false
	}
	return isQueueStateActive(strings.ToLower(strings.TrimSpace(task.State.String())))
}

// StopAutoContinueForChat clears the loop for a chat authoritatively: a
// scheduled continuation must not resume an interaction the user stopped. It is
// called by the interrupt signal and by the agent-stop surface.
func StopAutoContinueForChat(db *gorm.DB, chatID uint, reason string) error {
	if db == nil || chatID == 0 {
		return nil
	}
	state := loadAutoContinueState(db, chatID)
	if state.Stopped && state.StopReason == strings.TrimSpace(reason) {
		return nil
	}
	state.Stopped = true
	if strings.TrimSpace(reason) != "" {
		state.StopReason = strings.TrimSpace(reason)
	} else {
		state.StopReason = "stopped"
	}
	return saveAutoContinueState(db, chatID, state)
}

// ResumeAutoContinueForChat clears a terminal loop stop so a stalled/stopped
// self-driving interaction can self-drive again after a nudge (ci#540
// `hive_mind_agent_continue`). It only clears the stop latch; the turn counter
// is left intact so the remaining budget still applies. A chat whose loop was
// never enabled is untouched.
func ResumeAutoContinueForChat(db *gorm.DB, chatID uint) error {
	if db == nil || chatID == 0 {
		return nil
	}
	state := loadAutoContinueState(db, chatID)
	if !state.Stopped && strings.TrimSpace(state.StopReason) == "" {
		return nil
	}
	state.Stopped = false
	state.StopReason = ""
	return saveAutoContinueState(db, chatID, state)
}

// loadAutoContinueState reads the persisted loop state of a chat.
func loadAutoContinueState(db *gorm.DB, chatID uint) autoContinueState {
	state := autoContinueState{}
	config := chatSettingsConfig(db, chatID)
	if config == nil {
		return state
	}
	if turns, ok := configInt(config, autoContinueTurnsStateKey); ok && turns > 0 {
		state.Turns = turns
	}
	state.Stopped = configBool(config, autoContinueStoppedStateKey)
	state.StopReason = configString(config, autoContinueStopReasonStateKey)
	return state
}

// saveAutoContinueState persists the loop state of a chat.
func saveAutoContinueState(db *gorm.DB, chatID uint, state autoContinueState) error {
	if db == nil || chatID == 0 {
		return nil
	}
	settings := database.ChatSettings{}
	findErr := db.Where("chat_id = ?", chatID).First(&settings).Error
	if findErr != nil && findErr != gorm.ErrRecordNotFound {
		return findErr
	}
	config := map[string]interface{}{}
	if findErr == nil && len(settings.ConfigData) > 0 {
		_ = json.Unmarshal(settings.ConfigData, &config)
	}
	config[autoContinueTurnsStateKey] = state.Turns
	config[autoContinueStoppedStateKey] = state.Stopped
	if state.StopReason != "" {
		config[autoContinueStopReasonStateKey] = state.StopReason
	} else {
		delete(config, autoContinueStopReasonStateKey)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if findErr == gorm.ErrRecordNotFound {
		return db.Create(&database.ChatSettings{ChatId: chatID, ConfigData: encoded}).Error
	}
	return db.Model(&settings).Update("config_data", encoded).Error
}

// recordAutoContinueStop records why a loop stopped, so the state is inspectable
// and a later restart does not silently resume. Only terminal reasons are
// recorded: transient guards (a live turn, a not-yet-finished message, a turn
// that paused on a user action and will resume through the confirmation path)
// must not latch the loop off.
func recordAutoContinueStop(db *gorm.DB, chat database.Chat, state autoContinueState, reason string) {
	switch strings.TrimSpace(reason) {
	case "", "continuation", "not_a_clean_completion", "turn_in_flight", "pending_action":
		return
	}
	if state.Stopped && state.StopReason == reason {
		return
	}
	state.Stopped = true
	state.StopReason = reason
	if err := saveAutoContinueState(db, chat.ID, state); err != nil {
		log.Printf("auto-continue: failed to record stop reason %q for chat %s: %v", reason, chat.UUID, err)
	}
}

// chatSettingsConfig decodes a chat's server-side settings block.
func chatSettingsConfig(db *gorm.DB, chatID uint) map[string]interface{} {
	if db == nil || chatID == 0 {
		return nil
	}
	settings := database.ChatSettings{}
	if err := db.Where("chat_id = ?", chatID).First(&settings).Error; err != nil || len(settings.ConfigData) == 0 {
		return nil
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(settings.ConfigData, &config); err != nil {
		return nil
	}
	return config
}

// messageText returns a message's text content.
func messageText(message database.Message) string {
	if message.Text == nil {
		return ""
	}
	return *message.Text
}

// clampAutoContinueDelay bounds a configured quiet period.
func clampAutoContinueDelay(seconds int) int {
	if seconds < 0 {
		return 0
	}
	if seconds > maxAutoContinueDelaySeconds {
		return maxAutoContinueDelaySeconds
	}
	return seconds
}

func configBool(config map[string]interface{}, key string) bool {
	if config == nil {
		return false
	}
	switch typed := config[key].(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

func configString(config map[string]interface{}, key string) string {
	if config == nil {
		return ""
	}
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func configInt(config map[string]interface{}, key string) (int, bool) {
	if config == nil {
		return 0, false
	}
	switch typed := config[key].(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	case json.Number:
		value, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(value), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		value := 0
		if _, err := fmt.Sscanf(trimmed, "%d", &value); err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}
