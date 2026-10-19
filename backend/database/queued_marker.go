package database

import (
	"encoding/json"

	"gorm.io/gorm"
)

// QueueStateMetaKey is the message metadata key the chat transcript reads to
// render a message as pending rather than processed (ci#656). The frontend
// consumes it in `frontend/packages/ui/src/integration/lib/status-indicator.ts`
// (isQueuePendingMessage reads meta_data.queue_state).
const QueueStateMetaKey = "queue_state"

// QueueStateQueued marks a message whose bot reply is queued behind a live
// turn: it is persisted and visible immediately, but not consumed yet.
const QueueStateQueued = "queued"

// MarkMessageQueued stamps the queued marker into a metadata map and reports
// whether the map changed.
func MarkMessageQueued(meta map[string]interface{}) bool {
	if meta == nil {
		return false
	}
	if state, ok := meta[QueueStateMetaKey].(string); ok && state == QueueStateQueued {
		return false
	}
	meta[QueueStateMetaKey] = QueueStateQueued
	return true
}

// ClearMarkerFromMeta removes the queued marker from a metadata map and reports
// whether the map changed. Other metadata keys are preserved.
func ClearMarkerFromMeta(meta map[string]interface{}) bool {
	if meta == nil {
		return false
	}
	if _, ok := meta[QueueStateMetaKey]; !ok {
		return false
	}
	delete(meta, QueueStateMetaKey)
	return true
}

// IsMessageQueued reports whether a message carries the queued marker.
func IsMessageQueued(message Message) bool {
	if len(message.MetaData) == 0 {
		return false
	}
	state, _ := messageMeta(message)[QueueStateMetaKey].(string)
	return state == QueueStateQueued
}

func messageMeta(message Message) map[string]interface{} {
	meta := map[string]interface{}{}
	if len(message.MetaData) > 0 {
		_ = json.Unmarshal(message.MetaData, &meta)
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	return meta
}

// persistMessageMeta writes the encoded metadata and mirrors it onto the
// in-memory message, so the caller's response and the persisted row agree.
func persistMessageMeta(DB *gorm.DB, message *Message, meta map[string]interface{}) error {
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := DB.Model(&Message{}).Where("uuid = ?", message.UUID).Update("meta_data", JSONRaw(encoded)).Error; err != nil {
		return err
	}
	message.MetaData = JSONRaw(encoded)
	return nil
}

// SetMessageQueued marks a message as queued (ci#656): it was sent into a chat
// that is already running a turn, so its reply waits behind that turn. Reports
// whether the marker was applied.
func SetMessageQueued(DB *gorm.DB, message *Message) (bool, error) {
	if DB == nil || message == nil || message.UUID == "" {
		return false, nil
	}
	meta := messageMeta(*message)
	if !MarkMessageQueued(meta) {
		return false, nil
	}
	return true, persistMessageMeta(DB, message, meta)
}

// ClearMessageQueued removes the queued marker from a message whose turn has
// been picked up by a worker, so it stops rendering as pending. It is a no-op
// for an unmarked message. Reports whether the marker was removed.
func ClearMessageQueued(DB *gorm.DB, message *Message) (bool, error) {
	if DB == nil || message == nil || message.UUID == "" {
		return false, nil
	}
	meta := messageMeta(*message)
	if !ClearMarkerFromMeta(meta) {
		return false, nil
	}
	return true, persistMessageMeta(DB, message, meta)
}

// ClearMessageQueuedForChat removes the queued marker from every marked message
// of a chat. An explicit interrupt supersedes the replies that were queued
// behind the stopped turn - they are never consumed - so they must not keep
// rendering as queued. Reports how many messages were cleared.
func ClearMessageQueuedForChat(DB *gorm.DB, chatID uint) (int64, error) {
	if DB == nil || chatID == 0 {
		return 0, nil
	}
	var messages []Message
	if err := DB.Where("chat_id = ?", chatID).Find(&messages).Error; err != nil {
		return 0, err
	}
	var cleared int64
	for i := range messages {
		if _, ok := messageMeta(messages[i])[QueueStateMetaKey]; !ok {
			continue
		}
		message := messages[i]
		changed, err := ClearMessageQueued(DB, &message)
		if err != nil {
			return cleared, err
		}
		if changed {
			cleared++
		}
	}
	return cleared, nil
}
