package database

import (
	"encoding/json"
	"time"
)

type UploadedFile struct {
	Model
	FileID     string `gorm:"unique"` // Tusd's file ID
	FileName   string
	Size       int64
	MIMEType   string
	StorageURL string
	OwnerID    uint            // Original uploader
	Owner      User            `gorm:"foreignKey:OwnerID"`
	SharedWith []User          `gorm:"many2many:file_access;"` // Users with access
	MetaData   json.RawMessage `gorm:"type:json"`              // Additional metadata (e.g., OpenAI file ID)
}

// Join table for additional metadata (optional)
type FileAccess struct {
	UserID         uint   `gorm:"primaryKey"`
	UploadedFileID uint   `gorm:"primaryKey"`
	Permission     string // E.g., "view", "edit"
	CreatedAt      time.Time
}
