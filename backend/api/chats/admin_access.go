package chats

import (
	"backend/database"

	"gorm.io/gorm"
)

// resolveChatActingUser maps a session principal to the user a chat read acts
// for. A human session acts for itself; an automated (bot) session acts for the
// primary owner of its active bot runtime config, falling back to the bot itself
// when no owner is recorded.
//
// The canonical copy of this resolution lives in `backend/api/bots` (ci#824); it
// is duplicated here rather than shared because `bots` already imports this
// package, so importing it back would be an import cycle. It is a no-op for
// humans, so no scope is widened for them, and checks that must stay on the raw
// principal (ownership, admin, browser-token guards) keep using the session user.
func resolveChatActingUser(db *gorm.DB, sessionUser *database.User) *database.User {
	if sessionUser == nil || !sessionUser.IsAutomated {
		return sessionUser
	}
	var runtime database.BotRuntimeConfig
	err := db.Preload("OwnerUser").
		Where("bot_user_id = ? AND is_active = ?", sessionUser.ID, true).
		First(&runtime).Error
	if err == nil && runtime.OwnerUser.ID != 0 {
		owner := runtime.OwnerUser
		return &owner
	}
	return sessionUser
}

// scopedChatQuery restricts a chats query to the chats the user participates
// in. Admins may inspect any chat so they can view and manage other users'
// interactions in "see all" mode.
//
// An automated session is scoped to its owner (ci#865): a bot owns no
// user1_id/user2_id row of its own, so scoping by the raw principal rejected the
// very chat its own create-interaction call had just created with "Invalid chat
// UUID".
func scopedChatQuery(db *gorm.DB, user *database.User) *gorm.DB {
	user = resolveChatActingUser(db, user)
	if user != nil && user.IsAdmin {
		return db
	}
	return db.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
}

// scopedMessageQuery restricts a messages query to the messages the user
// exchanged. Admins may inspect any message in "see all" mode; an automated
// session is scoped to its owner for the same reason as scopedChatQuery
// (ci#865).
func scopedMessageQuery(db *gorm.DB, user *database.User) *gorm.DB {
	user = resolveChatActingUser(db, user)
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
