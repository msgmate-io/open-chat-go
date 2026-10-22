package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gorm.io/gorm"

	"backend/logging"
)

type ListedMessage struct {
	UUID              string                  `json:"uuid"`
	SendAt            string                  `json:"send_at"`
	SenderID          uint                    `json:"sender_id"`
	ReceiverID        uint                    `json:"receiver_id"`
	SenderUUID        string                  `json:"sender_uuid"`
	SenderIsAutomated bool                    `json:"sender_is_automated"`
	DataType          string                  `json:"data_type"`
	Text              string                  `json:"text"`
	Reasoning         *[]string               `json:"reasoning"`
	ToolCalls         *[]interface{}          `json:"tool_calls"`
	MetaData          *map[string]interface{} `json:"meta_data"`
}

type ListedMessagesPage struct {
	Limit      int             `json:"limit"`
	Page       int             `json:"page"`
	TotalPages int             `json:"total_pages"`
	Rows       []ListedMessage `json:"rows"`
}

func convertMessageToListedMessage(message database.Message) ListedMessage {
	messageMetaData := map[string]interface{}{}
	json.Unmarshal(message.MetaData, &messageMetaData)

	text := ""
	if message.Text != nil {
		text = *message.Text
	}

	toolCalls := []interface{}{}
	if message.ToolCalls != nil {
		for _, toolCall := range *message.ToolCalls {
			var toolCallData map[string]interface{}
			json.Unmarshal(toolCall, &toolCallData)
			toolCalls = append(toolCalls, toolCallData)
		}
	}

	return ListedMessage{
		UUID:              message.UUID,
		SendAt:            message.CreatedAt.String(),
		SenderID:          message.SenderId,
		ReceiverID:        message.ReceiverId,
		SenderUUID:        message.Sender.UUID,
		SenderIsAutomated: message.Sender.IsAutomated,
		DataType:          message.DataType,
		Text:              text,
		Reasoning:         message.Reasoning,
		ToolCalls:         &toolCalls,
		MetaData:          &messageMetaData,
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
//	@Success      200 {object} chats.ListedMessagesPage "Paginated list of messages"
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

	chatUuid, err := parseChatUUID(r)
	if err != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 40}
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
	result := scopedChatQuery(DB, user).
		Preload("User1").
		Preload("User2").
		Where("uuid = ?", chatUuid).
		First(&chat)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
			return
		}
		logging.Errorf("Failed to load chat %s: %v", chatUuid, result.Error)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	// The chat filter has to exist BEFORE pagination runs: Paginate counts the
	// rows of the handle it receives to derive TotalPages, and counting the bare
	// DB handle described every message in the database rather than this chat's.
	// Clients then believed a short chat had hundreds of pages and kept paging
	// (the unnecessary-fetch class, ci#789). Build the filtered query first — the
	// same shape backend/api/chats/List.go uses — and paginate that.
	query := DB.
		Where("chat_id = ?", chat.ID).
		Where("deleted_at IS NULL")

	// Now list the messages paginated
	result = query.Scopes(database.Paginate(&messages, &pagination, query)).
		Preload("Sender").
		Find(&messages)

	if result.Error != nil {
		logging.Errorf("Failed to list messages for chat %s: %v", chatUuid, result.Error)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	listedMessages := make([]ListedMessage, len(messages))
	for i, message := range messages {
		listedMessages[i] = convertMessageToListedMessage(message)
	}

	response := ListedMessagesPage{
		Limit:      pagination.Limit,
		Page:       pagination.Page,
		TotalPages: pagination.TotalPages,
		Rows:       listedMessages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

}
