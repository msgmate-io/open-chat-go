package database

import (
	"gorm.io/gorm"
	"time"
)

type Session struct {
	gorm.Model
	UserId uint      `json:"UserId" gorm:"index"`
	User   User      `json:"User" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:NO ACTION;"`
	Token  string    `gorm:"column:token;primaryKey;type:varchar(43)"`
	Data   []byte    `gorm:"column:data"`
	Expiry time.Time `gorm:"column:expiry;index"`
}
