package chats

import (
	"backend/database"
	"testing"
)

// TestSetChatPinRepinAfterUnpin guards ci#361: ChatPin embeds the soft-deletable
// Model while its unique index covers only (chat_id, user_id), so a soft-deleted
// row from a previous unpin used to block the re-pin and stay invisible to
// loadChatPins — leaving the chat silently unpinned.
func TestSetChatPinRepinAfterUnpin(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "pin-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "pin-bot@example.com", false)

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if err := setChatPin(DB, chat.ID, owner.ID, true); err != nil {
		t.Fatalf("first pin: %v", err)
	}
	if !loadChatPins(DB, owner.ID, []uint{chat.ID})[chat.ID] {
		t.Fatal("expected chat to be pinned after the first pin")
	}

	if err := setChatPin(DB, chat.ID, owner.ID, false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if loadChatPins(DB, owner.ID, []uint{chat.ID})[chat.ID] {
		t.Fatal("expected chat to be unpinned after unpin")
	}

	if err := setChatPin(DB, chat.ID, owner.ID, true); err != nil {
		t.Fatalf("re-pin after unpin: %v", err)
	}
	if !loadChatPins(DB, owner.ID, []uint{chat.ID})[chat.ID] {
		t.Fatal("expected chat to be pinned again after re-pin (regression: soft-deleted row blocked it)")
	}
}

// TestSetChatPinResurrectsLegacySoftDeletedRow guards that a pre-existing
// soft-deleted row (created before the hard-delete fix) is still handled: the
// re-pin must un-delete it rather than insert a conflicting duplicate.
func TestSetChatPinResurrectsLegacySoftDeletedRow(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "legacy-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "legacy-bot@example.com", false)

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "conversation"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	// Simulate the legacy state: a soft-deleted pin row.
	legacy := database.ChatPin{ChatId: chat.ID, UserId: owner.ID}
	if err := DB.Create(&legacy).Error; err != nil {
		t.Fatalf("failed to seed legacy pin: %v", err)
	}
	if err := DB.Delete(&database.ChatPin{}, legacy.ID).Error; err != nil {
		t.Fatalf("failed to soft-delete legacy pin: %v", err)
	}
	var softDeleted int64
	if err := DB.Unscoped().Model(&database.ChatPin{}).
		Where("id = ? AND deleted_at IS NOT NULL", legacy.ID).
		Count(&softDeleted).Error; err != nil || softDeleted != 1 {
		t.Fatalf("expected a soft-deleted legacy row, got %d (err %v)", softDeleted, err)
	}

	if err := setChatPin(DB, chat.ID, owner.ID, true); err != nil {
		t.Fatalf("re-pin over legacy soft-deleted row: %v", err)
	}
	if !loadChatPins(DB, owner.ID, []uint{chat.ID})[chat.ID] {
		t.Fatal("expected the legacy soft-deleted pin to be resurrected")
	}
}
