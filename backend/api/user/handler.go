package user

import (
	"gorm.io/gorm"
)

type UserHandler struct {
	DB           *gorm.DB
	CookieDomain string
}
