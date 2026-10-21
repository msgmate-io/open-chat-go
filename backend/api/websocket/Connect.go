package websocket

import (
	"backend/database"
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"gorm.io/gorm"
)

// isExpectedSocketClose reports whether an error returned while serving a
// WebSocket is a normal disconnect (the client went away or cancelled the
// request) rather than a real failure. Logging those disconnects as errors
// produced a steady stream of "context canceled" noise.
func isExpectedSocketClose(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure,
		websocket.StatusGoingAway,
		websocket.StatusNoStatusRcvd,
		websocket.StatusAbnormalClosure:
		return true
	}
	return false
}

func (ws *WebSocketHandler) Connect(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// Handle WebSocket subscription first, before any potential writes to the response
	if err := ws.SubscribeChannel(w, r, user.UUID); err != nil {
		// Log real failures only; a client disconnect (context canceled / closed
		// connection) is expected and must not be reported as an error.
		if !isExpectedSocketClose(err) {
			ws.logf("error soket connection error: %v", err)
		}
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var chatByUUID database.Chat
			chatErr := DB.Where("uuid = ? AND chat_type = ?", shareUUID, "interaction").First(&chatByUUID).Error
			if chatErr == nil {
				if shareByChatErr := DB.Where("chat_id = ?", chatByUUID.ID).Order("created_at DESC").First(&share).Error; shareByChatErr != nil {
					statusCode := http.StatusInternalServerError
					if errors.Is(shareByChatErr, gorm.ErrRecordNotFound) {
						statusCode = http.StatusNotFound
					}
					http.Error(w, "Shared chat not found", statusCode)
					return
				}
			} else {
				statusCode := http.StatusInternalServerError
				if errors.Is(chatErr, gorm.ErrRecordNotFound) {
					statusCode = http.StatusNotFound
				}
				http.Error(w, "Shared chat not found", statusCode)
				return
			}
		} else {
			http.Error(w, "Shared chat not found", http.StatusInternalServerError)
			return
		}
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

	if err := ws.SubscribeChatChannel(w, r, chat.UUID, share.ContentMode); err != nil {
		if !isExpectedSocketClose(err) {
			ws.logf("error shared interaction socket connection error: %v", err)
		}
		return
	}
}
