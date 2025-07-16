package chats

import (
	"encoding/json"
	"net/http"
	"time"

	"backend/database"
	"backend/server/util"
)

// StreamingMessageResponse mirrors a database.StreamingMessage row for the
// client, so it can hydrate the currently-streaming bot message after a page
// reload while the completion is still in flight.
type StreamingMessageResponse struct {
	UUID       string        `json:"uuid"`
	SessionID  string        `json:"session_id"`
	SenderUUID string        `json:"sender_uuid"`
	Text       string        `json:"text"`
	Reasoning  []string      `json:"reasoning"`
	ToolCalls  []interface{} `json:"tool_calls"`
	Seq        int64         `json:"seq"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// GetStreamingMessage returns the (optional) snapshot of the message that is
// currently being streamed into this chat. Responds with 204 when no stream
// is in flight. Stale snapshots (no updates for streamingSnapshotStaleAfter)
// are cleaned up and treated as absent.
//
//	@Summary      Get currently streaming message snapshot
//	@Description  Returns the throttled snapshot of the bot message currently being streamed, if any
//	@Tags         messages
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} chats.StreamingMessageResponse
//	@Success      204 {string} string "No streaming message in flight"
//	@Failure      400 {string} string "Invalid request"
//	@Router       /api/v1/chats/{chat_uuid}/messages/streaming [get]
func (h *ChatsHandler) GetStreamingMessage(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUuid := r.PathValue("chat_uuid")
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	var chat database.Chat
	if err := DB.Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat).Error; err != nil {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	// Opportunistically remove stale snapshots (e.g. after a worker crash).
	cutoff := time.Now().Add(-5 * time.Minute)
	DB.Unscoped().Where("updated_at < ?", cutoff).Delete(&database.StreamingMessage{})

	var snapshot database.StreamingMessage
	if err := DB.Preload("Sender").
		Where("chat_id = ?", chat.ID).
		Order("updated_at DESC").
		First(&snapshot).Error; err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	toolCalls := []interface{}{}
	for _, toolCall := range snapshot.ToolCalls {
		var toolCallData interface{}
		if err := json.Unmarshal(toolCall, &toolCallData); err == nil {
			toolCalls = append(toolCalls, toolCallData)
		}
	}

	response := StreamingMessageResponse{
		UUID:       snapshot.UUID,
		SessionID:  snapshot.SessionID,
		SenderUUID: snapshot.Sender.UUID,
		Text:       snapshot.Text,
		Reasoning:  snapshot.Reasoning,
		ToolCalls:  toolCalls,
		Seq:        snapshot.Seq,
		UpdatedAt:  snapshot.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
