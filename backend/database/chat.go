package database

import (
	"encoding/json"
	"time"
)

type Message struct {
	Model
	ReadAt     *time.Time         `json:"read_at" gorm:"default:null"`
	SenderId   uint               `json:"-" gorm:"index"`
	Sender     User               `json:"-" gorm:"foreignKey:SenderId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ReceiverId uint               `json:"-" gorm:"index"`
	Receiver   User               `json:"-" gorm:"foreignKey:ReceiverId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	DataType   string             `json:"data_type" gorm:"default:'text'"`
	ChatId     uint               `json:"-" gorm:"index"`
	Chat       Chat               `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Content    *[]byte            `json:"-"`
	Text       *string            `json:"text"`
	Reasoning  *[]string          `json:"reasoning,omitempty" gorm:"type:jsonb;serializer:json"`
	ToolCalls  *[]json.RawMessage `json:"tool_calls,omitempty" gorm:"type:jsonb;serializer:json"`
	MetaData   json.RawMessage    `json:"meta_data" gorm:"type:jsonb"`
}

// SharedChatConfig stores the shared LLM/tool configuration for a chat.
// It is created from the `shared_config` payload when a chat is created and is
// loaded for both chat participants when listing/opening chats and when tools run.
type SharedChatConfig struct {
	Model
	ChatId     uint            `json:"-" gorm:"index"`
	Chat       Chat            `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ConfigData json.RawMessage `json:"config_data" gorm:"type:jsonb"`
}

type Chat struct {
	Model
	User1Id         uint              `json:"-" gorm:"index"`
	User2Id         uint              `json:"-" gorm:"index"`
	User1           User              `json:"user1" gorm:"foreignKey:User1Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	User2           User              `json:"user2" gorm:"foreignKey:User2Id;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	LatestMessageId *uint             `json:"-" gorm:"index"`
	LatestMessage   *Message          `json:"latest_message" gorm:"foreignKey:LatestMessageId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	SharedConfigId  *uint             `json:"-" gorm:"index"`
	SharedConfig    *SharedChatConfig `json:"config" gorm:"foreignKey:SharedConfigId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ChatType        string            `json:"chat_type" gorm:"default:'conversation'"`
}

// ChatSettings stores per-chat settings owned by the backend for server-side
// behavior/state, separate from shared user-visible chat config. It is scoped
// to a chat and currently not exposed directly through chat APIs.
type ChatSettings struct {
	Model
	ChatId     uint            `json:"ChatId" gorm:"index"`
	Chat       Chat            `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	ConfigData json.RawMessage `json:"config_data" gorm:"type:jsonb"`
}

// SharedChatInstance links a private chat to a public share UUID owned by a user.
// It powers publish/unpublish chat sharing and public read-only shared interaction views.
type SharedChatInstance struct {
	Model
	ChatId        uint   `json:"-" gorm:"index"`
	Chat          Chat   `json:"-" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	OwningUserId  uint   `json:"-" gorm:"index"`
	OwningUser    User   `json:"-" gorm:"foreignKey:OwningUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ChatShareUUID string `json:"chat_share_uuid" gorm:"uniqueIndex;not null"`
}
