package chats

import (
	"backend/database"

	"gorm.io/gorm"
)

// scopedChatQuery restricts a chats query to the chats the user participates
// in. Admins may inspect any chat so they can view and manage other users'
// interactions in "see all" mode.
func scopedChatQuery(db *gorm.DB, user *database.User) *gorm.DB {
	if user != nil && user.IsAdmin {
		return db
	}
	return db.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)
}

// scopedMessageQuery restricts a messages query to the messages the user
// exchanged. Admins may inspect any message in "see all" mode.
func scopedMessageQuery(db *gorm.DB, user *database.User) *gorm.DB {
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
