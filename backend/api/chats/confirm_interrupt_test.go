package chats

import (
	"encoding/json"
	"testing"

	"backend/database"
)

// When the user stops a native bot generation, the bot reply task has usually
// already completed (it pauses on the first confirmable tool call), so the
// asynq cancellation is a no-op. The interrupt must also void the parked
// proposal, otherwise it stays executable after the user asked to stop.
func TestCancelPendingConfirmableActionForChatVoidsParkedAction(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "interrupt-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "interrupt-bot@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	toolCalls := []json.RawMessage{
		json.RawMessage(`{"id":"call_1","name":"coder_bash","status":"pending_confirmation"}`),
	}
	meta := database.JSONRaw(`{"finished":true,"confirmable_actions":[{"action_id":"call_1","target_tool_name":"coder_bash","status":"pending"}]}`)
	msg := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		ToolCalls:  &toolCalls,
		MetaData:   meta,
	}
	if err := DB.Create(&msg).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	cancelled, err := cancelPendingConfirmableActionsForChat(DB, chat.ID, owner.UUID)
	if err != nil {
		t.Fatalf("cancelPendingConfirmableActionsForChat failed: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("expected 1 cancelled action, got %d", cancelled)
	}

	var reloaded database.Message
	if err := DB.First(&reloaded, msg.ID).Error; err != nil {
		t.Fatalf("failed to reload message: %v", err)
	}

	reloadedMeta := map[string]interface{}{}
	if err := json.Unmarshal(reloaded.MetaData, &reloadedMeta); err != nil {
		t.Fatalf("failed to parse reloaded metadata: %v", err)
	}
	actions := reloadedMeta["confirmable_actions"].([]interface{})
	action := actions[0].(map[string]interface{})
	if status, _ := action["status"].(string); status != "cancelled" {
		t.Fatalf("expected action status cancelled, got %q", action["status"])
	}
	if by, _ := action["cancelled_by"].(string); by != owner.UUID {
		t.Fatalf("expected cancelled_by %q, got %q", owner.UUID, action["cancelled_by"])
	}

	if reloaded.ToolCalls == nil || len(*reloaded.ToolCalls) != 1 {
		t.Fatalf("expected the tool call to be preserved")
	}
	toolCall := map[string]interface{}{}
	if err := json.Unmarshal((*reloaded.ToolCalls)[0], &toolCall); err != nil {
		t.Fatalf("failed to parse reloaded tool call: %v", err)
	}
	if status, _ := toolCall["status"].(string); status != "cancelled" {
		t.Fatalf("expected tool call status cancelled, got %q", toolCall["status"])
	}

	// A second interrupt (or a retried signal) must not double-count.
	again, err := cancelPendingConfirmableActionsForChat(DB, chat.ID, owner.UUID)
	if err != nil {
		t.Fatalf("second cancel failed: %v", err)
	}
	if again != 0 {
		t.Fatalf("expected no further cancellations, got %d", again)
	}
}

// A parked action the user already rejected/executed must be left untouched.
func TestCancelPendingConfirmableActionForChatLeavesResolvedActions(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "resolved-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "resolved-bot@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	msg := database.Message{
		ChatId:     chat.ID,
		SenderId:   bot.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"finished":true,"confirmable_actions":[{"action_id":"call_1","target_tool_name":"coder_bash","status":"executed"}]}`),
	}
	if err := DB.Create(&msg).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	cancelled, err := cancelPendingConfirmableActionsForChat(DB, chat.ID, owner.UUID)
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("expected an already-executed action to be untouched, got %d", cancelled)
	}
}
