package chats

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// runtimeSelectorsMetaKey is the message meta key that carries the runtime
// selection widget state. It holds either an array of selector objects or a
// single selector object (tolerated for older emitters).
const runtimeSelectorsMetaKey = "runtime_selectors"

// Runtime selector status values persisted back into the message meta.
const (
	RuntimeSelectorPending   = "pending"
	RuntimeSelectorConfirmed = "confirmed"
	RuntimeSelectorCancelled = "cancelled"
)

// resolveRuntimeSelectorRequest is the body accepted by the resolve endpoint.
// The widget creates the interaction client-side and then records the decision
// (with the resulting interaction identifiers) so a reload shows the resolved
// state instead of the pending widget.
type resolveRuntimeSelectorRequest struct {
	SelectorID            string `json:"selector_id"`
	Decision              string `json:"decision"`
	StartedChatUUID       string `json:"started_chat_uuid,omitempty"`
	StartedInteractionURL string `json:"started_interaction_url,omitempty"`
}

// ResolveRuntimeSelectorResponse is returned by the resolve handler.
type ResolveRuntimeSelectorResponse struct {
	Success  bool                   `json:"success"`
	Status   string                 `json:"status"`
	Selector map[string]interface{} `json:"selector"`
}

// parseRuntimeSelectorsFromMeta extracts the selector objects from a message
// meta map. Both the array form and the single-object form are accepted.
func parseRuntimeSelectorsFromMeta(meta map[string]interface{}) []map[string]interface{} {
	if meta == nil {
		return nil
	}
	raw, ok := meta[runtimeSelectorsMetaKey]
	if !ok {
		return nil
	}
	selectors := []map[string]interface{}{}
	switch typed := raw.(type) {
	case []interface{}:
		for _, entry := range typed {
			if selector, ok := entry.(map[string]interface{}); ok {
				selectors = append(selectors, selector)
			}
		}
	case map[string]interface{}:
		selectors = append(selectors, typed)
	}
	return selectors
}

// findRuntimeSelector picks the selector a decision targets. An explicit id
// wins; without one (eg a selector persisted before ids existed) the first
// still-pending entry is used.
func findRuntimeSelector(selectors []map[string]interface{}, selectorID string) map[string]interface{} {
	if selectorID != "" {
		for _, selector := range selectors {
			if actionTaskStringField(selector, "id") == selectorID {
				return selector
			}
		}
	}
	for _, selector := range selectors {
		status, _ := selector["status"].(string)
		if status == "" || status == RuntimeSelectorPending {
			return selector
		}
	}
	return nil
}

// persistRuntimeSelectorMeta writes the mutated selector list back into the
// message meta while preserving every unrelated meta key.
func persistRuntimeSelectorMeta(DB *gorm.DB, message database.Message, meta map[string]interface{}, selectors []map[string]interface{}) error {
	encodedSelectors := make([]interface{}, 0, len(selectors))
	for _, selector := range selectors {
		encodedSelectors = append(encodedSelectors, selector)
	}
	meta[runtimeSelectorsMetaKey] = encodedSelectors
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return DB.Model(&database.Message{}).Where("id = ?", message.ID).Update("meta_data", database.JSONRaw(encoded)).Error
}

// ResolveRuntimeSelector records the user's runtime choice on the message that
// carries the selector, so the pending action clears and a reload renders the
// resolved state.
//
//	@Summary      Resolve runtime selector
//	@Description  Record the user's decision (started or cancelled) for a runtime selector carried by a message.
//	@Tags         messages
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        message_uuid path string true "Selector message UUID"
//	@Success      200 {object} chats.ResolveRuntimeSelectorResponse
//	@Failure      400 {string} string "Invalid request"
//	@Failure      403 {string} string "Forbidden"
//	@Failure      404 {string} string "Not found"
//	@Failure      409 {string} string "Conflict"
//	@Router       /api/v1/chats/{chat_uuid}/messages/{message_uuid}/runtime-selector/resolve [post]
func (h *ChatsHandler) ResolveRuntimeSelector(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	chatUUID := strings.TrimSpace(r.PathValue("chat_uuid"))
	messageUUID := strings.TrimSpace(r.PathValue("message_uuid"))
	if chatUUID == "" || messageUUID == "" {
		http.Error(w, "Invalid chat/message UUID", http.StatusBadRequest)
		return
	}

	chat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		http.Error(w, "Chat not found", http.StatusNotFound)
		return
	}
	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return
	}

	var message database.Message
	if err := DB.Where("uuid = ? AND chat_id = ?", messageUUID, chat.ID).First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load message", http.StatusInternalServerError)
		return
	}

	meta := map[string]interface{}{}
	if len(message.MetaData) > 0 {
		if err := json.Unmarshal(message.MetaData, &meta); err != nil {
			http.Error(w, "Message is not a runtime selector", http.StatusConflict)
			return
		}
	}
	selectors := parseRuntimeSelectorsFromMeta(meta)
	if len(selectors) == 0 {
		http.Error(w, "Message has no runtime selector", http.StatusConflict)
		return
	}

	var req resolveRuntimeSelectorRequest
	if err := decodeOptionalJSONBody(r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req.SelectorID = strings.TrimSpace(req.SelectorID)
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "started" && decision != "cancelled" {
		http.Error(w, "decision must be started or cancelled", http.StatusBadRequest)
		return
	}

	selector := findRuntimeSelector(selectors, req.SelectorID)
	if selector == nil {
		http.Error(w, "Runtime selector not found", http.StatusNotFound)
		return
	}
	if status, _ := selector["status"].(string); status != "" && status != RuntimeSelectorPending {
		http.Error(w, "Runtime selector has already been resolved", http.StatusConflict)
		return
	}

	startedChatUUID := strings.TrimSpace(req.StartedChatUUID)
	startedURL := strings.TrimSpace(req.StartedInteractionURL)
	if decision == "started" && startedChatUUID != "" {
		startedChat, err := findAccessibleChat(DB, user, startedChatUUID)
		if err != nil {
			http.Error(w, "Started interaction chat not found", http.StatusConflict)
			return
		}
		if !isInteractionChatType(startedChat.ChatType) {
			http.Error(w, "Started chat is not an interaction", http.StatusConflict)
			return
		}
	}

	status := RuntimeSelectorConfirmed
	if decision == "cancelled" {
		status = RuntimeSelectorCancelled
	}
	selector["status"] = status
	selector["decided_by"] = user.UUID
	selector["decided_at"] = time.Now().UTC().Format(time.RFC3339)
	if decision == "started" {
		if startedChatUUID != "" {
			selector["started_chat_uuid"] = startedChatUUID
		}
		if startedURL != "" {
			selector["started_interaction_url"] = startedURL
		}
	}

	if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
		http.Error(w, "Failed to update runtime selector", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ResolveRuntimeSelectorResponse{
		Success:  true,
		Status:   status,
		Selector: selector,
	})
}
