package chats

import (
	"encoding/json"
	"testing"

	"backend/chatstate"
	"backend/database"
)

func TestMessageHasPendingConfirmationRecognizesOpencodeNeedsAction(t *testing.T) {
	pending := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_needs_action":{"status":"pending","reason":"ApiError","message":"invalid api key"}}`),
	}
	if !messageHasPendingConfirmation(pending) {
		t.Fatalf("expected a pending opencode_needs_action marker to count as a pending confirmation")
	}

	resolved := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_needs_action":{"status":"resolved"}}`),
	}
	if messageHasPendingConfirmation(resolved) {
		t.Fatalf("expected a resolved opencode_needs_action marker to be ignored")
	}

	absent := database.Message{
		MetaData: database.JSONRaw(`{"finished":true}`),
	}
	if messageHasPendingConfirmation(absent) {
		t.Fatalf("expected a message without markers to be ignored")
	}
}

func TestMessageHasPendingConfirmationRecognizesOpencodeQuestion(t *testing.T) {
	pending := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_question":{"status":"pending","id":"que_1","title":"OpenCode needs your input","description":"Which scope?"}}`),
	}
	if !messageHasPendingConfirmation(pending) {
		t.Fatalf("expected a pending opencode_question marker to count as a pending confirmation")
	}

	answered := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_question":{"status":"answered","id":"que_1"}}`),
	}
	if messageHasPendingConfirmation(answered) {
		t.Fatalf("expected an answered opencode_question marker to be ignored")
	}

	cancelled := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"opencode_question":{"status":"cancelled","id":"que_1"}}`),
	}
	if messageHasPendingConfirmation(cancelled) {
		t.Fatalf("expected a cancelled opencode_question marker to be ignored")
	}
}

func TestResolveInteractionStatusPendingQuestionBeatsRunningBackend(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "status-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "status-bot@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	shared := database.SharedChatConfig{
		ChatId:     chat.ID,
		ConfigData: json.RawMessage(`{"chat_backend":"status-test-backend"}`),
	}
	if err := DB.Create(&shared).Error; err != nil {
		t.Fatalf("failed to create shared config: %v", err)
	}
	if err := DB.Model(&chat).Update("shared_config_id", shared.ID).Error; err != nil {
		t.Fatalf("failed to link shared config: %v", err)
	}

	text := "Should I plan for option A or option B?"
	msg := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		Text:       &text,
		MetaData:   database.JSONRaw(`{"finished":true,"opencode_question":{"status":"pending","id":"que_1"}}`),
	}
	if err := DB.Create(&msg).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	// Simulate the live OpenCode session still reporting "running" while the
	// ask-only question is pending: the pending user action must win over the
	// backend-running short-circuit so the chat shows needs_confirmation.
	chatstate.RegisterBackendStateProvider("status-test-backend", func(string) (chatstate.BackendState, bool) {
		return chatstate.BackendStateRunning, true
	})

	status, err := resolveInteractionStatus(DB, nil, chat)
	if err != nil {
		t.Fatalf("resolveInteractionStatus failed: %v", err)
	}
	if status.State != string(chatstate.StateNeedsConfirmation) {
		t.Fatalf("expected needs_confirmation, got state=%q source=%q active=%v", status.State, status.Source, status.IsActive)
	}
	if status.IsActive {
		t.Fatalf("expected IsActive=false while waiting on the user")
	}
}

func TestMessageHasPendingConfirmationRecognizesInteractionConfirmation(t *testing.T) {
	pending := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"pending","source_message_uuid":"msg-1"}}`),
	}
	if !messageHasPendingConfirmation(pending) {
		t.Fatalf("expected a pending interaction_confirmation to count as a pending confirmation")
	}

	approved := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"approved"}}`),
	}
	if messageHasPendingConfirmation(approved) {
		t.Fatalf("expected an approved interaction_confirmation to be ignored")
	}

	rejected := database.Message{
		MetaData: database.JSONRaw(`{"finished":true,"interaction_confirmation":{"status":"rejected"}}`),
	}
	if messageHasPendingConfirmation(rejected) {
		t.Fatalf("expected a rejected interaction_confirmation to be ignored")
	}
}
