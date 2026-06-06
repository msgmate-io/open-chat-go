package chats

import (
	"backend/database"
	"backend/server/util"
	"backend/sharecensor"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type SharedChatPublishResponse struct {
	ChatUUID       string `json:"chat_uuid"`
	ChatShareUUID  string `json:"chat_share_uuid"`
	AccessMode     string `json:"access_mode"`
	TargetUsername string `json:"target_username,omitempty"`
	TargetUserUUID string `json:"target_user_uuid,omitempty"`
	ContentMode    string `json:"content_mode"`
}

// publishChatRequest optionally scopes a share. An omitted/empty body keeps
// the default fully-public share behaviour.
type publishChatRequest struct {
	// AccessMode is "public" (default) or "user".
	AccessMode string `json:"access_mode"`
	// ContentMode selects the share exposure: "censored" (default) or "unsecure".
	ContentMode string `json:"content_mode"`
	// TargetUsername/TargetUserUUID identify the single user a "user" share is
	// scoped to.
	TargetUsername string `json:"target_username"`
	TargetUserUUID string `json:"target_user_uuid"`
}

func normalizeShareAccessMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "user") {
		return "user"
	}
	return "public"
}

// shareAllowsUser reports whether a viewer may open a share. Public shares are
// open to everyone; user-scoped shares require the owner or the target user.
func shareAllowsUser(share database.SharedChatInstance, user *database.User) bool {
	if share.IsPublic() {
		return true
	}
	if user == nil {
		return false
	}
	return share.AllowsUser(user.ID)
}

// findShareTargetUser resolves the user a per-user share is scoped to by
// username (case-insensitive) or UUID.
func findShareTargetUser(DB *gorm.DB, username, userUUID string) (database.User, error) {
	var user database.User
	username = strings.TrimSpace(username)
	userUUID = strings.TrimSpace(userUUID)
	if userUUID != "" {
		err := DB.Where("uuid = ?", userUUID).First(&user).Error
		return user, err
	}
	err := DB.Where("LOWER(username) = ?", strings.ToLower(username)).First(&user).Error
	return user, err
}

type PublicInteractionChat struct {
	UUID               string                 `json:"uuid"`
	ChatShareUUID      string                 `json:"chat_share_uuid"`
	ContentMode        string                 `json:"content_mode"`
	ChatType           string                 `json:"chat_type"`
	PublishedAt        string                 `json:"published_at"`
	Partner            interface{}            `json:"partner"`
	Config             interface{}            `json:"config"`
	InteractionDetails map[string]interface{} `json:"interaction_details,omitempty"`
	// FollowUp describes the follow-up interaction this interaction handed off
	// to (e.g. the agent a runtime-selector/chooser bot picked), so a viewer
	// clicking the interaction badge can be redirected to it.
	FollowUp *PublicFollowUp `json:"follow_up,omitempty"`
}

func ensureOwnedChatShare(DB *gorm.DB, chat database.Chat, owningUserID uint, accessMode string, targetUserID *uint, contentMode string) (database.SharedChatInstance, error) {
	accessMode = normalizeShareAccessMode(accessMode)
	var share database.SharedChatInstance
	query := DB.Where("chat_id = ? AND owning_user_id = ?", chat.ID, owningUserID)
	if accessMode == "user" {
		query = query.Where("access_mode = ? AND target_user_id = ?", "user", targetUserID)
	} else {
		// Legacy rows predate AccessMode and therefore store an empty value;
		// treat them as the public share so publishing reuses them.
		query = query.Where("(access_mode = ? OR access_mode = '' OR access_mode IS NULL)", "public")
	}
	err := query.First(&share).Error
	if err == nil {
		if sharecensor.NormalizeContentMode(share.ContentMode) != contentMode {
			share.ContentMode = contentMode
			if err := DB.Model(&share).Update("content_mode", contentMode).Error; err != nil {
				return database.SharedChatInstance{}, err
			}
		}
		return share, nil
	}
	if err != gorm.ErrRecordNotFound {
		return database.SharedChatInstance{}, err
	}

	share = database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owningUserID,
		ChatShareUUID: uuid.NewString(),
		ContentMode:   contentMode,
		AccessMode:    accessMode,
		TargetUserId:  targetUserID,
	}
	if err := DB.Create(&share).Error; err != nil {
		return database.SharedChatInstance{}, err
	}

	return share, nil
}

