package chats

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"backend/chatstate"
	"backend/database"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"backend/logging"
)

// This file implements the ci#609 "never a silent idle" guard for a native
// (msgmate_coder) interaction that cannot start.
//
// Symptom before this guard: when the sandbox node was saturated, a dispatched
// native interaction stayed `state=idle` with `newest_agent_msg=none` forever -
// no bot message, no "insufficient CPU" provisioning error, nothing an operator
// (or the external auto-continue dispatcher) could act on. The interaction
// looked frozen and the dispatched work was lost.
//
// The guard decides, from durable state only, whether an interaction is
// *stalled*:
//
//   - it is an interaction chat;
//   - nothing is live for it (no bot:reply task active/queued/scheduled, no
//     external chat backend reporting a running generation);
//   - the newest message is still the prompt (the agent never produced a turn
//     after it) and does not wait on the user (no pending confirmation);
//   - that prompt is older than the configured stall window.
//
// A stalled interaction is (1) reported in the interaction status
// (`stalled`/`stall_reason`/`stall_age_seconds`) and on the trusted
// agent-dispatch view, and (2) given one visible, persisted bot error message so
// the chat itself says why it never started. The report is idempotent: a chat
// gets at most one stall report, so re-reading the status cannot spam the
// transcript.

const (
	// interactionStallConfigKey is the per-agent shared-config key naming how
	// long an unstarted interaction may rest before it is reported as stalled.
	// 0 (or negative) disables the guard for that agent.
	interactionStallConfigKey = "interaction_stall_after_seconds"

	// InteractionStallEnvKey is the deployment-wide version of the same knob.
	// The env deliberately wins over the per-agent key, because the agent key
	// is routinely a materialised default (the ci#620 lesson).
	InteractionStallEnvKey = "OCI_INTERACTION_STALL_AFTER_SECONDS"

	// DefaultInteractionStallAfter is the stall window used when nothing else is
	// configured. It matches the window the external dispatcher loop observes
	// before it gives up (ci#537/#633, ~10 minutes).
	DefaultInteractionStallAfter = 10 * time.Minute

	// InteractionStallReasonNoFirstTurn is the recorded reason of a stall: no
	// agent turn ran after the interaction's prompt.
	InteractionStallReasonNoFirstTurn = "stall_no_first_turn"

	// interactionStallReportMetaKey marks the visible message this guard posts,
	// making the report idempotent.
	interactionStallReportMetaKey = "interaction_stall_report"

	// interactionStallScanLimit is how many recent messages are scanned for the
	// report marker.
	interactionStallScanLimit = 10
)

// InteractionStallPolicy is the resolved stall window of an interaction.
type InteractionStallPolicy struct {
	// After is how long an unstarted interaction may rest before it is reported.
	// Zero or less disables the guard.
	After time.Duration
	// Source names where the window came from (env:/config:/dispatch:/default),
	// so a status line can say which value actually won.
	Source string
}

// Enabled reports whether the guard is active for this policy.
func (p InteractionStallPolicy) Enabled() bool { return p.After > 0 }

// String renders the policy as one operator-readable line.
func (p InteractionStallPolicy) String() string {
	return fmt.Sprintf("stall_after=%s source=%s", p.After, p.Source)
}

// InteractionStallStatus is the resolved stall state of an interaction.
type InteractionStallStatus struct {
	Stalled bool
	Reason  string
	// Since is the timestamp of the prompt that never got an agent turn.
	Since time.Time
	// Age is how long that prompt has rested.
	Age time.Duration
	// Threshold is the resolved stall window.
	Threshold time.Duration
	// Reported records that the visible stall error was already posted.
	Reported bool
}

// ResolveInteractionStallPolicy resolves the stall window from a chat shared
// config. Precedence: deployment env → per-agent config key → the
// `agent_dispatch` block written by POST /api/v1/agents/dispatch → default.
func ResolveInteractionStallPolicy(config map[string]interface{}) InteractionStallPolicy {
	policy := InteractionStallPolicy{After: DefaultInteractionStallAfter, Source: "default"}

	if seconds, ok := interactionStallEnvSeconds(); ok {
		policy.After = interactionStallSecondsToDuration(seconds)
		policy.Source = "env:" + InteractionStallEnvKey
		return policy
	}

	if value, ok := configInt(config, interactionStallConfigKey); ok {
		policy.After = interactionStallSecondsToDuration(value)
		policy.Source = "config:" + interactionStallConfigKey
		return policy
	}

	dispatch := map[string]interface{}{}
	if config != nil {
		if raw, ok := config[agentDispatchConfigKey].(map[string]interface{}); ok {
			dispatch = raw
		}
	}
	if value, ok := configInt(dispatch, interactionStallConfigKey); ok {
		policy.After = interactionStallSecondsToDuration(value)
		policy.Source = "dispatch:" + interactionStallConfigKey
	}
	return policy
}

