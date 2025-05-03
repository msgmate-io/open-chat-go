package files

import (
	"backend/database"
	"backend/server/util"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type FilesHandler struct{}

type FileUploadResponse struct {
	FileID       string    `json:"file_id"`
	FileName     string    `json:"file_name"`
	Size         int64     `json:"size"`
	MimeType     string    `json:"mime_type"`
	UploadedAt   time.Time `json:"uploaded_at"`
	OpenAIFileID string    `json:"openai_file_id,omitempty"`
}

type FileAttachment struct {
	FileID      string `json:"file_id"`
	DisplayName string `json:"display_name,omitempty"`
}

type OpenAIUploadResponse struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int    `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}

// UploadFile handles file uploads for chat attachments
func (h *FilesHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if we should reupload to OpenAI
	reuploadToOpenAI := r.URL.Query().Get("reupload_to_openai") == "true"

	// Parse multipart form (10MB limit)
	err = r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	// Get the uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file size (5MB limit for chat attachments)
	if header.Size > 5<<20 {
		http.Error(w, "File too large. Maximum size is 5MB", http.StatusBadRequest)
		return
	}

	// Validate file type
	allowedTypes := map[string]bool{
		"image/jpeg":         true,
		"image/png":          true,
		"image/gif":          true,
		"image/webp":         true,
		"application/pdf":    true,
		"text/plain":         true,
		"application/msword": true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		"application/vnd.ms-excel": true,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
	}

	if !allowedTypes[header.Header.Get("Content-Type")] {
		http.Error(w, "File type not allowed", http.StatusBadRequest)
		return
	}

	// Generate unique file ID
	fileID := uuid.New().String()

	// Create uploads directory if it doesn't exist
	uploadsDir := "./uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		http.Error(w, "Unable to create uploads directory", http.StatusInternalServerError)
		return
	}

	// Create file path
	filePath := filepath.Join(uploadsDir, fileID)

	// Create the file on disk
	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Unable to create file on server", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file to the destination
	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}

	// Close the file so we can read it again for OpenAI upload
	dst.Close()

	// Upload to OpenAI if requested
	var openAIFileID string
	if reuploadToOpenAI {
		openAIFileID, err = h.uploadToOpenAI(filePath, header.Filename, header.Header.Get("Content-Type"))
		if err != nil {
			fmt.Printf("Error uploading to OpenAI: %v\n", err)
			// Don't fail the upload if OpenAI upload fails
		}
	}

	// Create database record
	uploadedFile := database.UploadedFile{
		FileID:     fileID,
		FileName:   header.Filename,
		Size:       header.Size,
		MIMEType:   header.Header.Get("Content-Type"),
		StorageURL: filePath,
		OwnerID:    user.ID,
	}

	// Add OpenAI file ID to metadata if available
	if openAIFileID != "" {
		metadata := map[string]interface{}{
			"openai_file_id": openAIFileID,
		}
		metadataBytes, _ := json.Marshal(metadata)
		uploadedFile.MetaData = metadataBytes
	}

	if err := DB.Create(&uploadedFile).Error; err != nil {
		// Clean up file if database save fails
		os.Remove(filePath)
		http.Error(w, "Unable to save file record", http.StatusInternalServerError)
		return
	}

	// Return success response
	response := FileUploadResponse{
		FileID:       fileID,
		FileName:     header.Filename,
		Size:         header.Size,
		MimeType:     header.Header.Get("Content-Type"),
		UploadedAt:   uploadedFile.CreatedAt,
		OpenAIFileID: openAIFileID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// uploadToOpenAI uploads a file to OpenAI's files API
func (h *FilesHandler) uploadToOpenAI(filePath, fileName, contentType string) (string, error) {
	// Get OpenAI API key from environment
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	// Read the file
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	// Create multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add the file
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("error creating form file: %w", err)
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return "", fmt.Errorf("error copying file data: %w", err)
	}

	// Add purpose field
	err = writer.WriteField("purpose", "assistants")
	if err != nil {
		return "", fmt.Errorf("error writing purpose field: %w", err)
	}

	writer.Close()

	// Create request to OpenAI
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/files", &buf)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+openAIKey)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var openAIResp OpenAIUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	return openAIResp.ID, nil
}

// GetFile serves uploaded files
func (h *FilesHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	// Get file record
	var uploadedFile database.UploadedFile
	if err := DB.Where("file_id = ?", fileID).First(&uploadedFile).Error; err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Check if user has access to the file
	if uploadedFile.OwnerID != user.ID {
		// Check if file is shared with user
		var fileAccess database.FileAccess
		if err := DB.Where("uploaded_file_id = ? AND user_id = ?", uploadedFile.ID, user.ID).First(&fileAccess).Error; err != nil {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	// Check if file exists on disk
	if _, err := os.Stat(uploadedFile.StorageURL); os.IsNotExist(err) {
		http.Error(w, "File not found on disk", http.StatusNotFound)
		return
	}

	// Serve the file
	w.Header().Set("Content-Type", uploadedFile.MIMEType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", uploadedFile.FileName))
	http.ServeFile(w, r, uploadedFile.StorageURL)
}

// DeleteFile allows users to delete their own files
func (h *FilesHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	// Get file record
	var uploadedFile database.UploadedFile
	if err := DB.Where("file_id = ?", fileID).First(&uploadedFile).Error; err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Check if user owns the file
	if uploadedFile.OwnerID != user.ID {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Delete file from disk
	if err := os.Remove(uploadedFile.StorageURL); err != nil {
		// Log error but continue with database cleanup
		fmt.Printf("Error deleting file from disk: %v\n", err)
	}

	// Delete from database
	if err := DB.Delete(&uploadedFile).Error; err != nil {
		http.Error(w, "Unable to delete file record", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File deleted successfully"))
}

// GetFileInfo returns file information including metadata
func (h *FilesHandler) GetFileInfo(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	// Get file record
	var uploadedFile database.UploadedFile
	if err := DB.Where("file_id = ?", fileID).First(&uploadedFile).Error; err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Check if user has access to the file
	if uploadedFile.OwnerID != user.ID {
		// Check if file is shared with user
		var fileAccess database.FileAccess
		if err := DB.Where("uploaded_file_id = ? AND user_id = ?", uploadedFile.ID, user.ID).First(&fileAccess).Error; err != nil {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	// Parse metadata to get OpenAI file ID
	var metadata map[string]interface{}
	var openAIFileID string
	if uploadedFile.MetaData != nil {
		if err := json.Unmarshal(uploadedFile.MetaData, &metadata); err == nil {
			if id, ok := metadata["openai_file_id"].(string); ok {
				openAIFileID = id
			}
		}
	}

	// Return file info
	response := FileUploadResponse{
		FileID:       uploadedFile.FileID,
		FileName:     uploadedFile.FileName,
		Size:         uploadedFile.Size,
		MimeType:     uploadedFile.MIMEType,
		UploadedAt:   uploadedFile.CreatedAt,
		OpenAIFileID: openAIFileID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetFileData returns file data as base64-encoded content
func (h *FilesHandler) GetFileData(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	// Get file record
	var uploadedFile database.UploadedFile
	if err := DB.Where("file_id = ?", fileID).First(&uploadedFile).Error; err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Check if user has access to the file
	if uploadedFile.OwnerID != user.ID {
		// Check if file is shared with user
		var fileAccess database.FileAccess
		if err := DB.Where("uploaded_file_id = ? AND user_id = ?", uploadedFile.ID, user.ID).First(&fileAccess).Error; err != nil {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
	}

	// Check if file exists on disk
	if _, err := os.Stat(uploadedFile.StorageURL); os.IsNotExist(err) {
		http.Error(w, "File not found on disk", http.StatusNotFound)
		return
	}

	// Read the file content
	fileBytes, err := os.ReadFile(uploadedFile.StorageURL)
	if err != nil {
		http.Error(w, "Unable to read file", http.StatusInternalServerError)
		return
	}

	// Encode as base64
	encodedData := base64.StdEncoding.EncodeToString(fileBytes)

	// Return the file data
	response := map[string]interface{}{
		"data":         encodedData,
		"content_type": uploadedFile.MIMEType,
		"file_name":    uploadedFile.FileName,
		"file_size":    uploadedFile.Size,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
