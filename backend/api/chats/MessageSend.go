package chats

import (
	"backend/api/websocket"
	"backend/database"
	"encoding/json"
	"net/http"
)

// TODO: allow different message types
type SendMessage struct {
	Text string `json:"text"`
}

// Send a message to a chat
//
//	@Summary      Send a message
//	@Description  Send a message to a chat
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Param        text body SendMessage true "Message content"
//	@Param        chat_uuid path string true "Chat UUID"
//	@Router       /api/v1/chats/{chat_uuid}/messages/send [post]
func (h *ChatsHandler) MessageSend(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	var data SendMessage
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	chatUuid := r.PathValue("chat_uuid") // TODO - validate chat UUID!
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	// First find the chat by its id and user.ID
	var chat database.Chat
	result := database.DB.Preload("User1").
		Preload("User2").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
	}

	var receiverId uint
	if chat.User1.ID == user.ID {
		receiverId = chat.User2.ID
	} else {
		receiverId = chat.User1.ID
	}

	var message database.Message = database.Message{
		ChatId:     chat.ID,
		SenderId:   user.ID,
		ReceiverId: receiverId,
		Text:       &data.Text,
	}

	q := database.DB.Create(&message)

	// Now publish websocket updates to online & subscribed users
	websocket.MessageHandler.SendMessage(
		receiverId,
		websocket.MessageHandler.NewMessage(
			chatUuid,
			user.UUID,
			data.Text,
		),
	)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