// interactionStallSecondsToDuration converts a configured second count into the
// stall window. A non-positive value disables the guard.
func interactionStallSecondsToDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// interactionStallEnvSeconds reads the deployment-wide stall window in seconds.
func interactionStallEnvSeconds() (int, bool) {
	raw := strings.TrimSpace(os.Getenv(InteractionStallEnvKey))
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// InteractionStallStatusForChat resolves the stall state of a chat for an
// out-of-package caller (the trusted agent-dispatch surface).
func InteractionStallStatusForChat(db *gorm.DB, inspector *asynq.Inspector, chat database.Chat) InteractionStallStatus {
	return detectInteractionStall(db, inspector, chat, time.Now())
}

// detectInteractionStall reports whether an interaction is stalled: nothing is
// live for it and its newest message is an unanswered prompt older than the
// resolved stall window.
func detectInteractionStall(db *gorm.DB, inspector *asynq.Inspector, chat database.Chat, now time.Time) InteractionStallStatus {
	status := InteractionStallStatus{}
	if db == nil || chat.ID == 0 || !isInteractionChatType(chat.ChatType) {
		return status
	}
	chat = hydrateChatParticipants(db, chat)
	policy := ResolveInteractionStallPolicy(sharedConfigMapForChat(db, chat))
	status.Threshold = policy.After
	if !policy.Enabled() {
		return status
	}
	// A live turn (or a live external backend generation) is never a stall.
	if interactionHasLiveActivity(db, inspector, chat) {
		return status
	}
	return interactionStallFromMessages(db, chat, now)
}

// interactionStallFromMessages is the durable-state half of the detection: the
// caller has already established that nothing is live for the chat, so the
// decision is made from the newest message alone. Keeping it separate means the
// status resolver - which already performed the queue/backend liveness checks -
// can annotate a stall without a second queue round-trip per chat (the batch
// `/chats/states` endpoint resolves up to 100 chats per poll).
func interactionStallFromMessages(db *gorm.DB, chat database.Chat, now time.Time) InteractionStallStatus {
	status := InteractionStallStatus{}
	if db == nil || chat.ID == 0 || !isInteractionChatType(chat.ChatType) {
		return status
	}
	chat = hydrateChatParticipants(db, chat)
	policy := ResolveInteractionStallPolicy(sharedConfigMapForChat(db, chat))
	status.Threshold = policy.After
	if !policy.Enabled() {
		return status
	}

	latest, err := latestMessageForChat(db, chat.ID)
	if err != nil {
		return status
	}
	// A pending confirmation means the interaction waits on the user, not on a
	// sandbox: that is a decision, never a stall.
	if len(pendingActionsForMessage(latest)) > 0 {
		return status
	}
	// The agent already answered after this message: not a no-first-turn stall.
	if bot := interactionAutomatedParticipant(chat); bot != nil && latest.SenderId == bot.ID {
		return status
	}

	age := now.Sub(latest.CreatedAt)
	if age < policy.After {
		return status
	}

	status.Stalled = true
	status.Reason = InteractionStallReasonNoFirstTurn
	status.Since = latest.CreatedAt
	status.Age = age
	status.Reported = interactionStallReported(db, chat.ID)
	return status
}

// interactionHasLiveActivity reports whether any work is live for the chat: a
// queued/scheduled/active bot reply, a reply queued behind a live turn, or an
// external chat backend reporting a running generation.
func interactionHasLiveActivity(db *gorm.DB, inspector *asynq.Inspector, chat database.Chat) bool {
	if chatHasLiveBotReply(inspector, chat.UUID) {
		return true
	}
	if inspector != nil && workqueue.BotReplyIsActive(inspector, chat.UUID) {
		return true
	}
	return interactionBackendIsRunning(db, chat)
}

// interactionBackendIsRunning reports whether an external chat backend owns a
// live generation for the chat.
func interactionBackendIsRunning(db *gorm.DB, chat database.Chat) bool {
	if db == nil {
		return false
	}
	backend, ok := chatBackendName(db, chat)
	if !ok {
		return false
	}
	provider, ok := chatstate.LookupBackendStateProvider(backend)
	if !ok {
		return false
	}
	state, ok := provider(chat.UUID)
	return ok && state == chatstate.BackendStateRunning
}

// interactionStallReported reports whether the visible stall error was already
// posted for the chat.
func interactionStallReported(db *gorm.DB, chatID uint) bool {
	if db == nil || chatID == 0 {
		return false
	}
	var messages []database.Message
	if err := db.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Limit(interactionStallScanLimit).
		Find(&messages).Error; err != nil {
		return false
	}
	for _, message := range messages {
		if len(message.MetaData) == 0 {
			continue
		}
		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) != nil {
			continue
		}
		if reported, _ := meta[interactionStallReportMetaKey].(bool); reported {
			return true
		}
	}
	return false
}

