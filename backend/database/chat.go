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

type ChatSettings struct {
	Model
	ChatId uint `json:"ChatId" gorm:"index"`
	Chat   Chat `json:"Chat" gorm:"foreignKey:ChatId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
}
