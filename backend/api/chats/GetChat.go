package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strings"
)

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if commaIdx := strings.Index(host, ","); commaIdx >= 0 {
		host = strings.TrimSpace(host[:commaIdx])
	}
	if host == "" {
		return ""
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	return scheme + "://" + host
}

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

	var share database.SharedChatInstance
	shareErr := DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, user.ID).First(&share).Error
	if shareErr == nil {
		listedChat.ChatShareUUID = share.ChatShareUUID
		baseURL := requestBaseURL(r)
		if baseURL != "" {
			listedChat.SharedChatURL = baseURL + "/interaction/" + share.ChatShareUUID
		}
	}

	json.NewEncoder(w).Encode(listedChat)
}
