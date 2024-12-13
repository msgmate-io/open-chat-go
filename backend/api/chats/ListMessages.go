package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

type ListedMessage struct {
	UUID       string `json:"uuid"`
	SendAt     string `json:"send_at"`
	SenderUUID string `json:"sender_uuid"`
	Text       string `json:"text"`
}

func convertMessageToListedMessage(message database.Message) ListedMessage {
	return ListedMessage{
		UUID:       message.UUID,
		SendAt:     message.CreatedAt.String(),
		SenderUUID: message.Sender.UUID,
		Text:       *message.Text,
	}
}

// List returns a list of messages for a specified chat ( owned by the user).
//
//	@Summary      Get Chat messages
//	@Description  Retrieve a list of messages associated with a specific chat UUID
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Param        page  query  int  false  "Page number"  default(1)
//	@Param        limit query  int  false  "Page size"     default(10)
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {array}  database.Message "List of chats"
//	@Failure      400 {string} string "Invalid user ID"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/chats/{chat_uuid}/messages/list [get]
func (h *ChatsHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	var messages []database.Message
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUuid := r.PathValue("chat_uuid") // TODO - validate chat UUID!
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 10}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// First find the chat by its id and user.ID
	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
	}

	// Now list the messages paginated
	result = DB.Scopes(database.Paginate(&messages, &pagination, DB)).
		Where("chat_id = ? AND (receiver_id = ? OR sender_id = ?)", chat.ID, user.ID, user.ID).
		Where("deleted_at IS NULL").
		Preload("Sender").
		Find(&messages)

	if result.Error != nil {
		http.Error(w, "Couldn't find messages", http.StatusBadRequest)
	}

	listedMessages := make([]ListedMessage, len(messages))
	for i, message := range messages {
		listedMessages[i] = convertMessageToListedMessage(message)
	}

	pagination.Rows = listedMessages

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)

}
