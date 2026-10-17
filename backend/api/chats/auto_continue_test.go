package chats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

func TestResolveAutoContinueConfigDefaults(t *testing.T) {
	cfg := resolveAutoContinueConfig(nil)
	if cfg.Enabled {
		t.Fatal("auto-continue must default to disabled")
	}
	if cfg.Max != autoContinueDefaultMax {
		t.Fatalf("max = %d, want %d", cfg.Max, autoContinueDefaultMax)
	}
	if cfg.Delay != autoContinueDefaultDelaySeconds*time.Second {
		t.Fatalf("delay = %s, want %ds", cfg.Delay, autoContinueDefaultDelaySeconds)
	}
	if cfg.CompletionMarker != autoContinueDefaultCompletionMarker {
		t.Fatalf("completion marker = %q, want %q", cfg.CompletionMarker, autoContinueDefaultCompletionMarker)
	}
	if cfg.HumanMarker != autoContinueDefaultHumanMarker {
		t.Fatalf("human marker = %q, want %q", cfg.HumanMarker, autoContinueDefaultHumanMarker)
	}
	if cfg.Prompt == "" {
		t.Fatal("default prompt must be set")
	}
}

func TestResolveAutoContinueConfigFromSharedConfig(t *testing.T) {
	cfg := resolveAutoContinueConfig(map[string]interface{}{
		autoContinueConfigKey:      true,
		autoContinueMaxKey:         3,
		autoContinueDelayKey:       12,
		autoContinuePromptKey:      "keep going",
		autoContinueCompletionKey:  "DONE",
		autoContinueHumanMarkerKey: "HELP",
	})
	if !cfg.Enabled {
		t.Fatal("expected auto-continue enabled")
	}
	if cfg.Max != 3 {
		t.Fatalf("max = %d, want 3", cfg.Max)
	}
	if cfg.Delay != 12*time.Second {
		t.Fatalf("delay = %s, want 12s", cfg.Delay)
	}
	if cfg.Prompt != "keep going" {
		t.Fatalf("prompt = %q, want keep going", cfg.Prompt)
	}
	if cfg.CompletionMarker != "DONE" || cfg.HumanMarker != "HELP" {
		t.Fatalf("markers = %q/%q, want DONE/HELP", cfg.CompletionMarker, cfg.HumanMarker)
	}
}

func TestResolveAutoContinueConfigFromDispatch(t *testing.T) {
	cfg := resolveAutoContinueConfig(map[string]interface{}{
		"agent_dispatch": map[string]interface{}{
			"auto_continue":     true,
			"completion_marker": "FIN",
			"human_marker":      "HUMAN",
		},
	})
	if !cfg.Enabled {
		t.Fatal("a dispatched auto_continue must enable the loop")
	}
	if cfg.CompletionMarker != "FIN" {
		t.Fatalf("completion marker = %q, want FIN", cfg.CompletionMarker)
	}
	if cfg.HumanMarker != "HUMAN" {
		t.Fatalf("human marker = %q, want HUMAN", cfg.HumanMarker)
	}
}

func TestShouldAutoContinue(t *testing.T) {
	enabled := autoContinueConfig{
		Enabled:          true,
		Max:              3,
		CompletionMarker: "<<TASK_COMPLETE>>",
		HumanMarker:      "<<NEED_HUMAN>>",
	}
	cases := []struct {
		name         string
		cfg          autoContinueConfig
		completed    bool
		pending      bool
		count        int
		text         string
		wantContinue bool
	}{
		{name: "continues on a clean completion", cfg: enabled, completed: true, count: 0, wantContinue: true},
		{name: "disabled never continues", cfg: autoContinueConfig{Enabled: false, Max: 3}, completed: true, wantContinue: false},
		{name: "incomplete turn never continues", cfg: enabled, completed: false, wantContinue: false},
		{name: "pending actions stop the loop", cfg: enabled, completed: true, pending: true, wantContinue: false},
		{name: "budget exhausted stops the loop", cfg: enabled, completed: true, count: 3, wantContinue: false},
		{name: "completion marker stops the loop", cfg: enabled, completed: true, text: "all done <<TASK_COMPLETE>>", wantContinue: false},
		{name: "human marker stops the loop", cfg: enabled, completed: true, text: "blocked <<NEED_HUMAN>>", wantContinue: false},
		{name: "count below budget continues", cfg: enabled, completed: true, count: 2, wantContinue: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoContinue(tc.cfg, tc.completed, tc.pending, tc.count, tc.text); got != tc.wantContinue {
				t.Fatalf("shouldAutoContinue = %v, want %v", got, tc.wantContinue)
			}
		})
	}
}

