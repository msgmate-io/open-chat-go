package database

import (
	"time"

	"gorm.io/gorm"
)

type MobileAPIResponseCache struct {
	gorm.Model
	CacheKey            string `gorm:"size:128;uniqueIndex;not null"`
	Method              string `gorm:"size:8;index;not null"`
	URLPathWithQuery    string `gorm:"size:2048;index;not null"`
	SessionScopeHash    string `gorm:"size:128;index;not null"`
	StatusCode          int    `gorm:"not null"`
	ContentType         string `gorm:"size:255"`
	ResponseHeadersJSON string `gorm:"type:text"`
	// ci#878: no explicit `type:blob` - SQLite's `blob` is not a PostgreSQL type
	// (it fails AutoMigrate with "type blob does not exist"). GORM's per-dialect
	// default for []byte is `blob` on SQLite and `bytea` on PostgreSQL, so the
	// column keeps its SQLite schema and becomes valid on PostgreSQL.
	ResponseBody    []byte
	ExpiresAt       time.Time `gorm:"index"`
	LastValidatedAt time.Time `gorm:"index"`
}
