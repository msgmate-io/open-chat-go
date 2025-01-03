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
	err := ws.SubscribeChannel(w, r, user.UUID)

	if err != nil {
		ws.logf("error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// messagesHandler := Messages{}
	// jsonMessage := messagesHandler.UserWentOnline(user.UUID)
	// fetch the users contacts and send UserWentOnline to all of them
	//ws.PublishInChannel(jsonMessage, userId)
}
