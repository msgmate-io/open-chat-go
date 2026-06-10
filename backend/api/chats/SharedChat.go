package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type SharedChatPublishResponse struct {
	ChatUUID      string `json:"chat_uuid"`
	ChatShareUUID string `json:"chat_share_uuid"`
}

type PublicInteractionChat struct {
	UUID     string      `json:"uuid"`
	ChatType string      `json:"chat_type"`
	Partner  interface{} `json:"partner"`
	Config   interface{} `json:"config"`
}

func (h *ChatsHandler) Publish(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := r.PathValue("chat_uuid")
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	chat, err := findOwnedChat(DB, user.ID, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	var share database.SharedChatInstance
	err = DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, user.ID).First(&share).Error
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SharedChatPublishResponse{ChatUUID: chat.UUID, ChatShareUUID: share.ChatShareUUID})
		return
	}
	if err != gorm.ErrRecordNotFound {
		http.Error(w, "Failed to publish chat", http.StatusInternalServerError)
		return
	}

	share = database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  user.ID,
		ChatShareUUID: uuid.NewString(),
	}
	if err := DB.Create(&share).Error; err != nil {
		http.Error(w, "Failed to publish chat", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SharedChatPublishResponse{ChatUUID: chat.UUID, ChatShareUUID: share.ChatShareUUID})
}

func (h *ChatsHandler) Unpublish(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := r.PathValue("chat_uuid")
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	chat, err := findOwnedChat(DB, user.ID, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	if err := DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, user.ID).Delete(&database.SharedChatInstance{}).Error; err != nil {
		http.Error(w, "Failed to unpublish chat", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h *ChatsHandler) GetSharedInteraction(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	shareUUID := r.PathValue("chat_share_uuid")
	if shareUUID == "" {
		http.Error(w, "Invalid shared chat UUID", http.StatusBadRequest)
		return
	}

	chat, _, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		http.Error(w, "Shared chat not found", http.StatusNotFound)
		return
	}

	partner := chat.User2
	if partner.ID == 0 {
		partner = chat.User1
	}

	var config interface{}
	if chat.SharedConfig != nil {
		_ = json.Unmarshal(chat.SharedConfig.ConfigData, &config)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PublicInteractionChat{
		UUID:     chat.UUID,
		ChatType: chat.ChatType,
		Partner: map[string]interface{}{
			"name":         partner.Name,
			"is_automated": partner.IsAutomated,
			"uuid":         partner.UUID,
		},
		Config: config,
	})
}

func (h *ChatsHandler) ListSharedInteractionMessages(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	shareUUID := r.PathValue("chat_share_uuid")
	if shareUUID == "" {
		http.Error(w, "Invalid shared chat UUID", http.StatusBadRequest)
		return
	}

	chat, _, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		http.Error(w, "Shared chat not found", http.StatusNotFound)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 40}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, parseErr := strconv.Atoi(pageParam); parseErr == nil && page > 0 {
			pagination.Page = page
		}
	}
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, parseErr := strconv.Atoi(limitParam); parseErr == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	var messages []database.Message
	q := DB.Scopes(database.Paginate(&messages, &pagination, DB)).
		Where("chat_id = ?", chat.ID).
		Where("deleted_at IS NULL").
		Preload("Sender").
		Find(&messages)
	if q.Error != nil {
		http.Error(w, "Couldn't list shared messages", http.StatusInternalServerError)
		return
	}

	listed := make([]ListedMessage, len(messages))
	for i, message := range messages {
		listed[i] = convertMessageToListedMessage(message)
	}
	pagination.Rows = listed

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}

func findOwnedChat(DB *gorm.DB, userID uint, chatUUID string) (database.Chat, error) {
	var chat database.Chat
	err := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, userID, userID).First(&chat).Error
	return chat, err
}

func getSharedChatByUUID(DB *gorm.DB, shareUUID string) (database.Chat, database.SharedChatInstance, error) {
	var share database.SharedChatInstance
	err := DB.Where("chat_share_uuid = ?", shareUUID).First(&share).Error
	if err != nil {
		return database.Chat{}, database.SharedChatInstance{}, err
	}

	var chat database.Chat
	err = DB.Preload("User1").Preload("User2").Preload("SharedConfig").Where("id = ?", share.ChatId).First(&chat).Error
	if err != nil {
		return database.Chat{}, database.SharedChatInstance{}, err
	}

	return chat, share, nil
}
