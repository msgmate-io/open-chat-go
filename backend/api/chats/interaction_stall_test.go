package chats

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// stallTestChatSeq makes each chat in a test use distinct user names (the user
// table enforces a unique username/email).
var stallTestChatSeq int

// ci#609: a native interaction that could not provision used to stay
// `state=idle` with no bot message and no error, forever. These tests pin the
// guard: a stalled interaction is reported (status + one visible bot error), a
// healthy/idle one is not, and the report is idempotent.

// stallTestChat provisions an interaction chat between a human owner and an
// automated bot with the given shared config, plus the initial prompt. The user
// names are suffixed per call so one test can build several chats in one DB.
func stallTestChat(t *testing.T, DB *gorm.DB, config map[string]interface{}) (database.Chat, database.User, database.User, database.Message) {
	t.Helper()
	stallTestChatSeq++
	suffix := strconv.Itoa(stallTestChatSeq)
	owner := createUserForChatsTest(t, DB, "stall-owner-"+suffix+"@example.com", false)
	bot := createUserForChatsTest(t, DB, "stall-bot-"+suffix+"@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark the bot automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create the chat: %v", err)
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to encode the shared config: %v", err)
	}
	shared := database.SharedChatConfig{ChatId: chat.ID, ConfigData: encoded}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create the shared config: %v", err)
	}
	if err := DB.Model(&chat).Update("shared_config_id", shared.ID).Error; err != nil {
		t.Fatalf("failed to link the shared config: %v", err)
	}

	text := "Implement the ticket."
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: bot.ID,
		DataType:   "text",
		Text:       &text,
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create the prompt: %v", err)
	}
	if err := DB.Model(&chat).Update("latest_message_id", message.ID).Error; err != nil {
		t.Fatalf("failed to set the latest message: %v", err)
	}

	loaded := database.Chat{}
	if err := DB.Preload("User1").Preload("User2").Preload("SharedConfig").First(&loaded, chat.ID).Error; err != nil {
		t.Fatalf("failed to reload the chat: %v", err)
	}
	return loaded, *owner, *bot, message
}

// ageMessagesBackdates pushes every message of a chat into the past so the
// stall window is exceeded without sleeping.
func ageMessagesBackdates(t *testing.T, DB *gorm.DB, chatID uint, age time.Duration) {
	t.Helper()
	past := time.Now().Add(-age)
	if err := DB.Model(&database.Message{}).Where("chat_id = ?", chatID).
		Update("created_at", past).Error; err != nil {
		t.Fatalf("failed to backdate the messages: %v", err)
	}
}

func TestResolveInteractionStallPolicyPrecedence(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	def := ResolveInteractionStallPolicy(nil)
	if def.After != DefaultInteractionStallAfter || def.Source != "default" {
		t.Fatalf("default policy = %s, want the default window", def)
	}
	if !def.Enabled() {
		t.Fatal("the default policy must be enabled")
	}

	fromConfig := ResolveInteractionStallPolicy(map[string]interface{}{
		interactionStallConfigKey: 120,
	})
	if fromConfig.After != 2*time.Minute {
		t.Fatalf("config window = %s, want 2m", fromConfig.After)
	}

	fromDispatch := ResolveInteractionStallPolicy(map[string]interface{}{
		"agent_dispatch": map[string]interface{}{interactionStallConfigKey: 45},
	})
	if fromDispatch.After != 45*time.Second {
		t.Fatalf("dispatch window = %s, want 45s", fromDispatch.After)
	}

	// The deployment lever wins over the (routinely materialised) agent key.
	t.Setenv(InteractionStallEnvKey, "600")
	fromEnv := ResolveInteractionStallPolicy(map[string]interface{}{
		interactionStallConfigKey: 30,
	})
	if fromEnv.After != 10*time.Minute || fromEnv.Source != "env:"+InteractionStallEnvKey {
		t.Fatalf("env policy = %s, want the deployment window to win", fromEnv)
	}
	t.Setenv(InteractionStallEnvKey, "")
}

func TestResolveInteractionStallPolicyDisabled(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	policy := ResolveInteractionStallPolicy(map[string]interface{}{interactionStallConfigKey: 0})
	if policy.Enabled() {
		t.Fatalf("a zero window must disable the guard, got %s", policy)
	}
	// A malformed env value falls back to the remaining precedence chain rather
	// than silently disabling the guard.
	t.Setenv(InteractionStallEnvKey, "not-a-number")
	if got := ResolveInteractionStallPolicy(nil).After; got != DefaultInteractionStallAfter {
		t.Fatalf("malformed env window = %s, want the default", got)
	}
	t.Setenv(InteractionStallEnvKey, "")
}

