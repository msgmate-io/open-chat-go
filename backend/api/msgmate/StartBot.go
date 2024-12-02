package msgmate

import (
	"backend/database"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// starts a go-routine with a bot that starts listening to the chat websocket with a specific time-out
func (h *MsgmateHandler) StartBot(w http.ResponseWriter, r *http.Request) {
	_, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// 0 - authenticate the bot with it's own account
	sessionId := "TODO"

	// 1 - retrieve a session for the bot

	dialOptions := websocket.DialOptions{}
	dialOptions.HTTPHeader.Add("Cookie", fmt.Sprintf("session_id=%v", sessionId))

	c, _, err := websocket.Dial(ctx, "ws://localhost:8080", &dialOptions)
	if err != nil {
		http.Error(w, "Unable to dial websocket connection", http.StatusBadRequest)
		return
	}
	defer c.CloseNow()

	err = wsjson.Write(ctx, c, "hi")
	if err != nil {
		http.Error(w, "Unable to write to channel", http.StatusBadRequest)
		return
	}

	c.Close(websocket.StatusNormalClosure, "")

}
