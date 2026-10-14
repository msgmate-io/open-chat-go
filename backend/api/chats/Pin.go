package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type updateChatPinRequest struct {
	Pinned bool `json:"pinned"`
}

type chatPinResponse struct {
	ChatUUID string `json:"chat_uuid"`
	Pinned   bool   `json:"pinned"`
}

// loadChatPins batch-loads which of the given chats the user has pinned.
func loadChatPins(db *gorm.DB, userID uint, chatIDs []uint) map[uint]bool {
	pinned := map[uint]bool{}
	if len(chatIDs) == 0 {
		return pinned
	}
	rows := []database.ChatPin{}
	if err := db.Where("user_id = ? AND chat_id IN ?", userID, chatIDs).Find(&rows).Error; err != nil {
		return pinned
	}
	for _, row := range rows {
		pinned[row.ChatId] = true
	}
	return pinned
}

// setChatPin pins or unpins a chat for a user.
//
// ChatPin embeds the soft-deletable Model, but its unique index covers only
// (chat_id, user_id). So an unpin leaves a soft-deleted row that both blocks a
// later insert (unique violation) and is invisible to loadChatPins, which made
// "pin → unpin → pin" silently leave the chat unpinned (ci#361). Pin therefore
// resurrects any existing row on conflict, and unpin hard-deletes so no stale
// row is left behind.
func setChatPin(db *gorm.DB, chatID, userID uint, pinned bool) error {
	if pinned {
		pin := database.ChatPin{ChatId: chatID, UserId: userID}
		return db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chat_id"}, {Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"deleted_at": nil}),
		}).Create(&pin).Error
	}
	return db.Unscoped().
		Where("chat_id = ? AND user_id = ?", chatID, userID).
		Delete(&database.ChatPin{}).Error
}

// UpdatePin pins or unpins a chat for the authenticated user.
//
//	@Summary      Pin or unpin a chat
//	@Description  Set the per-user pin flag for a chat the authenticated user participates in
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        request body chats.updateChatPinRequest true "Pin state"
//	@Success      200 {object} chats.chatPinResponse "Updated pin state"
//	@Failure      400 {string} string "Invalid chat UUID or JSON"
//	@Router       /api/v1/chats/{chat_uuid}/pin [put]
func (h *ChatsHandler) UpdatePin(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := strings.TrimSpace(r.PathValue("chat_uuid"))
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var chat database.Chat
	result := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, user.ID, user.ID).First(&chat)
	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var payload updateChatPinRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := setChatPin(DB, chat.ID, user.ID, payload.Pinned); err != nil {
		if payload.Pinned {
			http.Error(w, "Unable to pin chat", http.StatusInternalServerError)
		} else {
			http.Error(w, "Unable to unpin chat", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatPinResponse{ChatUUID: chat.UUID, Pinned: payload.Pinned})
}
