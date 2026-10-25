package chats

import (
	"backend/database"

	"gorm.io/gorm"
)

// actingScopeUser resolves the user whose chats a session may read.
//
// ci#865: an interaction created through POST /bots/{identifier}/interactions
// is stored owner-scoped — the participant is the *acting user* (the owner of
// the bot runtime, see createBotInteraction), while the read/follow routes
// scoped by the raw session principal. For an automated (bot) session that is
// the bot user itself, so the session that had just created a dispatched
// interaction could not read or follow it back and the dispatched run never
// started (also behind the silent_idle_freeze failures on ci#848).
//
// Mirroring the bots API's resolveActingUser, an automated session is therefore
// resolved to the owner of its active runtime config. Non-automated sessions and
// bots without an active runtime config keep their own scope, so authorisation
// for unrelated users is unchanged.
func actingScopeUser(db *gorm.DB, user *database.User) *database.User {
	if db == nil || user == nil || !user.IsAutomated {
		return user
	}
	var runtime database.BotRuntimeConfig
	err := db.Preload("OwnerUser").
		Where("bot_user_id = ? AND is_active = ?", user.ID, true).
		First(&runtime).Error
	if err != nil || runtime.OwnerUser.ID == 0 {
		return user
	}
	owner := runtime.OwnerUser
	return &owner
}

// scopedChatQuery restricts a chats query to the chats the user participates
// in. Admins may inspect any chat so they can view and manage other users'
// interactions in "see all" mode.
func scopedChatQuery(db *gorm.DB, user *database.User) *gorm.DB {
	user = actingScopeUser(db, user)
	if user != nil && user.IsAdmin {
		return db
	}
	return db.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
}

// scopedMessageQuery restricts a messages query to the messages the user
// exchanged. Admins may inspect any message in "see all" mode.
func scopedMessageQuery(db *gorm.DB, user *database.User) *gorm.DB {
	user = actingScopeUser(db, user)
	if user != nil && user.IsAdmin {
		return db
	}
	return db.Where("receiver_id = ? OR sender_id = ?", user.ID, user.ID)
}

// findAccessibleChat loads a chat by UUID for the given user. Admins may load
// any chat; regular users only chats they participate in.
func findAccessibleChat(db *gorm.DB, user *database.User, chatUUID string) (database.Chat, error) {
	var chat database.Chat
	err := scopedChatQuery(db, user).Where("uuid = ?", chatUUID).First(&chat).Error
	return chat, err
}

// adminIsExternalViewer reports whether an admin is looking at a chat they do
// not participate in. The frontend uses this to render an explicit "admin view"
// banner.
func adminIsExternalViewer(user *database.User, chat database.Chat) bool {
	if user == nil || !user.IsAdmin {
		return false
	}
	return chat.User1Id != user.ID && chat.User2Id != user.ID
}

// resolveChatBotCounterparty returns the bot participant of a chat for the
// acting user. Regular users get the other participant; an admin inspecting a
// chat they do not participate in gets the automated participant, so admins can
// approve/cancel other users' coding interactions.
func resolveChatBotCounterparty(chat database.Chat, user database.User) (database.User, bool) {
	if bot, ok := getChatCounterparty(chat, user); ok {
		return bot, true
	}
	if user.IsAdmin {
		if chat.User1.IsAutomated {
			return chat.User1, true
		}
		if chat.User2.IsAutomated {
			return chat.User2, true
		}
	}
	return database.User{}, false
}
