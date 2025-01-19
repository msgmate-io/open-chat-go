package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
)

// TODO: should also supply user Id
type CreateChat struct {
	ContactToken string          `json:"contact_token"`
	FirstMessage string          `json:"first_message"`
	SharedConfig json.RawMessage `json:"shared_config"`
}

// Create a chat
//
//	@Summary      Create a chat
//	@Description  Create a chat
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Success      200  {string}  string	"Chat created"
//	@Failure      400  {string}  string	"Invalid chat"
//	@Failure      500  {object}  string	"Internal server error"
//	@Router       /api/v1/chats/create [post]
func (h *ChatsHandler) Create(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	ch, err := util.GetWebsocket(r)
	if err != nil {
		http.Error(w, "Unable to get websocket", http.StatusBadRequest)
		return
	}

	var data CreateChat
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var otherUser database.User
	if err := DB.First(&otherUser, "contact_token = ?", data.ContactToken).Error; err != nil {
		http.Error(w, "Invalid contact token", http.StatusBadRequest)
		return
	}

	// TODO check for blocked users
	// Small optimization, try to always ensure User1Id < User2Id
	var chat database.Chat
	if user.ID < otherUser.ID {
		chat = database.Chat{
			User1Id: user.ID,
			User2Id: otherUser.ID,
		}
	} else {
		chat = database.Chat{
			User1Id: otherUser.ID,
			User2Id: user.ID,
		}
	}

	DB.Create(&chat)
	DB.Preload("User1").Preload("User2").Preload("LatestMessage").First(&chat, chat.ID)

	if data.FirstMessage != "" {
		message := database.Message{
			ChatId:     chat.ID,
			SenderId:   user.ID,
			ReceiverId: otherUser.ID,
			Text:       &data.FirstMessage,
		}
		DB.Create(&message)
		chat.LatestMessageId = &message.ID
		DB.Save(&chat)
	}

	if data.SharedConfig != nil {
		sharedConfig := database.SharedChatConfig{
			ChatId:     chat.ID,
			ConfigData: data.SharedConfig,
		}
		DB.Create(&sharedConfig)
		chat.SharedConfigId = &sharedConfig.ID
		DB.Save(&chat)
	}

	if data.FirstMessage != "" {
		SendWebsocketMessage(ch, otherUser.UUID, chat.UUID, *user, SendMessage{
			Text: data.FirstMessage,
		})
	}

	DB.Preload("User1").Preload("User2").Preload("LatestMessage").First(&chat, chat.ID)
	listedChat := convertChatToListedChat(user, chat)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedChat)

}