// Publish creates (or returns) a share UUID for a chat. An empty body (or
// access_mode "public") yields a public share; access_mode "user" plus a target
// username/UUID yields a share only that user (and the owner) may open.
//
//	@Summary      Publish chat
//	@Description  Publish a chat and return its share UUID
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        request body chats.publishChatRequest false "Optional share scope"
//	@Success      200 {object} SharedChatPublishResponse "Published chat info"
//	@Failure      400 {string} string "Invalid chat UUID or share target"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Failed to publish chat"
//	@Router       /api/chat/{chat_uuid}/publish [post]
func (h *ChatsHandler) Publish(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID, chatUUIDErr := parseChatUUID(r)
	if chatUUIDErr != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	chat, err := findOwnedChat(DB, user.ID, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	var payload publishChatRequest
	if r.Body != nil {
		if decodeErr := json.NewDecoder(r.Body).Decode(&payload); decodeErr != nil && decodeErr != io.EOF {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	}

	accessMode := normalizeShareAccessMode(payload.AccessMode)
	contentMode := sharecensor.NormalizeContentMode(payload.ContentMode)
	var targetUserID *uint
	targetUsername := ""
	targetUserUUID := ""
	if accessMode == "user" {
		target, targetErr := findShareTargetUser(DB, payload.TargetUsername, payload.TargetUserUUID)
		if targetErr != nil {
			http.Error(w, "Share target user not found", http.StatusBadRequest)
			return
		}
		targetUserID = &target.ID
		targetUsername = target.Username
		targetUserUUID = target.UUID
	}

	share, err := ensureOwnedChatShare(DB, chat, user.ID, accessMode, targetUserID, contentMode)
	if err != nil {
		http.Error(w, "Failed to publish chat", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SharedChatPublishResponse{
		ChatUUID:       chat.UUID,
		ChatShareUUID:  share.ChatShareUUID,
		ContentMode:    share.ContentMode,
		AccessMode:     share.AccessMode,
		TargetUsername: targetUsername,
		TargetUserUUID: targetUserUUID,
	})
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

	chatUUID, chatUUIDErr := parseChatUUID(r)
	if chatUUIDErr != nil {
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

	// A censored share must not expose the model configuration beyond the
	// non-sensitive knobs: the system prompt and the tool init parameters may
	// embed credentials or private workspace paths, so they are dropped (and the
	// raw shared config is never returned as-is in censored mode).
	censored := share.IsCensored()
	if censored {
		for _, key := range []string{"system_prompt", "tool_init"} {
			delete(interactionDetails, key)
		}
		if _, ok := config.(map[string]interface{}); ok {
			config = interactionDetails
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PublicInteractionChat{
		UUID:          chat.UUID,
		ChatShareUUID: share.ChatShareUUID,
		ContentMode:   sharecensor.NormalizeContentMode(share.ContentMode),
		ChatType:      chat.ChatType,
		PublishedAt:   share.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Partner: map[string]interface{}{
			"name":         partner.Name,
			"is_automated": partner.IsAutomated,
			"uuid":         partner.UUID,
		},
		Config:             config,
		InteractionDetails: interactionDetails,
		FollowUp:           publicFollowUpForInteraction(DB, chat),
	})
}

type SharedInteractionAccessResponse struct {
	Authenticated bool   `json:"authenticated"`
	HasAccess     bool   `json:"has_access"`
	ChatUUID      string `json:"chat_uuid,omitempty"`
	ContentMode   string `json:"content_mode,omitempty"`
}

// GetSharedInteractionAccess reports whether the current viewer is
// authenticated and whether the viewer can open the full interaction chat.
//
//	@Summary      Get shared interaction access
//	@Description  Report whether the viewer is authenticated and has access to the full interaction
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Success      200 {object} SharedInteractionAccessResponse "Viewer access info"
//	@Router       /api/interaction/{chat_share_uuid}/access [get]
func (h *ChatsHandler) GetSharedInteractionAccess(w http.ResponseWriter, r *http.Request) {
	response := SharedInteractionAccessResponse{}
	user := util.GetOptionalUser(r)
	response.Authenticated = user != nil

	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	shareUUID := r.PathValue("chat_share_uuid")
	if shareUUID != "" {
		if chat, accessShare, chatErr := getSharedChatByUUID(DB, shareUUID); chatErr == nil {
			response.ContentMode = accessShare.ContentMode
			response.ChatUUID = chat.UUID
			if user != nil && (chat.User1Id == user.ID || chat.User2Id == user.ID) {
				response.HasAccess = true
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(response)
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

	chat, share, err := getSharedChatByUUID(DB, shareUUID)
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
	writeSharedMessagesJSON(w, response, share.IsCensored())
}

func findOwnedChat(DB *gorm.DB, userID uint, chatUUID string) (database.Chat, error) {
	var chat database.Chat
	err := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, userID, userID).First(&chat).Error
	return chat, err
}

func getSharedChatByUUID(DB *gorm.DB, shareUUID string) (database.Chat, database.SharedChatInstance, error) {
	var share database.SharedChatInstance
	err := DB.Where("chat_share_uuid = ?", shareUUID).First(&share).Error
	if err == gorm.ErrRecordNotFound {
		var chat database.Chat
		chatErr := DB.Preload("User1").Preload("User2").Preload("SharedConfig").
			Where("uuid = ? AND chat_type = ?", shareUUID, "interaction").
			First(&chat).Error
		if chatErr != nil {
			return database.Chat{}, database.SharedChatInstance{}, chatErr
		}

		shareErr := DB.Where("chat_id = ?", chat.ID).Order("created_at DESC").First(&share).Error
		if shareErr != nil {
			return database.Chat{}, database.SharedChatInstance{}, shareErr
		}

		return chat, share, nil
	}
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

// writeSharedMessagesJSON serialises a shared message payload and, when the
// owning share is in the default censored content mode, strips tool-call
// arguments/results, meta-data, reasoning and any secret-looking key before the
// bytes are written to the response.
func writeSharedMessagesJSON(w http.ResponseWriter, v interface{}, censored bool) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if censored {
		raw = sharecensor.CensorSharedJSON(raw)
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(raw)
	return err
}