func autoContinueTestChat(t *testing.T, DB *gorm.DB, config map[string]interface{}) (database.Chat, database.User, database.User) {
	t.Helper()
	owner := createUserForChatsTest(t, DB, "ac-owner-"+shortSuffix(), false)
	bot := createUserForChatsTest(t, DB, "ac-bot-"+shortSuffix(), false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("mark bot automated: %v", err)
	}
	encoded, _ := json.Marshal(config)
	shared := database.SharedChatConfig{ConfigData: encoded}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("create shared config: %v", err)
	}
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction", SharedConfigId: &shared.ID}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	return chat, *owner, *bot
}

func shortSuffix() string {
	return time.Now().Format("150405.000000")
}

// TestScheduleAutoContinuePostsContinuationMessage verifies the in-product loop
// end-to-end short of the queue: an opted-in interaction's clean completion
// posts the continuation prompt as the owner (the enqueue then fails only
// because the test passes no asynq client).
func TestScheduleAutoContinuePostsContinuationMessage(t *testing.T) {
	DB := setupChatsTestDB(t)
	chat, owner, bot := autoContinueTestChat(t, DB, map[string]interface{}{
		autoContinueConfigKey: true,
		autoContinuePromptKey: "keep going please",
	})
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		Text:       stringPtr("working on it"),
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"finished":true}`),
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}

	scheduleAutoContinue(context.Background(), DB, nil, nil, chat, bot, owner, message)

	var continuation database.Message
	if err := DB.Where("chat_id = ? AND sender_id = ? AND receiver_id = ?", chat.ID, owner.ID, bot.ID).
		Order("created_at desc").First(&continuation).Error; err != nil {
		t.Fatalf("expected a continuation message: %v", err)
	}
	if continuation.Text == nil || *continuation.Text != "keep going please" {
		t.Fatalf("continuation text = %v, want the configured prompt", continuation.Text)
	}
}

// TestScheduleAutoContinueStopsOnCompletionMarker verifies the loop stops when
// the agent signals completion.
func TestScheduleAutoContinueStopsOnCompletionMarker(t *testing.T) {
	DB := setupChatsTestDB(t)
	chat, owner, bot := autoContinueTestChat(t, DB, map[string]interface{}{
		autoContinueConfigKey: true,
	})
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		Text:       stringPtr("all done <<TASK_COMPLETE>>"),
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"finished":true}`),
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}

	scheduleAutoContinue(context.Background(), DB, nil, nil, chat, bot, owner, message)

	var count int64
	DB.Model(&database.Message{}).Where("chat_id = ? AND sender_id = ?", chat.ID, owner.ID).Count(&count)
	if count != 0 {
		t.Fatalf("a completion marker must not post a continuation, got %d owner messages", count)
	}
}

func TestAutoContinueCountPersistsAndResets(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ac-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "ac-bot@example.com", false)
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}

	if got := loadAutoContinueCount(DB, chat.ID); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}
	incrementAutoContinueCount(DB, chat.ID)
	incrementAutoContinueCount(DB, chat.ID)
	if got := loadAutoContinueCount(DB, chat.ID); got != 2 {
		t.Fatalf("count after two increments = %d, want 2", got)
	}
	resetAutoContinueCount(DB, chat.ID)
	if got := loadAutoContinueCount(DB, chat.ID); got != 0 {
		t.Fatalf("count after reset = %d, want 0", got)
	}
}

func TestWriteAutoContinueCountPreservesOtherSettings(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ac-owner2@example.com", false)
	bot := createUserForChatsTest(t, DB, "ac-bot2@example.com", false)
	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := DB.Create(&database.ChatSettings{ChatId: chat.ID, ConfigData: json.RawMessage(`{"title":"My chat"}`)}).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	incrementAutoContinueCount(DB, chat.ID)

	settings := database.ChatSettings{}
	if err := DB.Where("chat_id = ?", chat.ID).First(&settings).Error; err != nil {
		t.Fatalf("load settings: %v", err)
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(settings.ConfigData, &config); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if config["title"] != "My chat" {
		t.Fatalf("title was clobbered: %#v", config)
	}
	if value, _ := jsonInt(config[autoContinueCountSettingKey]); value != 1 {
		t.Fatalf("count = %v, want 1", config[autoContinueCountSettingKey])
	}
}
