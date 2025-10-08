package msgmate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
)

// FileHandlerImpl implements the FileHandler interface
type FileHandlerImpl struct {
	botContext *BotContext
}

// NewFileHandler creates a new file handler
func NewFileHandler(botContext *BotContext) *FileHandlerImpl {
	return &FileHandlerImpl{
		botContext: botContext,
	}
}

// ProcessAttachments processes file attachments in a message
func (fh *FileHandlerImpl) ProcessAttachments(attachments []interface{}, backend string) ([]map[string]interface{}, error) {
	var contentArray []map[string]interface{}

	for _, att := range attachments {
		if attMap, ok := att.(map[string]interface{}); ok {
			if fileID, ok := attMap["file_id"].(string); ok {
				mimeType, _ := attMap["mime_type"].(string)

				// Check if this is an image
				if mimeType != "" && strings.HasPrefix(mimeType, "image/") {
					// For images, convert to base64 and use vision format
					base64Data, contentType, err := fh.RetrieveFileData(fileID)
					if err != nil {
						log.Printf("Error retrieving image data for %s: %v", fileID, err)
						continue
					}

					contentArray = append(contentArray, map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": fmt.Sprintf("data:%s;base64,%s", contentType, base64Data),
						},
					})
				} else {
					// For non-images, handle based on backend
					if backend == "openai" {
						// For OpenAI backend, use file ID approach
						openAIFileID, err := fh.getOpenAIFileID(fileID)
						if err != nil {
							log.Printf("Error getting OpenAI file ID for %s: %v", fileID, err)
							continue
						}

						if openAIFileID == "" {
							// Upload file to OpenAI if not already uploaded
							openAIFileID, err = fh.UploadToOpenAI(fileID, mimeType)
							if err != nil {
								log.Printf("Error uploading file to OpenAI for %s: %v", fileID, err)
								continue
							}
						}

						// Add file reference to content array
						contentArray = append(contentArray, map[string]interface{}{
							"type": "file",
							"file": map[string]interface{}{
								"file_id": openAIFileID,
							},
						})
					} else {
						// For non-OpenAI backends, skip file attachments for now
						log.Printf("File attachments not supported for backend %s, skipping file %s", backend, fileID)
					}
				}
			}
		}
	}

	return contentArray, nil
}

// RetrieveFileData retrieves file data by ID
func (fh *FileHandlerImpl) RetrieveFileData(fileID string) (string, string, error) {
	// Create request to download the file
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s", fh.botContext.Client.GetHost(), fileID), nil)
	if err != nil {
		return "", "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", fh.botContext.Client.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("error downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("file download failed with status: %d", resp.StatusCode)
	}

	// Read the file content
	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("error reading file data: %w", err)
	}

	// Convert to base64
	base64Data := base64.StdEncoding.EncodeToString(fileData)

	// Get content type from response headers
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return base64Data, contentType, nil
}

// UploadToOpenAI uploads a file to OpenAI's API
func (fh *FileHandlerImpl) UploadToOpenAI(fileID, mimeType string) (string, error) {
	// Get OpenAI API key
	openAIKey := fh.botContext.Client.GetApiKey("openai")
	if openAIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	// First, get the file data from our server
	fileData, _, err := fh.RetrieveFileData(fileID)
	if err != nil {
		return "", fmt.Errorf("error retrieving file data: %w", err)
	}

	// Decode base64 data back to bytes
	fileBytes, err := base64.StdEncoding.DecodeString(fileData)
	if err != nil {
		return "", fmt.Errorf("error decoding base64 data: %w", err)
	}

	// Get file info to get the filename
	fileInfo, err := fh.getFileInfo(fileID)
	if err != nil {
		return "", fmt.Errorf("error getting file info: %w", err)
	}

	// Create multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add the file
	part, err := writer.CreateFormFile("file", fileInfo.FileName)
	if err != nil {
		return "", fmt.Errorf("error creating form file: %w", err)
	}

	_, err = io.Copy(part, bytes.NewReader(fileBytes))
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
	var openAIResp struct {
		ID        string `json:"id"`
		Object    string `json:"object"`
		Bytes     int    `json:"bytes"`
		CreatedAt int64  `json:"created_at"`
		Filename  string `json:"filename"`
		Purpose   string `json:"purpose"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	log.Printf("Successfully uploaded file %s to OpenAI with ID: %s", fileID, openAIResp.ID)

	return openAIResp.ID, nil
}

// getOpenAIFileID retrieves the OpenAI file ID for a given file ID
func (fh *FileHandlerImpl) getOpenAIFileID(fileID string) (string, error) {
	// Create request to get file info
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", fh.botContext.Client.GetHost(), fileID), nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", fh.botContext.Client.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error getting file info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("file info request failed with status: %d", resp.StatusCode)
	}

	// Parse the response to get file metadata
	var fileInfo struct {
		FileID       string                 `json:"file_id"`
		FileName     string                 `json:"file_name"`
		Size         int64                  `json:"size"`
		MimeType     string                 `json:"mime_type"`
		UploadedAt   string                 `json:"uploaded_at"`
		OpenAIFileID string                 `json:"openai_file_id,omitempty"`
		MetaData     map[string]interface{} `json:"meta_data,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return "", fmt.Errorf("error decoding file info: %w", err)
	}

	// Check if OpenAI file ID is in the response
	if fileInfo.OpenAIFileID != "" {
		return fileInfo.OpenAIFileID, nil
	}

	// Check if OpenAI file ID is in metadata
	if fileInfo.MetaData != nil {
		if openAIFileID, ok := fileInfo.MetaData["openai_file_id"].(string); ok && openAIFileID != "" {
			return openAIFileID, nil
		}
	}

	return "", nil
}

// getFileInfo retrieves file information including filename
func (fh *FileHandlerImpl) getFileInfo(fileID string) (*struct {
	FileID       string                 `json:"file_id"`
	FileName     string                 `json:"file_name"`
	Size         int64                  `json:"size"`
	MimeType     string                 `json:"mime_type"`
	UploadedAt   string                 `json:"uploaded_at"`
	OpenAIFileID string                 `json:"openai_file_id,omitempty"`
	MetaData     map[string]interface{} `json:"meta_data,omitempty"`
}, error) {
	// Create request to get file info
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", fh.botContext.Client.GetHost(), fileID), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", fh.botContext.Client.GetSessionId()))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("file info request failed with status: %d", resp.StatusCode)
	}

	// Parse the response to get file metadata
	var fileInfo struct {
		FileID       string                 `json:"file_id"`
		FileName     string                 `json:"file_name"`
		Size         int64                  `json:"size"`
		MimeType     string                 `json:"mime_type"`
		UploadedAt   string                 `json:"uploaded_at"`
		OpenAIFileID string                 `json:"openai_file_id,omitempty"`
		MetaData     map[string]interface{} `json:"meta_data,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return nil, fmt.Errorf("error decoding file info: %w", err)
	}

	return &fileInfo, nil
}

// FileHandlerFactory creates a file handler with the given context
func FileHandlerFactory(botContext *BotContext) FileHandler {
	return NewFileHandler(botContext)
}
