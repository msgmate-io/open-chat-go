package chats

import (
	"backend/database"
	"backend/server/util"
	"backend/workqueue"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
)

// interactionConfirmationMetaKey is the message meta key that carries the
// interaction confirmation widget state.
const interactionConfirmationMetaKey = "interaction_confirmation"

// InteractionConfirmationStatus values stored in the widget meta.
const (
	InteractionConfirmationPending  = "pending"
	InteractionConfirmationApproved = "approved"
	InteractionConfirmationRejected = "rejected"
)

type interactionConfirmationMeta struct {
	Status            string `json:"status"`
	SourceMessageUUID string `json:"source_message_uuid"`
	MessageUUID       string `json:"message_uuid,omitempty"`
	BotUUID           string `json:"bot_uuid,omitempty"`
	Title             string `json:"title,omitempty"`
	Description       string `json:"description,omitempty"`
	ChatUUID          string `json:"chat_uuid,omitempty"`
	DecidedBy         string `json:"decided_by,omitempty"`
	DecidedAt         string `json:"decided_at,omitempty"`
}

// BuildInteractionConfirmationMessage creates the bot message that renders the
// confirmation widget inside an interaction chat. The bot reply is enqueued
// only after the user approves the confirmation.
func BuildInteractionConfirmationMessage(
	tx *gorm.DB,
	chat database.Chat,
	botUserID uint,
	userID uint,
	botUUID string,
	sourceMessage database.Message,
	title string,
	description string,
) (database.Message, error) {
	text := title
	if text == "" {
		text = "Start coding interaction?"
	}
	confirmation := database.Message{
		ChatId:     chat.ID,
		SenderId:   botUserID,
		ReceiverId: userID,
		DataType:   "text",
		Text:       &text,
	}
	if err := tx.Create(&confirmation).Error; err != nil {
		return database.Message{}, err
	}

	// Record the confirmation message's own UUID in the widget meta so the
	// frontend can address the approve/reject endpoint without a separate
	// message lookup.
	metaBytes, err := json.Marshal(map[string]interface{}{
		"finished": true,
		interactionConfirmationMetaKey: interactionConfirmationMeta{
			Status:            InteractionConfirmationPending,
			SourceMessageUUID: sourceMessage.UUID,
			MessageUUID:       confirmation.UUID,
			BotUUID:           botUUID,
			Title:             title,
			Description:       description,
			ChatUUID:          chat.UUID,
		},
	})
	if err != nil {
		return database.Message{}, err
	}
	if err := tx.Model(&confirmation).Update("meta_data", database.JSONRaw(metaBytes)).Error; err != nil {
		return database.Message{}, err
	}
	confirmation.MetaData = database.JSONRaw(metaBytes)
	return confirmation, nil
}

// InteractionConfirmationResponse is returned by the approve/reject handlers.
type InteractionConfirmationResponse struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
	Queued  bool   `json:"queued"`
	State   string `json:"state,omitempty"`
}

// ApproveInteractionConfirmation approves a pending interaction confirmation
// and enqueues the deferred bot reply.
//
//	@Summary      Approve interaction confirmation
//	@Description  Approve a pending interaction confirmation and start the bot reply.
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        message_uuid path string true "Confirmation message UUID"
//	@Success      200 {object} chats.InteractionConfirmationResponse
//	@Failure      400 {string} string "Invalid request"
//	@Failure      403 {string} string "Forbidden"
//	@Failure      404 {string} string "Not found"
//	@Failure      409 {string} string "Conflict"
//	@Router       /api/v1/chats/{chat_uuid}/messages/{message_uuid}/interaction-confirmation/approve [post]
func (h *ChatsHandler) ApproveInteractionConfirmation(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	queueClient, clientErr := util.GetAsynqClient(r)
	queueInspector, inspectorErr := util.GetAsynqInspector(r)
	if clientErr != nil || inspectorErr != nil {
		http.Error(w, "Async queue unavailable", http.StatusInternalServerError)
		return
	}

	chat, message, meta, ok := loadPendingInteractionConfirmation(w, r, DB, user.ID)
	if !ok {
		return
	}

	botUser, counterpartyOK := getChatCounterparty(chat, *user)
	if !counterpartyOK || !botUser.IsAutomated {
		http.Error(w, "Confirmation is only available in chats with bots", http.StatusConflict)
		return
	}

	sourceMessageUUID := meta.SourceMessageUUID
	if sourceMessageUUID == "" {
		http.Error(w, "Confirmation has no source message", http.StatusConflict)
		return
	}

	meta.Status = InteractionConfirmationApproved
	meta.DecidedBy = user.UUID
	meta.DecidedAt = time.Now().UTC().Format(time.RFC3339)
	if err := persistInteractionConfirmationMeta(DB, message, meta); err != nil {
		http.Error(w, "Failed to update confirmation", http.StatusInternalServerError)
		return
	}

	if _, enqueueErr := workqueue.EnqueueBotReply(queueClient, queueInspector, workqueue.BotReplyPayload{
		ChatUUID:    chat.UUID,
		MessageUUID: sourceMessageUUID,
		BotUserID:   botUser.ID,
	}); enqueueErr != nil {
		http.Error(w, "Failed to enqueue bot reply", http.StatusInternalServerError)
		return
	}

	writeInteractionConfirmationResponse(w, InteractionConfirmationResponse{
		Success: true,
		Status:  InteractionConfirmationApproved,
		Queued:  true,
		State:   "active",
	})
}

