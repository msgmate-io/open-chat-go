package chats

import (
	"testing"

	"backend/database"
)

// ci#865: POST /bots/{identifier}/interactions stores the interaction chat
// owner-scoped (the participant is the *acting user*, ie the owner of the bot
// runtime), while the chat read/follow routes scoped by the raw session
// principal. A bot session that had just created a dispatched interaction
// therefore could not read or follow it back and the dispatched run never
// started. The scope helpers now additionally scope by the resolved owner.
func TestScopedChatQueryResolvesActingUserForBotSession(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-owner@example.com", false)
	botUser := createUserForChatsTest(t, DB, "ci865-bot@example.com", false)
	stranger := createUserForChatsTest(t, DB, "ci865-stranger@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	runtime := database.BotRuntimeConfig{
		Name:        "ci865-bot",
		BotUserId:   botUser.ID,
		OwnerUserId: owner.ID,
		IsActive:    true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime config: %v", err)
	}

	chat := database.Chat{User1Id: owner.ID, User2Id: botUser.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create interaction chat: %v", err)
	}

	if _, err := findAccessibleChat(DB, botUser, chat.UUID); err != nil {
		t.Fatalf("bot session that created the interaction must be able to read it: %v", err)
	}

	if _, err := findAccessibleChat(DB, owner, chat.UUID); err != nil {
		t.Fatalf("owner must still be able to read the interaction chat: %v", err)
	}

	if _, err := findAccessibleChat(DB, stranger, chat.UUID); err == nil {
		t.Fatalf("unrelated user must not be able to read the interaction chat")
	}
}

// A bot session without an active runtime config keeps exactly its own scope:
// the acting-user resolution must not widen authorisation for an unrelated bot.
func TestScopedChatQueryDoesNotWidenWithoutActiveRuntimeConfig(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ci865-inactive-owner@example.com", false)
	botUser := createUserForChatsTest(t, DB, "ci865-inactive-bot@example.com", false)

	botUser.IsAutomated = true
	if err := DB.Save(botUser).Error; err != nil {
		t.Fatalf("failed to mark bot user automated: %v", err)
	}

	runtime := database.BotRuntimeConfig{
		Name:        "ci865-inactive-bot",
		BotUserId:   botUser.ID,
		OwnerUserId: owner.ID,
		IsActive:    false,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime config: %v", err)
	}
	// The column carries `default:true`, so a zero-value bool is omitted on
	// insert; deactivate explicitly to model a disabled runtime config.
	if err := DB.Model(&runtime).Update("is_active", false).Error; err != nil {
		t.Fatalf("failed to deactivate bot runtime config: %v", err)
	}

	// A chat between the owner and an unrelated third party the bot is not part
	// of: without an active runtime config the bot session must not see it.
	thirdParty := createUserForChatsTest(t, DB, "ci865-third@example.com", false)
	chat := database.Chat{User1Id: owner.ID, User2Id: thirdParty.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	if _, err := findAccessibleChat(DB, botUser, chat.UUID); err == nil {
		t.Fatalf("inactive runtime config must not widen the bot session scope")
	}
}
