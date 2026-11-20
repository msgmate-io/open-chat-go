package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BotRuntimeOwner grants ownership access to a bot runtime config.
// OwnerUserId on BotRuntimeConfig remains the primary owner for compatibility.
type BotRuntimeOwner struct {
	Model
	BotRuntimeConfigId uint             `json:"bot_runtime_config_id" gorm:"index;uniqueIndex:idx_bot_runtime_owner"`
	BotRuntimeConfig   BotRuntimeConfig `json:"-" gorm:"foreignKey:BotRuntimeConfigId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	UserId             uint             `json:"user_id" gorm:"index;uniqueIndex:idx_bot_runtime_owner"`
	User               User             `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

// EnsureBotRuntimeOwner grants ownership of `runtimeID` to `userID`.
//
// BotRuntimeOwner embeds the soft-deletable Model while idx_bot_runtime_owner
// covers only (bot_runtime_config_id, user_id), so a revoked ownership leaves a
// tombstone that a `FirstOrCreate` lookup cannot see but the unique index still
// enforces. The plain FirstOrCreate used to fall through to an INSERT and fail
// with SQLSTATE 23505 - the same shape as the idx_user_integration_access
// crash loop (ci#1154). Both callers run on every boot
// (BackfillBotRuntimeOwnersMigration, and applyBotBootstrapConfigs right after
// it soft-deletes a transferred owner's grant), so such a failure aborts the
// whole migration pass and panic-loops the process. Upsert and revive the
// tombstone instead, like EnsureIntegrationAccess / EnsurePermission.
func EnsureBotRuntimeOwner(DB *gorm.DB, runtimeID uint, userID uint) error {
	if DB == nil || runtimeID == 0 || userID == 0 {
		return nil
	}
	owner := BotRuntimeOwner{BotRuntimeConfigId: runtimeID, UserId: userID}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bot_runtime_config_id"},
			{Name: "user_id"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"deleted_at": nil,
			"updated_at": time.Now(),
		}),
	}).Create(&owner).Error
}
