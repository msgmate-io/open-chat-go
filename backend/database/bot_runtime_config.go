package database

import "encoding/json"

// BotRuntimeConfig stores owner-scoped bot metadata and default interaction config.
type BotRuntimeConfig struct {
	Model
	BotUserId           uint            `json:"bot_user_id" gorm:"index;uniqueIndex"`
	BotUser             User            `json:"bot_user" gorm:"foreignKey:BotUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	OwnerUserId         uint            `json:"owner_user_id" gorm:"index;uniqueIndex:idx_bot_owner_name"`
	OwnerUser           User            `json:"owner_user" gorm:"foreignKey:OwnerUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Name                string          `json:"name" gorm:"not null;uniqueIndex:idx_bot_owner_name"`
	Description         string          `json:"description"`
	DefaultSharedConfig json.RawMessage `json:"default_shared_config" gorm:"type:jsonb"`
	IsPublic            bool            `json:"is_public" gorm:"default:false"`
	IsActive            bool            `json:"is_active" gorm:"default:true"`
}
