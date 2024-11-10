package database

import (
	"gorm.io/gorm"
	"time"
)

type Session struct {
	gorm.Model
	Token  string    `gorm:"column:token;primaryKey;type:varchar(43)"`
	Data   []byte    `gorm:"column:data"`
	Expiry time.Time `gorm:"column:expiry;index"`
}
