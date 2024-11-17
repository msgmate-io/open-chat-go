package chats

import (
	"net/http"
)

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

}