// RejectInteractionConfirmation rejects a pending interaction confirmation and
// immediately fails the interaction.
//
//	@Summary      Reject interaction confirmation
//	@Description  Reject a pending interaction confirmation; the interaction fails immediately.
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        message_uuid path string true "Confirmation message UUID"
//	@Success      200 {object} chats.InteractionConfirmationResponse
//	@Failure      400 {string} string "Invalid request"
//	@Failure      403 {string} string "Forbidden"
//	@Failure      404 {string} string "Not found"
//	@Failure      409 {string} string "Conflict"
//	@Router       /api/v1/chats/{chat_uuid}/messages/{message_uuid}/interaction-confirmation/reject [post]
func (h *ChatsHandler) RejectInteractionConfirmation(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chat, message, meta, ok := loadPendingInteractionConfirmation(w, r, DB, user.ID)
	if !ok {
		return
	}

	meta.Status = InteractionConfirmationRejected
	meta.DecidedBy = user.UUID
	meta.DecidedAt = time.Now().UTC().Format(time.RFC3339)

	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := persistInteractionConfirmationMeta(tx, message, meta); err != nil {
			return err
		}
		eventMeta, marshalErr := json.Marshal(map[string]interface{}{
			"finished":                        true,
			"error":                           true,
			"event_type":                      "interaction_confirmation",
			"event_phase":                     InteractionConfirmationRejected,
			"interaction_confirmation_status": InteractionConfirmationRejected,
		})
		if marshalErr != nil {
			return marshalErr
		}
		eventText := "The interaction was rejected by the user."
		event := database.Message{
			ChatId:     chat.ID,
			SenderId:   message.SenderId,
			ReceiverId: message.ReceiverId,
			DataType:   "event",
			Text:       &eventText,
			MetaData:   database.JSONRaw(eventMeta),
		}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		return tx.Model(&chat).Update("latest_message_id", event.ID).Error
	})
	if err != nil {
		http.Error(w, "Failed to reject confirmation", http.StatusInternalServerError)
		return
	}

	writeInteractionConfirmationResponse(w, InteractionConfirmationResponse{
		Success: true,
		Status:  InteractionConfirmationRejected,
		Queued:  false,
		State:   "failed",
	})
}

func loadPendingInteractionConfirmation(w http.ResponseWriter, r *http.Request, DB *gorm.DB, userID uint) (database.Chat, database.Message, *interactionConfirmationMeta, bool) {
	chatUUID := r.PathValue("chat_uuid")
	messageUUID := r.PathValue("message_uuid")
	if chatUUID == "" || messageUUID == "" {
		http.Error(w, "Invalid chat/message UUID", http.StatusBadRequest)
		return database.Chat{}, database.Message{}, nil, false
	}

	var chat database.Chat
	if err := DB.Preload("User1").
		Preload("User2").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUUID, userID, userID).
		First(&chat).Error; err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return database.Chat{}, database.Message{}, nil, false
	}

	var message database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return database.Chat{}, database.Message{}, nil, false
		}
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return database.Chat{}, database.Message{}, nil, false
	}

	meta, err := parseInteractionConfirmationMeta(message)
	if err != nil {
		http.Error(w, "Message is not an interaction confirmation", http.StatusConflict)
		return database.Chat{}, database.Message{}, nil, false
	}
	if meta.Status != InteractionConfirmationPending {
		http.Error(w, "Confirmation has already been resolved", http.StatusConflict)
		return database.Chat{}, database.Message{}, nil, false
	}
	return chat, message, meta, true
}

func parseInteractionConfirmationMeta(message database.Message) (*interactionConfirmationMeta, error) {
	if len(message.MetaData) == 0 {
		return nil, errors.New("no meta")
	}
	raw := map[string]interface{}{}
	if err := json.Unmarshal(message.MetaData, &raw); err != nil {
		return nil, err
	}
	entry, ok := raw[interactionConfirmationMetaKey].(map[string]interface{})
	if !ok {
		return nil, errors.New("no interaction confirmation")
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var meta interactionConfirmationMeta
	if err := json.Unmarshal(encoded, &meta); err != nil {
		return nil, err
	}
	if meta.Status == "" {
		meta.Status = InteractionConfirmationPending
	}
	return &meta, nil
}

func persistInteractionConfirmationMeta(DB *gorm.DB, message database.Message, meta *interactionConfirmationMeta) error {
	raw := map[string]interface{}{}
	if len(message.MetaData) > 0 {
		_ = json.Unmarshal(message.MetaData, &raw)
	}
	raw[interactionConfirmationMetaKey] = meta
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return DB.Model(&database.Message{}).Where("id = ?", message.ID).Update("meta_data", database.JSONRaw(encoded)).Error
}

func writeInteractionConfirmationResponse(w http.ResponseWriter, response InteractionConfirmationResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
