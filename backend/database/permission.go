package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PermissionName string

const (
	PermissionCreateAPITokens PermissionName = "create_api_tokens"
	PermissionCreateBots      PermissionName = "create_bots"
)

func IsValidPermissionName(value string) bool {
	switch PermissionName(value) {
	case PermissionCreateAPITokens:
		return true
	case PermissionCreateBots:
		return true
	default:
		return false
	}
}

// Permission grants a capability to a specific user.
type Permission struct {
	Model
	UserId     uint           `json:"-" gorm:"index;uniqueIndex:idx_user_permission"`
	User       User           `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Permission PermissionName `json:"permission" gorm:"type:varchar(64);uniqueIndex:idx_user_permission"`
}

// EnsurePermission grants `permission` to `userID`.
//
// Like IntegrationAccess, Permission is soft-deletable while idx_user_permission
// covers only (user_id, permission), so a revoked grant leaves a tombstone that
// a `FirstOrCreate` cannot see but the unique index still enforces. Granting
// with a plain insert would then fail with SQLSTATE 23505 and take the boot
// migration pass down with it; upsert and revive the tombstone instead.
func EnsurePermission(db *gorm.DB, userID uint, permission PermissionName) error {
	if db == nil || userID == 0 || permission == "" {
		return nil
	}
	record := Permission{UserId: userID, Permission: permission}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "permission"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"deleted_at": nil,
			"updated_at": time.Now(),
		}),
	}).Create(&record).Error
}
