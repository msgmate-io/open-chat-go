package chats

import (
	"encoding/json"
	"net/http"
	"strings"

	"backend/database"
	"backend/server/util"

	"gorm.io/gorm"
)

// Delete soft-deletes a chat owned by the authenticated user together with its
// messages, settings, shared config and derived interaction state, so the chat
// disappears from the list and can no longer be opened.
//
//	@Summary      Delete chat
//	@Description  Delete a chat and its messages/settings owned by the authenticated user
//	@Tags         chats
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} map[string]interface{} "Deletion result"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Unable to delete chat"
//	@Router       /api/v1/chats/{chat_uuid} [delete]
func (h *ChatsHandler) Delete(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.Message{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.ChatSettings{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.SharedChatConfig{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.SharedChatInstance{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.ToolInitData{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.StreamingMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("chat_id = ?", chat.ID).Delete(&database.ActionTaskDismissal{}).Error; err != nil {
			return err
		}
		return tx.Delete(&chat).Error
	}); err != nil {
		http.Error(w, "Unable to delete chat", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"chat_uuid": chat.UUID,
		"deleted":   true,
	})
}
