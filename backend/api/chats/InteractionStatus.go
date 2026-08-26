package chats

import (
	"backend/chatstate"
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type InteractionStatusResponse struct {
	ChatUUID             string `json:"chat_uuid"`
	IsActive             bool   `json:"is_active"`
	State                string `json:"state"`
	LatestMessageUUID    string `json:"latest_message_uuid,omitempty"`
	LatestMessageFinished *bool  `json:"latest_message_finished,omitempty"`
	Source               string `json:"source"`
}

// GetInteractionStatus returns deterministic status for a private interaction chat.
//
//	@Summary      Get interaction status
//	@Description  Retrieve deterministic active/finished status for an interaction chat
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} InteractionStatusResponse "Interaction status"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Failure      500 {string} string "Unable to resolve interaction status"
//	@Router       /api/v1/chats/{chat_uuid}/status [get]
func (h *ChatsHandler) GetInteractionStatus(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Async queue unavailable", http.StatusInternalServerError)
		return
	}

	chatUUID := strings.TrimSpace(r.PathValue("chat_uuid"))
	if chatUUID == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	chat, err := findOwnedChat(DB, user.ID, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}

	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	status, err := resolveInteractionStatus(DB, inspector, chat)
	if err != nil {
		http.Error(w, "Unable to resolve interaction status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// GetSharedInteractionStatus returns deterministic status for a shared interaction.
//
//	@Summary      Get shared interaction status
//	@Description  Retrieve deterministic active/finished status for a shared interaction chat
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Success      200 {object} InteractionStatusResponse "Interaction status"
//	@Failure      400 {string} string "Invalid shared chat UUID"
//	@Failure      404 {string} string "Shared chat not found"
//	@Failure      500 {string} string "Unable to resolve interaction status"
//	@Router       /api/interaction/{chat_share_uuid}/status [get]
func (h *ChatsHandler) GetSharedInteractionStatus(w http.ResponseWriter, r *http.Request) {
	DB, err := util.GetDB(r)
	if err != nil {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	inspector, err := util.GetAsynqInspector(r)
	if err != nil {
		http.Error(w, "Async queue unavailable", http.StatusInternalServerError)
		return
	}

	shareUUID := strings.TrimSpace(r.PathValue("chat_share_uuid"))
	if shareUUID == "" {
		http.Error(w, "Invalid shared chat UUID", http.StatusBadRequest)
		return
	}

	chat, _, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		http.Error(w, "Shared chat not found", http.StatusNotFound)
		return
	}

	status, err := resolveInteractionStatus(DB, inspector, chat)
	if err != nil {
		http.Error(w, "Unable to resolve interaction status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// confirmationScanLimit is how many recent messages are scanned for pending
// user confirmations (the confirming message is usually the latest bot
// message, but a newer user message may sit on top of it).
const confirmationScanLimit = 5

func resolveInteractionStatus(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (InteractionStatusResponse, error) {
	response := InteractionStatusResponse{
		ChatUUID: chat.UUID,
		IsActive: false,
		State:    string(chatstate.StateIdle),
		Source:   "none",
	}

	// External chat backends (eg opencode) run generations outside the bot
	// reply queue, so their live activity is reported through a state
	// provider instead of the queue.
	if backend, ok := chatBackendName(DB, chat); ok {
		if provider, ok := chatstate.LookupBackendStateProvider(backend); ok {
			if state, ok := provider(chat.UUID); ok && state == chatstate.BackendStateRunning {
				response.IsActive = true
				response.State = string(chatstate.StateActive)
				response.Source = "chat_backend"
				return response, nil
			}
		}
	}

	if inspector != nil {
		taskID := workqueue.BotReplyTaskID(chat.UUID)
		task, err := inspector.GetTaskInfo(workqueue.QueueDefault, taskID)
		if err == nil && task != nil {
			state := strings.ToLower(strings.TrimSpace(task.State.String()))
			if isQueueStateActive(state) {
				response.IsActive = true
				response.State = string(chatstate.StateActive)
				response.Source = "queue"
				return response, nil
			}
		}
	}

	latest, err := latestMessageForChat(DB, chat.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response, nil
		}
		return InteractionStatusResponse{}, err
	}

	response.LatestMessageUUID = latest.UUID
	response.Source = "message_meta"

	meta := map[string]interface{}{}
	if len(latest.MetaData) > 0 {
		_ = json.Unmarshal(latest.MetaData, &meta)
	}

	finished, _ := meta["finished"].(bool)
	response.LatestMessageFinished = &finished

	// A finished bot message that still carries a pending user confirmation
	// (confirmable action or permission prompt) means the interaction is
	// waiting on the user, not done.
	if hasPendingConfirmationInRecentMessages(DB, chat.ID) {
		response.State = string(chatstate.StateNeedsConfirmation)
		response.IsActive = false
		return response, nil
	}

	if finished {
		if errFlag, _ := meta["error"].(bool); errFlag {
			response.State = string(chatstate.StateFailed)
		} else {
			response.State = string(chatstate.StateFinished)
		}
		response.IsActive = false
		return response, nil
	}

	response.State = string(chatstate.StateIdle)
	response.IsActive = false
	return response, nil
}

// chatBackendName returns the "backend" value from the chat's shared config.
func chatBackendName(DB *gorm.DB, chat database.Chat) (string, bool) {
	if chat.SharedConfig != nil && len(chat.SharedConfig.ConfigData) > 0 {
		config := map[string]interface{}{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &config); err == nil {
			if backend, ok := config["backend"].(string); ok && strings.TrimSpace(backend) != "" {
				return strings.TrimSpace(backend), true
			}
		}
	}
	var sharedConfig database.SharedChatConfig
	err := DB.Where("chat_id = ?", chat.ID).First(&sharedConfig).Error
	if err != nil || len(sharedConfig.ConfigData) == 0 {
		return "", false
	}
	config := map[string]interface{}{}
	if err := json.Unmarshal(sharedConfig.ConfigData, &config); err != nil {
		return "", false
	}
	backend, _ := config["backend"].(string)
	if strings.TrimSpace(backend) == "" {
		return "", false
	}
	return strings.TrimSpace(backend), true
}

func latestMessageForChat(DB *gorm.DB, chatID uint) (database.Message, error) {
	var message database.Message
	err := DB.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		First(&message).Error
	return message, err
}

func hasPendingConfirmationInRecentMessages(DB *gorm.DB, chatID uint) bool {
	var messages []database.Message
	err := DB.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Limit(confirmationScanLimit).
		Find(&messages).Error
	if err != nil {
		return false
	}
	for _, message := range messages {
		if messageHasPendingConfirmation(message) {
			return true
		}
	}
	return false
}

func messageHasPendingConfirmation(message database.Message) bool {
	if len(message.MetaData) > 0 {
		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) == nil {
			if actions, ok := meta["confirmable_actions"].([]interface{}); ok {
				for _, rawAction := range actions {
					action, ok := rawAction.(map[string]interface{})
					if !ok {
						continue
					}
					if status, _ := action["status"].(string); status == "pending" {
						return true
					}
				}
			}
			if permission, ok := meta["opencode_permission"].(map[string]interface{}); ok {
				if status, _ := permission["status"].(string); status == "pending" {
					return true
				}
			}
		}
	}
	if message.ToolCalls != nil {
		for _, rawToolCall := range *message.ToolCalls {
			toolCall := map[string]interface{}{}
			if json.Unmarshal(rawToolCall, &toolCall) != nil {
				continue
			}
			if status, _ := toolCall["status"].(string); status == "pending_confirmation" {
				return true
			}
		}
	}
	return false
}

func isQueueStateActive(state string) bool {
	switch state {
	case "active", "pending", "scheduled", "retry", "processing":
		return true
	default:
		return false
	}
}
