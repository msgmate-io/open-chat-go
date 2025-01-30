package database

import "time"

type UploadedFile struct {
	Model
	FileID     string `gorm:"unique"` // Tusd's file ID
	FileName   string
	Size       int64
	MIMEType   string
	StorageURL string
	OwnerID    uint   // Original uploader
	Owner      User   `gorm:"foreignKey:OwnerID"`
	SharedWith []User `gorm:"many2many:file_access;"` // Users with access
}

// Join table for additional metadata (optional)
type FileAccess struct {
	UserID         uint   `gorm:"primaryKey"`
	UploadedFileID uint   `gorm:"primaryKey"`
	Permission     string // E.g., "view", "edit"
	CreatedAt      time.Time
}
