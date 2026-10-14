package msgmate

import (
	"encoding/json"
	"log"
	"time"

	"backend/database"

	"gorm.io/gorm"
)

const (
	// streamingSnapshotMinInterval throttles DB writes of the in-flight
	// streaming snapshot. One small row update per second per active stream
	// is negligible next to the SSE traffic itself.
	streamingSnapshotMinInterval = 1 * time.Second
	// streamingSnapshotStaleAfter marks hydrated snapshots as stale clientside.
	streamingSnapshotStaleAfter = 5 * time.Minute
)

// partialMessagePersister keeps a throttled snapshot of the currently
// streamed bot message in the streaming_messages table. It is a no-op when no
// global DB handle is available. All methods are safe to call on a nil
// receiver.
type partialMessagePersister struct {
	db        *gorm.DB
	chatUUID  string
	sessionID string
	senderID  uint
	chatID    uint
	rowID     uint
	lastFlush time.Time
	dirty     bool
}

// newPartialMessagePersister resolves the chat and creates the initial
// snapshot row. Returns nil when persistence is not possible (no DB handle,
// unknown chat); callers must therefore nil-check.
func newPartialMessagePersister(chatUUID, sessionID string, senderID uint) *partialMessagePersister {
	db := database.GetGlobalDB()
	if db == nil || chatUUID == "" || sessionID == "" {
		return nil
	}
	var chat database.Chat
	if err := db.Where("uuid = ?", chatUUID).First(&chat).Error; err != nil {
		return nil
	}
	row := &database.StreamingMessage{
		ChatId:    chat.ID,
		SessionID: sessionID,
		SenderId:  senderID,
	}
	if chat.User1Id == senderID {
		row.ReceiverId = chat.User2Id
	} else {
		row.ReceiverId = chat.User1Id
	}
	if err := db.Create(row).Error; err != nil {
		log.Printf("streaming persistence: failed to create snapshot row (chat %s): %v", chatUUID, err)
		return nil
	}
	return &partialMessagePersister{
		db:        db,
		chatUUID:  chatUUID,
		rowID:     row.ID,
		lastFlush: time.Now(),
	}
}

// snapshot persists the accumulated stream state, throttled to at most one
// write per streamingSnapshotMinInterval. seq is the current partial-message
// sequence number so clients can reconcile hydrated snapshots with
// subsequent live deltas.
func (p *partialMessagePersister) snapshot(seq int64, text string, reasoning []string, toolCalls []interface{}) {
	if p == nil {
		return
	}
	p.dirty = true
	if time.Since(p.lastFlush) < streamingSnapshotMinInterval {
		return
	}
	p.flush(seq, text, reasoning, toolCalls)
}

// flushNow writes the snapshot regardless of throttling.
func (p *partialMessagePersister) flushNow(seq int64, text string, reasoning []string, toolCalls []interface{}) {
	if p == nil {
		return
	}
	p.flush(seq, text, reasoning, toolCalls)
}

func (p *partialMessagePersister) flush(seq int64, text string, reasoning []string, toolCalls []interface{}) {
	toolCallsJSON := make([]json.RawMessage, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		encoded, err := json.Marshal(toolCall)
		if err != nil {
			continue
		}
		toolCallsJSON = append(toolCallsJSON, encoded)
	}

	updates := map[string]interface{}{
		"seq":  seq,
		"text": text,
	}
	// GORM map updates bypass the struct field JSON serializers, so []string /
	// []json.RawMessage values reach the driver as raw slices. SQLite rejects
	// that with "SQL logic error: row value misused (1)" and silently drops the
	// snapshot, which loses the tool-call state for every tool-using bot. Marshal
	// the JSON columns explicitly so the stored text matches what the field
	// serializer writes and reads back.
	if reasoningJSON, err := json.Marshal(reasoning); err != nil {
		log.Printf("streaming persistence: failed to marshal reasoning (chat %s): %v", p.chatUUID, err)
	} else {
		updates["reasoning"] = string(reasoningJSON)
	}
	if len(toolCallsJSON) > 0 {
		if encoded, err := json.Marshal(toolCallsJSON); err != nil {
			log.Printf("streaming persistence: failed to marshal tool calls (chat %s): %v", p.chatUUID, err)
		} else {
			updates["tool_calls"] = string(encoded)
		}
	}

	result := p.db.Model(&database.StreamingMessage{}).Where("id = ?", p.rowID).Updates(updates)
	if result.Error != nil {
		log.Printf("streaming persistence: failed to update snapshot (chat %s): %v", p.chatUUID, result.Error)
		return
	}
	p.lastFlush = time.Now()
	p.dirty = false
}

// dispose removes the snapshot row once the final message has been persisted.
func (p *partialMessagePersister) dispose() {
	if p == nil {
		return
	}
	if err := p.db.Unscoped().Delete(&database.StreamingMessage{}, p.rowID).Error; err != nil {
		log.Printf("streaming persistence: failed to delete snapshot row (chat %s): %v", p.chatUUID, err)
	}
}

// cleanStaleStreamingMessages removes snapshot rows that were never disposed
// (e.g. after a worker crash) and are older than streamingSnapshotStaleAfter.
func cleanStaleStreamingMessages() {
	db := database.GetGlobalDB()
	if db == nil {
		return
	}
	cutoff := time.Now().Add(-streamingSnapshotStaleAfter)
	if err := db.Unscoped().Where("updated_at < ?", cutoff).Delete(&database.StreamingMessage{}).Error; err != nil {
		log.Printf("streaming persistence: stale snapshot cleanup failed: %v", err)
	}
}
