package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type ListedChat struct {
	UUID          string            `json:"uuid"`
	Partner       database.User     `json:"partner"`
	LatestMessage *database.Message `json:"latest_message"`
	ChatType      string            `json:"chat_type"`
	Config        interface{}       `json:"config"`
}

func convertChatToListedChat(user *database.User, chat database.Chat) ListedChat {
	var partner database.User
	if chat.User1Id == user.ID {
		partner = chat.User2
	} else {
		partner = chat.User1
	}

	var config interface{}
	if chat.SharedConfig != nil {
		// The ConfigData is already JSON, just unmarshal it
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err != nil {
			log.Printf("Error unmarshaling config data: %v", err)
		}
	}

	return ListedChat{
		UUID:          chat.UUID,
		Partner:       partner,
		ChatType:      chat.ChatType,
		LatestMessage: chat.LatestMessage,
		Config:        config,
	}
}

// List returns a list of chats for a specified user.
//
//	@Summary      Get user chats
//	@Description  Retrieve a list of chats for the authenticated user
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        page  query  int  false  "Page number"  default(1)
//	@Param        limit query  int  false  "Page size"     default(40)
//	@Param        chat_types query string false "Chat types to filter by"
//	@Success      200 {object} database.Pagination "Paginated list of chats"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/chats/list [get]
func (h *ChatsHandler) List(w http.ResponseWriter, r *http.Request) {
	var chats []database.Chat

	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
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

	// Build the base query
	query := DB.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID)

	// Handle chat_types filter
	if chatTypesParam := r.URL.Query().Get("chat_types"); chatTypesParam != "" {
		chatTypes := strings.Split(chatTypesParam, ",")
		if len(chatTypes) > 0 {

			// Check if we need wildcard matching for "integration"
			hasIntegration := false
			for _, chatType := range chatTypes {
				if chatType == "integration" {
					hasIntegration = true
					break
				}
			}

			// Build the query conditions
			if hasIntegration {
				// Use pattern matching to find both exact "integration" and "integration:*"
				query = query.Where("chat_type IN ? OR chat_type LIKE ?", chatTypes, "integration:%")
			} else {
				// Use the original exact matching
				query = query.Where("chat_type IN ?", chatTypes)
			}
		}
	}

	// Apply pagination and preloads
	q := query.Scopes(database.Paginate(&chats, &pagination, DB)).
		Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Preload("LatestMessage").
		Find(&chats)

	if q.Error != nil {
		http.Error(w, q.Error.Error(), http.StatusInternalServerError)
		return
	}

	listedChats := make([]ListedChat, len(chats))
	for i, chat := range chats {
		listedChats[i] = convertChatToListedChat(user, chat)
	}

	pagination.Rows = listedChats

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
