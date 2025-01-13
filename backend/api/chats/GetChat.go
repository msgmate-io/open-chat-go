package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
)

func (h *ChatsHandler) GetChat(w http.ResponseWriter, r *http.Request) {
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

	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
	}

	w.Header().Set("Content-Type", "application/json")

	listedChat := convertChatToListedChat(user, chat)
	json.NewEncoder(w).Encode(listedChat)
}
