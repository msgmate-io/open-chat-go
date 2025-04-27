package chats

import (
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
)

type RerunMessageResponse struct {
	Success           bool   `json:"success"`
	ChatUUID          string `json:"chat_uuid"`
	SourceMessageUUID string `json:"source_message_uuid"`
	ResentMessageUUID string `json:"resent_message_uuid"`
	DeletedCount      int64  `json:"deleted_count"`
	Enqueued          bool   `json:"enqueued"`
}

func getChatCounterparty(chat database.Chat, user database.User) (database.User, bool) {
	if chat.User1.ID == user.ID {
		return chat.User2, true
	}
	if chat.User2.ID == user.ID {
		return chat.User1, true
	}
	return database.User{}, false
}

// RerunMessage rewinds a bot chat to a user message and retriggers bot reply.
//
//	@Summary      Rerun from message
//	@Description  Delete the selected user message and newer messages, recreate that user message, and enqueue a bot reply.
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        message_uuid path string true "Source user message UUID"
//	@Success      200 {object} chats.RerunMessageResponse
//	@Failure      400 {string} string "Invalid request"
//	@Failure      403 {string} string "Forbidden"
//	@Failure      404 {string} string "Not found"
//	@Failure      409 {string} string "Conflict"
//	@Router       /api/v1/chats/{chat_uuid}/messages/{message_uuid}/rerun [post]
func (h *ChatsHandler) RerunMessage(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := r.PathValue("chat_uuid")
	messageUUID := r.PathValue("message_uuid")
	if chatUUID == "" || messageUUID == "" {
		http.Error(w, "Invalid chat/message UUID", http.StatusBadRequest)
		return
	}

	queueClient, clientErr := util.GetAsynqClient(r)
	queueInspector, inspectorErr := util.GetAsynqInspector(r)
	if clientErr != nil || inspectorErr != nil {
		http.Error(w, "Async queue unavailable", http.StatusInternalServerError)
		return
	}

	var chat database.Chat
	if err := DB.Preload("User1").
		Preload("User2").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, user.ID, user.ID).
		First(&chat).Error; err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	botUser, ok := getChatCounterparty(chat, *user)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !botUser.IsAutomated {
		http.Error(w, "Rerun is only available in chats with bots", http.StatusConflict)
		return
	}

	var sourceMessage database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&sourceMessage).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}

	if sourceMessage.SenderId != user.ID {
		http.Error(w, "Only your own messages can be rerun", http.StatusForbidden)
		return
	}
	if sourceMessage.ReceiverId != botUser.ID {
		http.Error(w, "Message is not addressed to the bot", http.StatusConflict)
		return
	}

	if sourceMessage.DataType == "event" {
		http.Error(w, "Event messages cannot be rerun", http.StatusConflict)
		return
	}

	var resentMessage database.Message
	var deletedCount int64
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Message{}).
			Where("chat_id = ? AND id >= ? AND deleted_at IS NULL", chat.ID, sourceMessage.ID).
			Count(&deletedCount).Error; err != nil {
			return err
		}

		if deletedCount == 0 {
			return gorm.ErrRecordNotFound
		}

		if err := tx.Where("chat_id = ? AND id >= ?", chat.ID, sourceMessage.ID).Delete(&database.Message{}).Error; err != nil {
			return err
		}

		messageText := ""
		if sourceMessage.Text != nil {
			messageText = *sourceMessage.Text
		}

		resentMessage = database.Message{
			ChatId:     chat.ID,
			SenderId:   sourceMessage.SenderId,
			ReceiverId: sourceMessage.ReceiverId,
			DataType:   sourceMessage.DataType,
			Text:       &messageText,
			Reasoning:  sourceMessage.Reasoning,
			ToolCalls:  sourceMessage.ToolCalls,
			MetaData:   sourceMessage.MetaData,
		}

		if err := tx.Create(&resentMessage).Error; err != nil {
			return err
		}

		if err := tx.Model(&chat).Update("latest_message_id", resentMessage.ID).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "Message not found or already removed", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to rerun message", http.StatusInternalServerError)
		return
	}

	if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
		ChatUUID:    chatUUID,
		MessageUUID: resentMessage.UUID,
		BotUserID:   botUser.ID,
	}); enqueueErr != nil {
		http.Error(w, "Failed to enqueue bot reply", http.StatusInternalServerError)
		return
	}

	response := RerunMessageResponse{
		Success:           true,
		ChatUUID:          chatUUID,
		SourceMessageUUID: sourceMessage.UUID,
		ResentMessageUUID: resentMessage.UUID,
		DeletedCount:      deletedCount,
		Enqueued:          true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
