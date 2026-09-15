package chats

import (
	"backend/database"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

// ErrAttachmentNotOwned is returned when an attachment references an uploaded
// file that is not owned by the acting user.
var ErrAttachmentNotOwned = errors.New("attachment file not owned by user")

// ShareUploadedFileWithUser grants a user "view" access to an uploaded file.
// It is idempotent: an existing access row is left untouched.
func ShareUploadedFileWithUser(DB *gorm.DB, userID uint, uploadedFile *database.UploadedFile) error {
	var existingAccess database.FileAccess
	result := DB.Where("user_id = ? AND uploaded_file_id = ?", userID, uploadedFile.ID).First(&existingAccess)
	if result.Error == nil {
		return nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	}
	fileAccess := database.FileAccess{
		UserID:         userID,
		UploadedFileID: uploadedFile.ID,
		Permission:     "view",
		CreatedAt:      time.Now(),
	}
	return DB.Create(&fileAccess).Error
}

// EnrichAndShareAttachments loads each attachment's uploaded file, verifies the
// acting user owns it, grants the receiver view access and returns the
// attachments enriched with file_name/file_size/mime_type. Unknown files surface
// gorm.ErrRecordNotFound; files owned by another user surface
// ErrAttachmentNotOwned.
func EnrichAndShareAttachments(DB *gorm.DB, ownerID, receiverID uint, attachments []FileAttachment) ([]FileAttachment, error) {
	enriched := make([]FileAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		var uploadedFile database.UploadedFile
		if err := DB.Where("file_id = ?", attachment.FileID).First(&uploadedFile).Error; err != nil {
			return nil, err
		}
		if uploadedFile.OwnerID != ownerID {
			return nil, ErrAttachmentNotOwned
		}
		if err := ShareUploadedFileWithUser(DB, receiverID, &uploadedFile); err != nil {
			// Sharing is best-effort: the message itself must still be created.
			log.Printf("Error sharing file %s (ID: %d) with user %d: %v", attachment.FileID, uploadedFile.ID, receiverID, err)
		}
		enriched = append(enriched, FileAttachment{
			FileID:      attachment.FileID,
			DisplayName: attachment.DisplayName,
			FileName:    uploadedFile.FileName,
			FileSize:    uploadedFile.Size,
			MimeType:    uploadedFile.MIMEType,
		})
	}
	return enriched, nil
}
