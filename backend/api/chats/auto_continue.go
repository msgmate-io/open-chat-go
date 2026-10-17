package chats

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"backend/database"
	"backend/server/util"
	"backend/workqueue"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// This file implements the native, in-product auto-continuation loop (ci#537):
// after an automated agent's clean final reply is persisted for an interaction,
// the core can schedule the next turn itself, so a self-driving agent keeps
// working without an external re-prompt (the GitHub dispatcher loop covers
// dispatched runs; this is the "open-chat communication layer" half).
//
// It is per-interaction opt-in: the chat's shared config carries `auto_continue`
// (also accepted from the trusted dispatch surface under
// `agent_dispatch.auto_continue`), a budget (`auto_continue_max`), a delay
// (`auto_continue_delay_seconds`), the continuation prompt and the completion /
// human markers that stop the loop. The per-chat count is stored in the chat's
// server-side ChatSettings, reset whenever a human sends a manual message.

const (
	// Shared-config keys that configure the loop.
	autoContinueConfigKey      = "auto_continue"
	autoContinueMaxKey         = "auto_continue_max"
	autoContinueDelayKey       = "auto_continue_delay_seconds"
	autoContinuePromptKey      = "auto_continue_prompt"
	autoContinueCompletionKey  = "auto_continue_completion_marker"
	autoContinueHumanMarkerKey = "auto_continue_human_marker"

	// agent_dispatch sub-keys recorded by the trusted /api/v1/agents/dispatch
	// surface; used as fallbacks so a dispatched agent's loop options apply.
	agentDispatchAutoContinueKey = "auto_continue"
	agentDispatchCompletionKey   = "completion_marker"
	agentDispatchHumanMarkerKey  = "human_marker"

	// autoContinueCountSettingKey is the ChatSettings.ConfigData key holding the
	// per-chat continuation count. ChatSettings is server-side only (the API
	// exposes just the title), so this never leaks to the user.
	autoContinueCountSettingKey = "auto_continue_count"

	// Defaults.
	autoContinueDefaultMax              = 25
	autoContinueDefaultDelaySeconds     = 5
	autoContinueDefaultCompletionMarker = "<<TASK_COMPLETE>>"
	autoContinueDefaultHumanMarker      = "<<NEED_HUMAN>>"
	autoContinueDefaultPrompt           = "Continue the task. Do not stop to ask whether to continue; pick the next concrete step and make real progress now. " +
		"End your reply with <<TASK_COMPLETE>> on its own line when the whole task is complete and verified, " +
		"or <<NEED_HUMAN>> on its own line if you are blocked on a decision only a human can make."
)

// autoContinueConfig is the resolved per-interaction loop configuration.
type autoContinueConfig struct {
	Enabled          bool
	Max              int
	Delay            time.Duration
	Prompt           string
	CompletionMarker string
	HumanMarker      string
}

// resolveAutoContinueConfig reads the loop options from a chat's shared config,
// falling back to the trusted dispatch surface's `agent_dispatch` options and
// then to the defaults. It never fails: a missing or malformed config yields the
// disabled default.
func resolveAutoContinueConfig(config map[string]interface{}) autoContinueConfig {
	cfg := autoContinueConfig{
		Max:              autoContinueDefaultMax,
		Delay:            autoContinueDefaultDelaySeconds * time.Second,
		Prompt:           autoContinueDefaultPrompt,
		CompletionMarker: autoContinueDefaultCompletionMarker,
		HumanMarker:      autoContinueDefaultHumanMarker,
	}
	if config == nil {
		return cfg
	}
	var dispatch map[string]interface{}
	if raw, ok := config["agent_dispatch"].(map[string]interface{}); ok {
		dispatch = raw
	}

	if enabled, ok := config[autoContinueConfigKey].(bool); ok {
		cfg.Enabled = enabled
	} else if dispatch != nil {
		if enabled, ok := dispatch[agentDispatchAutoContinueKey].(bool); ok {
			cfg.Enabled = enabled
		}
	}

	if value, ok := jsonInt(config[autoContinueMaxKey]); ok && value > 0 {
		cfg.Max = value
	}
	if value, ok := jsonInt(config[autoContinueDelayKey]); ok && value >= 0 {
		cfg.Delay = time.Duration(value) * time.Second
	}
	if value := strings.TrimSpace(jsonString(config[autoContinuePromptKey])); value != "" {
		cfg.Prompt = value
	}
	if value := strings.TrimSpace(jsonString(config[autoContinueCompletionKey])); value != "" {
		cfg.CompletionMarker = value
	} else if dispatch != nil {
		if value := strings.TrimSpace(jsonString(dispatch[agentDispatchCompletionKey])); value != "" {
			cfg.CompletionMarker = value
		}
	}
	if value := strings.TrimSpace(jsonString(config[autoContinueHumanMarkerKey])); value != "" {
		cfg.HumanMarker = value
	} else if dispatch != nil {
		if value := strings.TrimSpace(jsonString(dispatch[agentDispatchHumanMarkerKey])); value != "" {
			cfg.HumanMarker = value
		}
	}
	return cfg
}