// ReportInteractionStall posts the visible stall error for a stalled
// interaction, exactly once. It returns true when a message was posted. It is a
// no-op for a healthy chat, for a chat whose report already exists and for a
// chat without an automated participant.
//
// The message is authored by the interaction's bot (so it lands in the chat as
// an agent turn) and carries `finished` + `error` metadata, which makes the
// interaction resolve as `failed` with a reason instead of resting in a silent
// `idle`.
func ReportInteractionStall(db *gorm.DB, inspector *asynq.Inspector, chat database.Chat) bool {
	if db == nil || chat.ID == 0 {
		return false
	}
	status := detectInteractionStall(db, inspector, chat, time.Now())
	if !status.Stalled || status.Reported {
		return false
	}
	chat = hydrateChatParticipants(db, chat)
	bot := interactionAutomatedParticipant(chat)
	if bot == nil {
		return false
	}
	owner := chat.User1
	if owner.ID == bot.ID {
		owner = chat.User2
	}
	if owner.ID == 0 {
		return false
	}

	text := buildInteractionStallText(status)
	metadata := map[string]interface{}{
		"finished":                    true,
		"error":                       true,
		"failure_reason":              InteractionStallReasonNoFirstTurn,
		interactionStallReportMetaKey: true,
		"interaction_stall": map[string]interface{}{
			"reason":            status.Reason,
			"since":             status.Since.UTC().Format(time.RFC3339),
			"age_seconds":       int(status.Age / time.Second),
			"threshold_seconds": int(status.Threshold / time.Second),
			"detected_at":       time.Now().UTC().Format(time.RFC3339),
		},
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return false
	}

	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		Text:       &text,
		DataType:   "text",
		MetaData:   database.JSONRaw(encoded),
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).
			Update("latest_message_id", message.ID).Error
	}); err != nil {
		logging.Errorf("interaction stall: failed to report the stall for chat %s: %v", chat.UUID, err)
		return false
	}
	logging.Debugf(
		"interaction stall reported for chat %s: no agent turn after %s (age %s, threshold %s, ci#609)",
		chat.UUID, status.Since.UTC().Format(time.RFC3339), status.Age.Round(time.Second), status.Threshold,
	)
	return true
}

// buildInteractionStallText renders the operator/model-facing stall report: what
// was observed, why the interaction is reported instead of resting silently, and
// what to do next.
func buildInteractionStallText(status InteractionStallStatus) string {
	age := status.Age.Round(time.Second)
	threshold := status.Threshold.Round(time.Second)
	reason := "no agent turn ran for this interaction and nothing is queued or running for the chat"
	if status.Reason == InteractionStallReasonNoFirstTurn {
		reason = "no agent turn ran after the prompt and nothing is queued or running for this chat"
	}
	return fmt.Sprintf(
		"This interaction never started: %s (prompt rested %s, stall window %s). "+
			"A native coding sandbox can usually not be provisioned when the node is out of CPU/memory, "+
			"which used to leave the interaction silently idle. It is reported as stalled with reason %q instead. "+
			"Re-dispatch the interaction, or continue it once capacity is free, so the work is not lost.",
		reason, age, threshold, status.Reason,
	)
}
