package chats

import (
	"backend/database"
	"encoding/json"
	"net/http"
	"strconv"
)

type ListedChat struct {
	UUID          string            `json:"uuid"`
	Partner       database.User     `json:"partner"`
	LatestMessage *database.Message `json:"latest_message"`
}

func convertChatToListedChat(user *database.User, chat database.Chat) ListedChat {
	var partner database.User
	if chat.User1Id == user.ID {
		partner = chat.User2
	} else {
		partner = chat.User1
	}

	return ListedChat{
		UUID:          chat.UUID,
		Partner:       partner,
		LatestMessage: chat.LatestMessage,
	}
}

// List returns a list of chats for a specified user.
//
//	@Summary      Get user chats
//	@Description  Retrieve a list of chats associated with a specific user ID
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        page  query  int  false  "Page number"  default(1)
//	@Param        limit query  int  false  "Page size"     default(10)
//	@Param        userID path int true "User ID"
//	@Success      200 {array}  database.Chat "List of chats"
//	@Failure      400 {string} string "Invalid user ID"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/chats/list [get]
func (h *ChatsHandler) List(w http.ResponseWriter, r *http.Request) {
	// TODO - implement pagination!
	var chats []database.Chat

	user, ok := r.Context().Value("user").(*database.User)

	if !ok {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
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

	q := database.DB.Scopes(database.Paginate(&chats, &pagination, database.DB)).
		Where("user1_id = ? OR user2_id = ?", user.ID, user.ID).
		Preload("User1").
		Preload("User2").
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