// shouldAutoContinue is the pure continue/stop decision. It continues only when
// the loop is enabled, the turn is a clean completion with no pending actions,
// the budget is not exhausted, and the agent did not signal completion or a need
// for a human.
func shouldAutoContinue(cfg autoContinueConfig, completed, hasPendingActions bool, count int, lastText string) bool {
	if !cfg.Enabled || !completed || hasPendingActions {
		return false
	}
	if count >= cfg.Max {
		return false
	}
	if markerPresent(lastText, cfg.CompletionMarker) || markerPresent(lastText, cfg.HumanMarker) {
		return false
	}
	return true
}

// markerPresent reports whether text contains marker (trimmed, case-insensitive
// on the marker's own casing is not required: markers are exact sentinels).
func markerPresent(text, marker string) bool {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return false
	}
	return strings.Contains(text, marker)
}

// maybeScheduleAutoContinueForMessage is the MessageSend hook. It runs for every
// persisted message and schedules the next turn in the background when the
// message is an automated agent's clean completion of an opted-in interaction.
// It fetches the queue handles synchronously (before the request context is
// released) and does the work detached so it never delays message persistence.
func maybeScheduleAutoContinueForMessage(r *http.Request, db *gorm.DB, chat database.Chat, sender, receiver database.User, message database.Message) {
	if db == nil || r == nil {
		return
	}
	if !sender.IsAutomated || receiver.IsAutomated || receiver.ID == 0 {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(chat.ChatType), "interaction") {
		return
	}
	client, clientErr := util.GetAsynqClient(r)
	if clientErr != nil {
		return
	}
	inspector, inspectorErr := util.GetAsynqInspector(r)
	if inspectorErr != nil {
		return
	}
	go scheduleAutoContinue(context.Background(), db, client, inspector, chat, sender, receiver, message)
}

// scheduleAutoContinue resolves the loop config, decides whether to continue and
// posts the continuation prompt as the owner, enqueueing the bot reply after the
// configured delay. Best-effort: every failure is logged and never affects the
// completed message.
func scheduleAutoContinue(ctx context.Context, db *gorm.DB, client *asynq.Client, inspector *asynq.Inspector, chat database.Chat, bot, owner database.User, message database.Message) {
	meta, ok := decodeMessageMetadata(message.MetaData)
	if !ok || !isInteractionCompletionMetadata(message, meta) {
		return
	}
	// A `task_completed` flag is an explicit completion signal a tool can set;
	// it stops the loop even without a text sentinel.
	if taskCompleted, _ := meta["task_completed"].(bool); taskCompleted {
		return
	}
	hasPendingActions := len(pendingActionsForMessage(message)) > 0

	config := chatSharedConfigData(db, chat)
	cfg := resolveAutoContinueConfig(config)
	if !cfg.Enabled {
		return
	}
	count := loadAutoContinueCount(db, chat.ID)
	if !shouldAutoContinue(cfg, true, hasPendingActions, count, messageText(message)) {
		return
	}
	if owner.ID == 0 || bot.ID == 0 {
		return
	}

	continuation := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: bot.ID,
		Text:       stringPtr(cfg.Prompt),
		DataType:   "text",
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&continuation).Error; err != nil {
			return err
		}
		return tx.Model(&database.Chat{}).Where("id = ?", chat.ID).Update("latest_message_id", continuation.ID).Error
	}); err != nil {
		log.Printf("auto-continue: failed to post continuation for chat %s: %v", chat.UUID, err)
		return
	}

	if _, err := workqueue.EnqueueBotReply(client, inspector, workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: continuation.UUID,
		BotUserID:   bot.ID,
	}, asynq.ProcessIn(cfg.Delay)); err != nil {
		log.Printf("auto-continue: failed to enqueue continuation reply for chat %s: %v", chat.UUID, err)
		return
	}
	incrementAutoContinueCount(db, chat.ID)
}

