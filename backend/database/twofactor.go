package database

import (
	"time"
)

type TwoFactorRecoveryCode struct {
	Model
	UserId   uint       `json:"-" gorm:"index"`
	User     User       `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	CodeHash string     `json:"-"`
	UsedAt   *time.Time `json:"-" gorm:"default:null"`
}
