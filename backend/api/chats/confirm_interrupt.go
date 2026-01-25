package chats

import (
	"encoding/json"
	"strings"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

// cancelPendingConfirmableActionsForChat voids every pending confirmable action
// a chat is currently parked on.
//
// A native coding agent pauses on the first mutating tool call, which completes
// the bot:reply task before the user ever presses stop. By then the asynq
// cancellation in the interrupt signal is a no-op, so the parked proposal would
// otherwise stay executable. Marking the actions (and their matching tool calls)
// cancelled makes the interactive stop authoritative: the confirm widget can no
// longer execute the action and the chat stops reporting a pending
// confirmation. The cancellation is recorded on the source message so the model
// sees the cancelled call in its tool-call context on the next turn.
//
// It returns the number of actions cancelled.
func cancelPendingConfirmableActionsForChat(db *gorm.DB, chatID uint, cancelledByUUID string) (int, error) {
	if db == nil || chatID == 0 {
		return 0, nil
	}

	var messages []database.Message
	if err := db.Where("chat_id = ?", chatID).Find(&messages).Error; err != nil {
		return 0, err
	}

	const cancelReason = "The response was stopped by the user."
	now := time.Now().UTC().Format(time.RFC3339)
	cancelled := 0

	for i := range messages {
		message := &messages[i]
		if len(message.MetaData) == 0 {
			continue
		}

		meta := map[string]interface{}{}
		if json.Unmarshal(message.MetaData, &meta) != nil {
			continue
		}

		rawActions, ok := meta["confirmable_actions"].([]interface{})
		if !ok || len(rawActions) == 0 {
			continue
		}

		changed := false
		cancelledIDs := map[string]bool{}
		for j, rawAction := range rawActions {
			action, ok := rawAction.(map[string]interface{})
			if !ok {
				continue
			}
			if status, _ := action["status"].(string); status != "pending" {
				continue
			}
			action["status"] = "cancelled"
			action["cancelled_at"] = now
			action["cancellation_reason"] = cancelReason
			if id, _ := action["action_id"].(string); id != "" {
				cancelledIDs[id] = true
			}
			if user := strings.TrimSpace(cancelledByUUID); user != "" {
				action["cancelled_by"] = user
			}
			rawActions[j] = action
			changed = true
			cancelled++
		}
		if !changed {
			continue
		}

		meta["confirmable_actions"] = rawActions
		if encoded, err := json.Marshal(meta); err == nil {
			_ = db.Model(&database.Message{}).Where("id = ?", message.ID).Update("meta_data", encoded).Error
		}

		if len(cancelledIDs) > 0 && message.ToolCalls != nil && len(*message.ToolCalls) > 0 {
			if updated, ok := cancelToolCalls(*message.ToolCalls, cancelledIDs, cancelReason); ok {
				_ = db.Model(&database.Message{}).Where("id = ?", message.ID).Update("tool_calls", updated).Error
			}
		}
	}

	return cancelled, nil
}

// cancelToolCalls flips the pending_confirmation status of the named tool calls
// to cancelled and records the reason, mirroring how the execute/reject paths
// settle a tool call once the parked action is resolved. ok is false when no
// tool call changed.
func cancelToolCalls(toolCalls []json.RawMessage, cancelledIDs map[string]bool, reason string) (string, bool) {
	updated := make([]json.RawMessage, 0, len(toolCalls))
	changed := false
	for _, raw := range toolCalls {
		call := map[string]interface{}{}
		if json.Unmarshal(raw, &call) != nil {
			updated = append(updated, raw)
			continue
		}
		id, _ := call["id"].(string)
		status, _ := call["status"].(string)
		if cancelledIDs[id] && status == "pending_confirmation" {
			call["status"] = "cancelled"
			call["error"] = reason
			if encoded, err := json.Marshal(call); err == nil {
				updated = append(updated, json.RawMessage(encoded))
				changed = true
				continue
			}
		}
		updated = append(updated, raw)
	}
	if !changed {
		return "", false
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}
