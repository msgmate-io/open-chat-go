package Models

import (
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	UserName string
	UserHash string
	UserId   uint
}
