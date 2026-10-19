package chats

import (
	"encoding/json"
	"testing"

	"backend/database"
)

// The transcript renders a message as pending while `meta_data.queue_state` is
// "queued", so every path that drops a chat's queued replies must drain the
// marker - otherwise the message stays pending forever.
func TestClearQueuedMarkersForChatDrainsMarkedMessagesOfTheChat(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "queued-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "queued-bot@example.com", false)
	bot.IsAutomated = true
	if err := DB.Save(bot).Error; err != nil {
		t.Fatalf("failed to mark bot automated: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	queued := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: bot.ID,
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"queue_state":"queued"}`),
	}
	plain := database.Message{
		ChatId:     chat.ID,
		SenderId:   owner.ID,
		ReceiverId: bot.ID,
		DataType:   "text",
		MetaData:   database.JSONRaw(`{"other":"value"}`),
	}
	for _, msg := range []*database.Message{&queued, &plain} {
		if err := DB.Create(msg).Error; err != nil {
			t.Fatalf("failed to create message: %v", err)
		}
	}

	ClearQueuedMarkersForChat(DB, chat.ID, chat.UUID)

	var reloaded database.Message
	if err := DB.First(&reloaded, queued.ID).Error; err != nil {
		t.Fatalf("failed to reload queued message: %v", err)
	}
	meta := map[string]interface{}{}
	if err := json.Unmarshal(reloaded.MetaData, &meta); err != nil {
		t.Fatalf("failed to parse metadata: %v", err)
	}
	if _, present := meta["queue_state"]; present {
		t.Fatalf("expected the queued marker to be drained, got %v", meta)
	}

	// A message that never carried the marker keeps its metadata untouched.
	var untouched database.Message
	if err := DB.First(&untouched, plain.ID).Error; err != nil {
		t.Fatalf("failed to reload plain message: %v", err)
	}
	plainMeta := map[string]interface{}{}
	if err := json.Unmarshal(untouched.MetaData, &plainMeta); err != nil {
		t.Fatalf("failed to parse plain metadata: %v", err)
	}
	if plainMeta["other"] != "value" {
		t.Fatalf("expected unrelated metadata to survive, got %v", plainMeta)
	}
}

// The drain must stay a no-op on an unknown chat instead of failing the caller:
// it runs inside interrupt/stop handlers that already did the real work.
func TestClearQueuedMarkersForChatIsSafeForUnknownChat(t *testing.T) {
	DB := setupChatsTestDB(t)
	ClearQueuedMarkersForChat(DB, 4242, "00000000-0000-0000-0000-000000000000")
}
