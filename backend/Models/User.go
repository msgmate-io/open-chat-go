package Models

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	uuid          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4()"`
	username      string
	password_hash string
}
