package database

import "gorm.io/gorm"

// BotRuntimeOwner grants ownership access to a bot runtime config.
// OwnerUserId on BotRuntimeConfig remains the primary owner for compatibility.
type BotRuntimeOwner struct {
	Model
	BotRuntimeConfigId uint             `json:"bot_runtime_config_id" gorm:"index;uniqueIndex:idx_bot_runtime_owner"`
	BotRuntimeConfig   BotRuntimeConfig `json:"-" gorm:"foreignKey:BotRuntimeConfigId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	UserId             uint             `json:"user_id" gorm:"index;uniqueIndex:idx_bot_runtime_owner"`
	User               User             `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

func EnsureBotRuntimeOwner(DB *gorm.DB, runtimeID uint, userID uint) error {
	owner := BotRuntimeOwner{BotRuntimeConfigId: runtimeID, UserId: userID}
	return DB.Where("bot_runtime_config_id = ? AND user_id = ?", runtimeID, userID).FirstOrCreate(&owner).Error
}
