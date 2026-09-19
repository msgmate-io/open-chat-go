package database

import (
	"encoding/json"
)

// StreamingMessage holds a throttled snapshot of the message a bot is
// currently streaming, so progress is not lost when a client reloads while a
// completion is still streaming. The row is created when the stream starts,
// refreshed roughly once per second while chunks arrive, and deleted when the
// final message is persisted (or the stream fails/is cancelled).
type StreamingMessage struct {
	Model
	ChatId     uint              `gorm:"index" json:"-"`
	Chat       Chat              `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	SessionID  string            `json:"session_id" gorm:"index;size:128"`
	SenderId   uint              `json:"-" gorm:"index"`
	Sender     User              `json:"-" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint              `json:"-"`
	Text       string            `json:"text" gorm:"type:text"`
	Reasoning  []string          `json:"reasoning,omitempty" gorm:"type:jsonb;serializer:json"`
	ToolCalls  []json.RawMessage `json:"tool_calls,omitempty" gorm:"type:jsonb;serializer:json"`
	Seq        int64             `json:"seq"`
}

// TableName is explicit so the "streaming_messages" name is stable.
func (StreamingMessage) TableName() string {
	return "streaming_messages"
}
