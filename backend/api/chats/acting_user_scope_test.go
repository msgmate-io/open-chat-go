package chats

import (
	"backend/database"
	"testing"

	"gorm.io/gorm"
)

// createBotRuntimeForChatsTest persists an automated bot user owned by `owner`
// plus its active runtime config, mirroring what the create-bot API writes.
func createBotRuntimeForChatsTest(t *testing.T, DB *gorm.DB, owner *database.User, name string) *database.User {
	t.Helper()
	botUser := database.User{
		Name:         name,
		Username:     name,
		Email:        name + "@bot.local",
		IsAutomated:  true,
		ContactToken: name + "-contact",
	}
	if err := DB.Create(&botUser).Error; err != nil {
		t.Fatalf("failed to create bot user %q: %v", name, err)
	}
	runtime := database.BotRuntimeConfig{
		BotUserId:   botUser.ID,
		OwnerUserId: owner.ID,
		Name:        name,
		IsActive:    true,
	}
	if err := DB.Create(&runtime).Error; err != nil {
		t.Fatalf("failed to create bot runtime %q: %v", name, err)
	}
	if err := database.EnsureBotRuntimeOwner(DB, runtime.ID, owner.ID); err != nil {
		t.Fatalf("failed to record bot owner %q: %v", name, err)
	}
	return &botUser
}

func createChatForChatsTest(t *testing.T, DB *gorm.DB, user1, user2 *database.User) database.Chat {
	t.Helper()
	chat := database.Chat{
		User1Id:  user1.ID,
		User2Id:  user2.ID,
		ChatType: "interaction",
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}
	return chat
}

// A bot session owns no chat row itself: its create-interaction call creates the
// chat under its owner, so a read scoped to the bot's own user id used to miss
// the chat it had just created and answer "Invalid chat UUID" (ci#865).
func TestScopedChatQueryWidensAutomatedSessionToItsOwner(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner", false)
	other := createUserForChatsTest(t, DB, "other", false)
	bot := createBotRuntimeForChatsTest(t, DB, owner, "scopebot")

	chat := createChatForChatsTest(t, DB, owner, other)

	// The bot's own scope must reach the owner's chat.
	loaded, err := findAccessibleChat(DB, bot, chat.UUID)
	if err != nil {
		t.Fatalf("bot could not load its owner's chat %s: %v", chat.UUID, err)
	}
	if loaded.ID != chat.ID {
		t.Fatalf("bot loaded chat %d, want %d", loaded.ID, chat.ID)
	}

	// Narrowing must still hold: a chat the owner does not participate in stays
	// invisible to the bot.
	stranger := createUserForChatsTest(t, DB, "stranger", false)
	private := createChatForChatsTest(t, DB, other, stranger)
	if _, err := findAccessibleChat(DB, bot, private.UUID); err == nil {
		t.Fatalf("bot reached a chat its owner does not participate in (%s)", private.UUID)
	}
}

// A human session must still be scoped to itself: an automated-only widening
// here would leak other users' chats.
func TestScopedChatQueryKeepsHumanScopedToSelf(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "humanowner", false)
	other := createUserForChatsTest(t, DB, "humanother", false)
	stranger := createUserForChatsTest(t, DB, "humanstranger", false)

	chat := createChatForChatsTest(t, DB, owner, other)

	if _, err := findAccessibleChat(DB, owner, chat.UUID); err != nil {
		t.Fatalf("participant could not load own chat: %v", err)
	}
	if _, err := findAccessibleChat(DB, stranger, chat.UUID); err == nil {
		t.Fatal("unrelated human could load someone else's chat")
	}
}

// An automated session with no runtime config must fall back to itself rather
// than silently widening to every chat.
func TestScopedChatQueryWithoutRuntimeFallsBackToBotUser(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "nobotowner", false)
	orphan := database.User{
		Name:         "orphanbot",
		Username:     "orphanbot",
		Email:        "orphanbot@bot.local",
		IsAutomated:  true,
		ContactToken: "orphanbot-contact",
	}
	if err := DB.Create(&orphan).Error; err != nil {
		t.Fatalf("failed to create orphan bot: %v", err)
	}

	chat := createChatForChatsTest(t, DB, owner, owner)
	if _, err := findAccessibleChat(DB, &orphan, chat.UUID); err == nil {
		t.Fatal("orphan automated session reached an unrelated chat")
	}
}

// Message reads take the same acting-user hop as chat reads (ci#865).
func TestScopedMessageQueryWidensAutomatedSessionToItsOwner(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "msgowner", false)
	other := createUserForChatsTest(t, DB, "msgother", false)
	bot := createBotRuntimeForChatsTest(t, DB, owner, "msgbot")

	chat := createChatForChatsTest(t, DB, owner, other)
	text := "owner to other"
	if err := DB.Create(&database.Message{
		SenderId:   owner.ID,
		ReceiverId: other.ID,
		ChatId:     chat.ID,
		Text:       &text,
	}).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	var messages []database.Message
	if err := scopedMessageQuery(DB, bot).Where("chat_id = ?", chat.ID).Find(&messages).Error; err != nil {
		t.Fatalf("bot message query failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("bot saw %d messages for its owner's chat, want 1", len(messages))
	}
}

// Admins keep their unscoped view, and the acting-user hop must not turn a
// non-admin owner into an admin.
func TestScopedChatQueryAdminAndActingUserInteraction(t *testing.T) {
	DB := setupChatsTestDB(t)
	admin := createUserForChatsTest(t, DB, "actingadmin", true)
	owner := createUserForChatsTest(t, DB, "actingowner", false)
	stranger := createUserForChatsTest(t, DB, "actingstranger", false)
	bot := createBotRuntimeForChatsTest(t, DB, owner, "actingbot")

	chat := createChatForChatsTest(t, DB, stranger, stranger)

	// The admin session sees everything.
	if _, err := findAccessibleChat(DB, admin, chat.UUID); err != nil {
		t.Fatalf("admin could not load an unrelated chat: %v", err)
	}
	// The bot is scoped to its (non-admin) owner, so it must not inherit admin.
	if _, err := findAccessibleChat(DB, bot, chat.UUID); err == nil {
		t.Fatal("bot inherited the admin's unscoped chat view")
	}
}
