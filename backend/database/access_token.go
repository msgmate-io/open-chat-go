package database

import "time"

// AccessToken stores hashed API credentials for user API access.
type AccessToken struct {
	Model
	UserId      uint       `json:"-" gorm:"index"`
	User        User       `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Name        string     `json:"name" gorm:"type:varchar(120);not null"`
	TokenPrefix string     `json:"token_prefix" gorm:"type:varchar(40);not null;index"`
	TokenHash   string     `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	LastUsedAt  *time.Time `json:"last_used_at" gorm:"default:null"`
	ExpiresAt   *time.Time `json:"expires_at" gorm:"default:null"`
	RevokedAt   *time.Time `json:"revoked_at" gorm:"default:null;index"`
}