// chatSharedConfigData decodes a chat's persisted shared config (empty when
// none).
func chatSharedConfigData(db *gorm.DB, chat database.Chat) map[string]interface{} {
	config := map[string]interface{}{}
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		_ = json.Unmarshal(chat.SharedConfig.ConfigData, &config)
		return config
	}
	if db != nil && chat.SharedConfigId != nil && *chat.SharedConfigId != 0 {
		shared := database.SharedChatConfig{}
		if err := db.First(&shared, *chat.SharedConfigId).Error; err == nil && len(shared.ConfigData) > 0 {
			_ = json.Unmarshal(shared.ConfigData, &config)
		}
	}
	return config
}

func messageText(message database.Message) string {
	if message.Text == nil {
		return ""
	}
	return *message.Text
}

func stringPtr(value string) *string {
	return &value
}

// loadAutoContinueCount reads the per-chat continuation count from the
// server-side chat settings.
func loadAutoContinueCount(db *gorm.DB, chatID uint) int {
	if db == nil || chatID == 0 {
		return 0
	}
	settings := database.ChatSettings{}
	if err := db.Where("chat_id = ?", chatID).First(&settings).Error; err != nil {
		return 0
	}
	config := map[string]interface{}{}
	if len(settings.ConfigData) > 0 {
		_ = json.Unmarshal(settings.ConfigData, &config)
	}
	value, _ := jsonInt(config[autoContinueCountSettingKey])
	if value < 0 {
		return 0
	}
	return value
}

// incrementAutoContinueCount bumps and persists the per-chat continuation count.
// The read-modify-write is not transactional across processes, but the loop is
// single-writer per chat in practice (a chat has at most one live generation).
func incrementAutoContinueCount(db *gorm.DB, chatID uint) {
	if db == nil || chatID == 0 {
		return
	}
	writeAutoContinueCount(db, chatID, loadAutoContinueCount(db, chatID)+1)
}

// resetAutoContinueCount clears the per-chat continuation budget, so a fresh
// human prompt restarts the loop with a full budget. It is a no-op when no
// budget was ever recorded, so ordinary chats never grow a settings row.
func resetAutoContinueCount(db *gorm.DB, chatID uint) {
	if db == nil || chatID == 0 {
		return
	}
	if loadAutoContinueCount(db, chatID) == 0 {
		return
	}
	writeAutoContinueCount(db, chatID, 0)
}

// writeAutoContinueCount upserts the count into the chat's server-side settings,
// preserving any other keys (e.g. the chat title).
func writeAutoContinueCount(db *gorm.DB, chatID uint, count int) {
	settings := database.ChatSettings{}
	findErr := db.Where("chat_id = ?", chatID).First(&settings).Error
	if findErr != nil && findErr != gorm.ErrRecordNotFound {
		return
	}
	config := map[string]interface{}{}
	if findErr == nil && len(settings.ConfigData) > 0 {
		_ = json.Unmarshal(settings.ConfigData, &config)
	}
	config[autoContinueCountSettingKey] = count
	encoded, err := json.Marshal(config)
	if err != nil {
		return
	}
	if findErr == gorm.ErrRecordNotFound {
		_ = db.Create(&database.ChatSettings{ChatId: chatID, ConfigData: encoded}).Error
		return
	}
	_ = db.Model(&settings).Update("config_data", encoded).Error
}

// jsonInt coerces a JSON-decoded number to int.
func jsonInt(raw interface{}) (int, bool) {
	switch typed := raw.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case json.Number:
		if value, err := typed.Int64(); err == nil {
			return int(value), true
		}
	}
	return 0, false
}

// jsonString coerces a JSON-decoded value to a string.
func jsonString(raw interface{}) string {
	value, _ := raw.(string)
	return value
}
