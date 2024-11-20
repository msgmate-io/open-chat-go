package chats

import (
	"backend/database"
	"encoding/json"
	"net/http"
)

// List returns a list of chats for a specified user.
//
//	@Summary      Get user chats
//	@Description  Retrieve a list of chats associated with a specific user ID
//	@Tags         chats
//	@Accept       json
//	@Produce      json
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

	if err := database.DB.Where("user1_id = ? OR user2_id = ?", user.ID, user.ID).Find(&chats).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Encode chats to JSON and handle any potential error during encoding
	if err := json.NewEncoder(w).Encode(chats); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
