package websocket

import (
	"backend/database"
	"net/http"

	"gorm.io/gorm"
)

func (ws *WebSocketHandler) Connect(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// Handle WebSocket subscription first, before any potential writes to the response
	if err := ws.SubscribeChannel(w, r, user.UUID); err != nil {
		// Log the error but use http.Error before any WebSocket upgrade happens
		ws.logf("error soket connection error: %v", err)
		// http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// messagesHandler := Messages{}
	// jsonMessage := messagesHandler.UserWentOnline(user.UUID)
	// fetch the users contacts and send UserWentOnline to all of them
	//ws.PublishInChannel(jsonMessage, userId)
}

func (ws *WebSocketHandler) ConnectSharedInteraction(w http.ResponseWriter, r *http.Request) {
	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok || DB == nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	shareUUID := r.PathValue("chat_share_uuid")
	if shareUUID == "" {
		http.Error(w, "Invalid shared chat UUID", http.StatusBadRequest)
		return
	}

	var share database.SharedChatInstance
	if err := DB.Where("chat_share_uuid = ?", shareUUID).First(&share).Error; err != nil {
		statusCode := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			statusCode = http.StatusNotFound
		}
		http.Error(w, "Shared chat not found", statusCode)
		return
	}

	var chat database.Chat
	if err := DB.Where("id = ?", share.ChatId).First(&chat).Error; err != nil {
		statusCode := http.StatusInternalServerError
		if err == gorm.ErrRecordNotFound {
			statusCode = http.StatusNotFound
		}
		http.Error(w, "Shared chat not found", statusCode)
		return
	}

	if err := ws.SubscribeChatChannel(w, r, chat.UUID); err != nil {
		ws.logf("error shared interaction socket connection error: %v", err)
		return
	}
}
