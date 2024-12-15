package websocket

import (
	"backend/database"
	"net/http"
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
