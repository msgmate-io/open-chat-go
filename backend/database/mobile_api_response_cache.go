package database

import (
	"time"

	"gorm.io/gorm"
)

type MobileAPIResponseCache struct {
	gorm.Model
	CacheKey            string    `gorm:"size:128;uniqueIndex;not null"`
	Method              string    `gorm:"size:8;index;not null"`
	URLPathWithQuery    string    `gorm:"size:2048;index;not null"`
	SessionScopeHash    string    `gorm:"size:128;index;not null"`
	StatusCode          int       `gorm:"not null"`
	ContentType         string    `gorm:"size:255"`
	ResponseHeadersJSON string    `gorm:"type:text"`
	ResponseBody        []byte    `gorm:"type:blob"`
	ExpiresAt           time.Time `gorm:"index"`
	LastValidatedAt     time.Time `gorm:"index"`
}