// TestDetectInteractionStallReportsUnstartedInteraction is the ci#609
// regression: an interaction whose prompt was never answered, with nothing
// live for it, is stalled.
func TestDetectInteractionStallReportsUnstartedInteraction(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, _, _ := stallTestChat(t, DB, map[string]interface{}{})
	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)

	status := detectInteractionStall(DB, nil, chat, time.Now())
	if !status.Stalled {
		t.Fatal("expected an unstarted interaction older than the window to be stalled")
	}
	if status.Reason != InteractionStallReasonNoFirstTurn {
		t.Fatalf("stall reason = %q, want %q", status.Reason, InteractionStallReasonNoFirstTurn)
	}
	if status.Age < status.Threshold {
		t.Fatalf("stall age %s must exceed the threshold %s", status.Age, status.Threshold)
	}
	if status.Reported {
		t.Fatal("no stall report must be recorded before one is posted")
	}
}

// TestDetectInteractionStallIgnoresAnsweredInteraction proves the guard only
// fires for a *never started* interaction: once the agent produced a turn after
// the prompt it is not a stall anymore.
func TestDetectInteractionStallIgnoresAnsweredInteraction(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, bot, _ := stallTestChat(t, DB, map[string]interface{}{})

	text := "Working on it."
	reply := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: chat.User1Id,
		DataType:   "text",
		Text:       &text,
		MetaData:   database.JSONRaw(`{"finished":true}`),
	}
	if err := DB.Create(&reply).Error; err != nil {
		t.Fatalf("failed to create the agent reply: %v", err)
	}
	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)

	if status := detectInteractionStall(DB, nil, chat, time.Now()); status.Stalled {
		t.Fatalf("an answered interaction must not be reported as stalled (%+v)", status)
	}
}

