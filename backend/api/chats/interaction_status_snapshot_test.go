package chats

import (
	"testing"

	"backend/database"
	"backend/workqueue"
)

// TestResolveInteractionStatusesWithSnapshotAligns pins the ci#850 batch path:
// resolving many chats with one BotReplyActivity snapshot must return a status
// per input chat, in the same order, so the agent-interaction listing can use
// it positionally instead of one inspector round-trip per chat.
func TestResolveInteractionStatusesWithSnapshotAligns(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "snapshot-owner@example.com", false)
	bot := createBotUserForChatsTest(t, DB, "snapshot-bot@example.com")

	chats := []database.Chat{
		{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"},
		{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"},
	}
	for i := range chats {
		if err := DB.Create(&chats[i]).Error; err != nil {
			t.Fatalf("failed to create chat %d: %v", i, err)
		}
	}

	statuses := ResolveInteractionStatusesWithSnapshot(DB, workqueue.SnapshotBotReplyActivity(nil), chats)
	if len(statuses) != len(chats) {
		t.Fatalf("expected %d statuses, got %d", len(chats), len(statuses))
	}
	for i := range chats {
		if statuses[i].ChatUUID != chats[i].UUID {
			t.Fatalf("status %d is for %q, want %q", i, statuses[i].ChatUUID, chats[i].UUID)
		}
		if statuses[i].State == "" {
			t.Fatalf("status %d has an empty state", i)
		}
	}
}
