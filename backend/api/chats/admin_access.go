package chats

import (
	"strings"

	"backend/database"

	"gorm.io/gorm"
)

// actingScopeUserIDs returns the participant ids a session may read chats and
// messages for.
//
// ci#865: an interaction created through POST /bots/{identifier}/interactions is
// stored owner-scoped — its participant is the *acting user* (the owner of the
// bot runtime, see createBotInteraction) — while the chat read/follow routes
// scoped by the raw session principal. For an automated (bot) session the raw
// principal is the bot user itself, so the session that had just created a
// dispatched interaction could not read or follow it back and the dispatched run
// never started (the same defect behind the silent_idle_freeze failures on
// ci#848).
//
// Mirroring the bots API's resolveActingUser, an automated session additionally
// scopes by the owner of its active runtime config. The resolution is strictly
// additive — the raw session principal stays in scope — so a bot session still
// reads every chat it participates in (for example an interaction dispatched by
// a non-owner acting user) and no visibility is lost. It is a no-op for
// non-automated sessions and for bots without an active runtime config, so
// authorisation for unrelated users is unchanged.
func actingScopeUserIDs(db *gorm.DB, user *database.User) []uint {
	if user == nil {
		return nil
	}
	ids := []uint{user.ID}
	if db == nil || !user.IsAutomated {
		return ids
	}
	var runtime database.BotRuntimeConfig
	err := db.Where("bot_user_id = ? AND is_active = ?", user.ID, true).First(&runtime).Error
	if err != nil || runtime.OwnerUserId == 0 || runtime.OwnerUserId == user.ID {
		return ids
	}
	return append(ids, runtime.OwnerUserId)
}

// scopeByParticipant restricts a query to rows where one of the given columns
// matches any of the ids.
func scopeByParticipant(db *gorm.DB, ids []uint, columns ...string) *gorm.DB {
	conditions := make([]string, 0, len(columns))
	args := make([]interface{}, 0, len(columns))
	for _, column := range columns {
		if len(ids) == 1 {
			conditions = append(conditions, column+" = ?")
			args = append(args, ids[0])
			continue
		}
		conditions = append(conditions, column+" IN ?")
		args = append(args, ids)
	}
	return db.Where(strings.Join(conditions, " OR "), args...)
}

// scopedChatQuery restricts a chats query to the chats the user participates
// in. Admins may inspect any chat so they can view and manage other users'
// interactions in "see all" mode.
func scopedChatQuery(db *gorm.DB, user *database.User) *gorm.DB {
	if user != nil && user.IsAdmin {
		return db
	}
	ids := actingScopeUserIDs(db, user)
	if len(ids) == 0 {
		return db.Where("1 = 0")
	}
	return scopeByParticipant(db, ids, "user1_id", "user2_id")
}

// scopedMessageQuery restricts a messages query to the messages the user
// exchanged. Admins may inspect any message in "see all" mode.
func scopedMessageQuery(db *gorm.DB, user *database.User) *gorm.DB {
	if user != nil && user.IsAdmin {
		return db
	}
	ids := actingScopeUserIDs(db, user)
	if len(ids) == 0 {
		return db.Where("1 = 0")
	}
	return scopeByParticipant(db, ids, "receiver_id", "sender_id")
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
