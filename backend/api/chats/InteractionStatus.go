package chats

import (
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

func resolveInteractionStatus(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat) (InteractionStatusResponse, error) {
	response := InteractionStatusResponse{
		ChatUUID: chat.UUID,
		IsActive: false,
		State:    "idle",
		Source:   "none",
	}

	if inspector != nil {
		taskID := workqueue.BotReplyTaskID(chat.UUID)
		task, err := inspector.GetTaskInfo(workqueue.QueueDefault, taskID)
		if err == nil && task != nil {
			state := strings.ToLower(strings.TrimSpace(task.State.String()))
			if isQueueStateActive(state) {
				response.IsActive = true
				response.State = "active"
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

	if raw, ok := meta["finished"].(bool); ok {
		finished := raw
		response.LatestMessageFinished = &finished
		if finished {
			if errFlag, _ := meta["error"].(bool); errFlag {
				response.State = "failed"
			} else {
				response.State = "finished"
			}
			response.IsActive = false
			return response, nil
		}
	}

	response.State = "idle"
	response.IsActive = false
	return response, nil
}

func latestMessageForChat(DB *gorm.DB, chatID uint) (database.Message, error) {
	var message database.Message
	err := DB.Where("chat_id = ?", chatID).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		First(&message).Error
	return message, err
}

func isQueueStateActive(state string) bool {
	switch state {
	case "active", "pending", "scheduled", "retry", "processing":
		return true
	default:
		return false
	}
}
