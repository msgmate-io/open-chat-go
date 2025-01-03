package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
)

// TODO: should also supply user Id
type CreateChat struct {
	ContactToken string `json:"contact_token"`
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chat)

}
