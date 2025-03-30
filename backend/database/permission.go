package database

type PermissionName string

const (
	PermissionCreateAPITokens PermissionName = "create_api_tokens"
)

func IsValidPermissionName(value string) bool {
	switch PermissionName(value) {
	case PermissionCreateAPITokens:
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
