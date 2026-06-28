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
	UUID               string                 `json:"uuid"`
	ChatShareUUID      string                 `json:"chat_share_uuid"`
	ChatType           string                 `json:"chat_type"`
	PublishedAt        string                 `json:"published_at"`
	Partner            interface{}            `json:"partner"`
	Config             interface{}            `json:"config"`
	InteractionDetails map[string]interface{} `json:"interaction_details,omitempty"`
}

func ensureOwnedChatShare(DB *gorm.DB, chat database.Chat, owningUserID uint) (database.SharedChatInstance, error) {
	var share database.SharedChatInstance
	err := DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, owningUserID).First(&share).Error
	if err == nil {
		return share, nil
	}
	if err != gorm.ErrRecordNotFound {
		return database.SharedChatInstance{}, err
	}

	share = database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owningUserID,
		ChatShareUUID: uuid.NewString(),
	}
	if err := DB.Create(&share).Error; err != nil {
		return database.SharedChatInstance{}, err
	}

	return share, nil
}

// Publish creates (or returns) a public share UUID for a chat.
//
//	@Summary      Publish chat
//	@Description  Publish a chat and return its share UUID
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} SharedChatPublishResponse "Published chat info"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Failed to publish chat"
//	@Router       /api/chat/{chat_uuid}/publish [post]
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

	share, err := ensureOwnedChatShare(DB, chat, user.ID)
	if err != nil {
		http.Error(w, "Failed to publish chat", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SharedChatPublishResponse{ChatUUID: chat.UUID, ChatShareUUID: share.ChatShareUUID})
}

// Unpublish removes the public share UUID for a chat.
//
//	@Summary      Unpublish chat
//	@Description  Remove public chat sharing
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} map[string]bool "Unpublish result"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Failed to unpublish chat"
//	@Router       /api/chat/{chat_uuid}/unpublish [post]
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

// GetSharedInteraction returns a public view of a published interaction.
//
//	@Summary      Get shared interaction
//	@Description  Retrieve a published interaction chat by share UUID
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Success      200 {object} PublicInteractionChat "Public interaction chat"
//	@Failure      400 {string} string "Invalid shared chat UUID"
//	@Failure      404 {string} string "Shared chat not found"
//	@Router       /api/interaction/{chat_share_uuid} [get]
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

	chat, share, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		http.Error(w, "Shared chat not found", http.StatusNotFound)
		return
	}

	partner := chat.User2
	if chat.User1.IsAutomated {
		partner = chat.User1
	} else if chat.User2.IsAutomated {
		partner = chat.User2
	} else if partner.ID == 0 {
		partner = chat.User1
	}

	var config interface{}
	if chat.SharedConfig != nil {
		_ = json.Unmarshal(chat.SharedConfig.ConfigData, &config)
	}

	interactionDetails := map[string]interface{}{}
	if configMap, ok := config.(map[string]interface{}); ok {
		for _, key := range []string{"model", "backend", "max_tokens", "temperature", "context", "tools", "tool_init", "system_prompt"} {
			if value, exists := configMap[key]; exists {
				interactionDetails[key] = value
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PublicInteractionChat{
		UUID:          chat.UUID,
		ChatShareUUID: share.ChatShareUUID,
		ChatType:      chat.ChatType,
		PublishedAt:   share.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Partner: map[string]interface{}{
			"name":         partner.Name,
			"is_automated": partner.IsAutomated,
			"uuid":         partner.UUID,
		},
		Config:             config,
		InteractionDetails: interactionDetails,
	})
}

// ListSharedInteractionMessages returns paginated public messages for a shared interaction.
//
//	@Summary      List shared interaction messages
//	@Description  Retrieve paginated messages for a shared interaction chat
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Param        page query int false "Page number" default(1)
//	@Param        limit query int false "Page size" default(40)
//	@Success      200 {object} chats.ListedMessagesPage "Paginated list of shared messages"
//	@Failure      400 {string} string "Invalid shared chat UUID"
//	@Failure      404 {string} string "Shared chat not found"
//	@Failure      500 {string} string "Couldn't list shared messages"
//	@Router       /api/interaction/{chat_share_uuid}/messages [get]
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
	response := ListedMessagesPage{
		Limit:      pagination.Limit,
		Page:       pagination.Page,
		TotalPages: pagination.TotalPages,
		Rows:       listed,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