// TestDetectInteractionStallIgnoresPendingConfirmation keeps a parked
// confirmation out of the stall path: waiting on the user is a decision, not a
// silent freeze.
func TestDetectInteractionStallIgnoresPendingConfirmation(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, bot, _ := stallTestChat(t, DB, map[string]interface{}{})

	pending := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: chat.User1Id,
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"finished":false,"confirmable_actions":[{"action_id":"a1","status":"pending","title":"Run the tests"}]}`),
	}
	if err := DB.Create(&pending).Error; err != nil {
		t.Fatalf("failed to create the parked confirmation: %v", err)
	}
	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)

	if status := detectInteractionStall(DB, nil, chat, time.Now()); status.Stalled {
		t.Fatalf("a chat waiting on the user must not be reported as stalled (%+v)", status)
	}
}

// TestDetectInteractionStallRespectsWindowAndOptOut pins the two off switches:
// a prompt newer than the window is fine, and a configured zero window disables
// the guard entirely.
func TestDetectInteractionStallRespectsWindowAndOptOut(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, _, _ := stallTestChat(t, DB, map[string]interface{}{})

	// Fresh prompt, default window: not stalled.
	if status := detectInteractionStall(DB, nil, chat, time.Now()); status.Stalled {
		t.Fatalf("a fresh prompt must not be reported as stalled (%+v)", status)
	}

	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)
	// A raised window means the aged prompt is still within it.
	raised, _, _, _ := stallTestChat(t, DB, map[string]interface{}{interactionStallConfigKey: 7200})
	if raised.ID == chat.ID {
		t.Fatal("expected a distinct chat")
	}
	ageMessagesBackdates(t, DB, raised.ID, 30*time.Minute)
	if status := detectInteractionStall(DB, nil, raised, time.Now()); status.Stalled {
		t.Fatalf("a prompt inside a raised window must not be reported as stalled (%+v)", status)
	}

	disabled, _, _, _ := stallTestChat(t, DB, map[string]interface{}{interactionStallConfigKey: 0})
	ageMessagesBackdates(t, DB, disabled.ID, 24*time.Hour)
	if status := detectInteractionStall(DB, nil, disabled, time.Now()); status.Stalled {
		t.Fatalf("a zero window must disable the guard (%+v)", status)
	}
}

// TestReportInteractionStallPostsOneVisibleError is the acceptance half of
// ci#609: the frozen interaction cannot stay silent - it gets one persisted bot
// error that names the stall, and the interaction then resolves as failed
// instead of idle.
func TestReportInteractionStallPostsOneVisibleError(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, bot, _ := stallTestChat(t, DB, map[string]interface{}{})
	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)

	if !ReportInteractionStall(DB, nil, chat) {
		t.Fatal("expected a stalled interaction to be reported")
	}

	latest, err := latestMessageForChat(DB, chat.ID)
	if err != nil {
		t.Fatalf("failed to read the stall report: %v", err)
	}
	if latest.SenderId != bot.ID {
		t.Fatalf("the stall report must be authored by the bot (got sender %d)", latest.SenderId)
	}
	if latest.Text == nil || len(*latest.Text) == 0 {
		t.Fatal("the stall report must carry visible text")
	}
	if !strings.Contains(*latest.Text, InteractionStallReasonNoFirstTurn) {
		t.Fatalf("the stall report must name the reason, got %q", *latest.Text)
	}
	meta := map[string]interface{}{}
	if err := json.Unmarshal(latest.MetaData, &meta); err != nil {
		t.Fatalf("the stall report metadata is not readable: %v", err)
	}
	if finished, _ := meta["finished"].(bool); !finished {
		t.Fatal("the stall report must be marked finished")
	}
	if isError, _ := meta["error"].(bool); !isError {
		t.Fatal("the stall report must be marked as an error")
	}
	if reason, _ := meta["failure_reason"].(string); reason != InteractionStallReasonNoFirstTurn {
		t.Fatalf("failure_reason = %q, want %q", reason, InteractionStallReasonNoFirstTurn)
	}

	// The interaction now resolves as a visible failure with a reason.
	status, err := resolveInteractionStatus(DB, nil, chat)
	if err != nil {
		t.Fatalf("resolveInteractionStatus failed: %v", err)
	}
	if status.State != "failed" {
		t.Fatalf("state = %q, want failed after a stall report", status.State)
	}

	// Idempotent: a second pass must not add another message.
	messagesBefore := countChatMessages(t, DB, chat.ID)
	if ReportInteractionStall(DB, nil, chat) {
		t.Fatal("a stall must only be reported once")
	}
	if messagesAfter := countChatMessages(t, DB, chat.ID); messagesAfter != messagesBefore {
		t.Fatalf("the stall report must be idempotent (%d -> %d messages)", messagesBefore, messagesAfter)
	}
}

// TestReportInteractionStallSkipsHealthyInteraction keeps the guard from
// spamming a chat that is fine or still starting.
func TestReportInteractionStallSkipsHealthyInteraction(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, _, _ := stallTestChat(t, DB, map[string]interface{}{})

	if ReportInteractionStall(DB, nil, chat) {
		t.Fatal("a fresh interaction must not be reported as stalled")
	}
	if got := countChatMessages(t, DB, chat.ID); got != 1 {
		t.Fatalf("expected only the prompt to exist, got %d messages", got)
	}
}

// TestResolveInteractionStatusAnnotatesStall pins the status projection the
// external dispatcher loop and an operator read: the stall fields travel with
// the deterministic status response.
func TestResolveInteractionStatusAnnotatesStall(t *testing.T) {
	t.Setenv(InteractionStallEnvKey, "")
	DB := setupChatsTestDB(t)
	chat, _, _, _ := stallTestChat(t, DB, map[string]interface{}{})
	ageMessagesBackdates(t, DB, chat.ID, 30*time.Minute)

	status, err := resolveInteractionStatus(DB, nil, chat)
	if err != nil {
		t.Fatalf("resolveInteractionStatus failed: %v", err)
	}
	if !status.Stalled || status.StallReason != InteractionStallReasonNoFirstTurn {
		t.Fatalf("status = %+v, want a stalled annotation", status)
	}
	if status.StallAgeSeconds <= 0 || status.StallThresholdSeconds <= 0 {
		t.Fatalf("stall age/threshold must be reported, got %+v", status)
	}
	// The stall is layered on top of the deterministic idle state, so existing
	// consumers keep working.
	if status.State != "idle" || status.IsActive {
		t.Fatalf("state = %q active=%v, want an inactive idle state", status.State, status.IsActive)
	}
	if status.LatestMessageFinished == nil || *status.LatestMessageFinished {
		t.Fatalf("the unanswered prompt must not be reported as finished: %v", status.LatestMessageFinished)
	}
}

func countChatMessages(t *testing.T, DB *gorm.DB, chatID uint) int64 {
	t.Helper()
	var count int64
	if err := DB.Model(&database.Message{}).Where("chat_id = ?", chatID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count messages: %v", err)
	}
	return count
}
