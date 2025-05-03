package integrations

import (
	"backend/database"
	"backend/scheduler"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SignalIntegrationService manages WebSocket connections to Signal REST API instances
type SignalIntegrationService struct {
	DB                *gorm.DB
	SchedulerService  *scheduler.SchedulerService
	activeConnections map[string]*SignalConnection
	mu                sync.Mutex
	serverURL         string
	stopChannels      map[string]chan struct{} // Channels to stop restart loops
	stopMu            sync.Mutex
}

// SignalConnection represents a connection to a Signal REST API instance
type SignalConnection struct {
	Integration database.Integration
	PhoneNumber string
	Port        int
	Alias       string
	Conn        *websocket.Conn
	Done        chan struct{}
}

// SignalAttachment represents an attachment from Signal
type SignalAttachment struct {
	ID              string  `json:"id"`
	ContentType     string  `json:"contentType"`
	Filename        string  `json:"filename"`
	Size            int64   `json:"size"`
	Width           *int    `json:"width,omitempty"`
	Height          *int    `json:"height,omitempty"`
	Caption         *string `json:"caption,omitempty"`
	UploadTimestamp *int64  `json:"uploadTimestamp,omitempty"`
}

// NewSignalIntegrationService creates a new SignalIntegrationService
func NewSignalIntegrationService(DB *gorm.DB, schedulerService *scheduler.SchedulerService, serverURL string) *SignalIntegrationService {
	service := &SignalIntegrationService{
		DB:                DB,
		SchedulerService:  schedulerService,
		activeConnections: make(map[string]*SignalConnection),
		serverURL:         serverURL,
		stopChannels:      make(map[string]chan struct{}),
	}

	return service
}

// StartIntegrationWithRestart starts the integration with automatic restart capability and error logging
func (s *SignalIntegrationService) StartIntegrationWithRestart(integration database.Integration) error {
	return s.StartIntegrationWithRestartContext(context.Background(), integration)
}

// StartIntegrationWithRestartContext starts the integration with automatic restart capability, error logging, and context cancellation
func (s *SignalIntegrationService) StartIntegrationWithRestartContext(ctx context.Context, integration database.Integration) error {
	// Parse the integration config to get the alias
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, ok := config["alias"].(string)
	if !ok {
		return fmt.Errorf("invalid alias in integration config")
	}

	// Check if we already have a restart loop for this integration
	s.stopMu.Lock()
	if _, exists := s.stopChannels[alias]; exists {
		s.stopMu.Unlock()
		return fmt.Errorf("restart loop for integration %s already exists", alias)
	}

	// Create stop channel for this integration
	stopCh := make(chan struct{})
	s.stopChannels[alias] = stopCh
	s.stopMu.Unlock()

	go func() {
		restartCount := 0
		maxRestartDelay := 30 * time.Second
		baseRestartDelay := 5 * time.Second
		maxRestartAttempts := 1000 // Prevent infinite restarts in case of persistent issues

		defer func() {
			if r := recover(); r != nil {
				log.Printf("[Signal:%s] Integration restart loop panicked: %v", alias, r)
				s.logErrorToDisk(fmt.Errorf("panic: %v", r), restartCount, alias)
			}
		}()

		for restartCount < maxRestartAttempts {
			select {
			case <-ctx.Done():
				log.Printf("[Signal:%s] Integration restart loop cancelled: %v", alias, ctx.Err())
				return
			case <-stopCh:
				log.Printf("[Signal:%s] Integration restart loop stopped by request", alias)
				return
			default:
				// Continue with integration restart logic
			}

			restartCount++
			log.Printf("[Signal:%s] Starting integration (attempt %d)...", alias, restartCount)

			// Start the integration and capture any errors
			err := s.StartIntegration(integration)

			// Log the error to disk
			if err != nil {
				s.logErrorToDisk(err, restartCount, alias)
				log.Printf("[Signal:%s] Integration crashed (attempt %d): %v", alias, restartCount, err)
			} else {
				log.Printf("[Signal:%s] Integration stopped normally (attempt %d)", alias, restartCount)
				// If the integration stopped normally (no error), we might want to exit the restart loop
				// For now, we'll continue restarting to handle cases where the integration exits gracefully
				// but we want it to keep running
			}

			// Calculate restart delay with exponential backoff (capped at maxRestartDelay)
			restartDelay := time.Duration(restartCount) * baseRestartDelay
			if restartDelay > maxRestartDelay {
				restartDelay = maxRestartDelay
			}

			log.Printf("[Signal:%s] Restarting integration in %v (attempt %d)", alias, restartDelay, restartCount+1)

			// Use a timer with context cancellation for the restart delay
			timer := time.NewTimer(restartDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				log.Printf("[Signal:%s] Integration restart loop cancelled during delay: %v", alias, ctx.Err())
				return
			case <-stopCh:
				timer.Stop()
				log.Printf("[Signal:%s] Integration restart loop stopped during delay", alias)
				return
			case <-timer.C:
				// Continue to next iteration
			}
		}

		log.Printf("[Signal:%s] Integration restart loop stopped after %d attempts (max reached)", alias, maxRestartAttempts)
		s.logErrorToDisk(fmt.Errorf("max restart attempts reached (%d)", maxRestartAttempts), restartCount, alias)
	}()

	log.Printf("[Signal:%s] Integration restart loop started", alias)
	return nil
}

// logErrorToDisk writes integration errors to a log file
func (s *SignalIntegrationService) logErrorToDisk(err error, attempt int, alias string) {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Printf("[Signal:%s] Failed to create logs directory: %v", alias, err)
		return
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFileName := filepath.Join(logsDir, fmt.Sprintf("signal_integration_errors_%s.log", timestamp))

	// Open log file in append mode
	logFile, err := os.OpenFile(logFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[Signal:%s] Failed to open log file: %v", alias, err)
		return
	}
	defer logFile.Close()

	// Get current time for logging
	now := time.Now()

	// Write detailed error entry
	errorEntry := fmt.Sprintf("[%s] Signal integration crash (attempt %d) for alias '%s':\n",
		now.Format("2006-01-02 15:04:05"),
		attempt,
		alias)

	errorEntry += fmt.Sprintf("  Error: %v\n", err)
	errorEntry += fmt.Sprintf("  Timestamp: %s\n", now.Format(time.RFC3339))
	errorEntry += fmt.Sprintf("  Attempt: %d\n", attempt)
	errorEntry += fmt.Sprintf("  Alias: %s\n", alias)

	// Add separator for readability
	errorEntry += "  " + strings.Repeat("-", 50) + "\n"

	if _, writeErr := logFile.WriteString(errorEntry); writeErr != nil {
		log.Printf("[Signal:%s] Failed to write to log file: %v", alias, writeErr)
	}
}

// StartAllActiveIntegrations starts WebSocket connections for all active Signal integrations
func (s *SignalIntegrationService) StartAllActiveIntegrations() {
	var integrations []database.Integration
	result := s.DB.Where("integration_type = ? AND active = ?", "signal", true).Find(&integrations)
	if result.Error != nil {
		log.Printf("[Signal:Service] Error finding active Signal integrations: %v", result.Error)
		return
	}

	log.Printf("[Signal:Service] Found %d active Signal integrations", len(integrations))
	for _, integration := range integrations {
		if err := s.StartIntegrationWithRestart(integration); err != nil {
			log.Printf("[Signal:Service] Error starting Signal integration %s: %v", integration.IntegrationName, err)
		}
	}
}

// checkAndStartDockerContainer checks if a Docker container is running and starts it if not
func (s *SignalIntegrationService) checkAndStartDockerContainer(alias string, port int) error {
	// Check if the container exists and is running
	checkCmd := exec.Command("docker", "ps", "--filter", fmt.Sprintf("name=%s", alias), "--format", "{{.Names}}")
	output, err := checkCmd.CombinedOutput()
	if err != nil {
		log.Printf("[Signal:%s] Error checking Docker container status: %v", alias, err)
		return fmt.Errorf("failed to check Docker container status: %w", err)
	}

	containerName := strings.TrimSpace(string(output))
	if containerName == alias {
		log.Printf("[Signal:%s] Docker container is already running", alias)
		return nil
	}

	// Container is not running, check if it exists
	checkExistsCmd := exec.Command("docker", "ps", "-a", "--filter", fmt.Sprintf("name=%s", alias), "--format", "{{.Names}}")
	existsOutput, err := checkExistsCmd.CombinedOutput()
	if err != nil {
		log.Printf("[Signal:%s] Error checking if Docker container exists: %v", alias, err)
		return fmt.Errorf("failed to check if Docker container exists: %w", err)
	}

	existsName := strings.TrimSpace(string(existsOutput))
	if existsName == alias {
		// Container exists but is not running, start it
		log.Printf("[Signal:%s] Docker container exists but is not running, starting it", alias)
		startCmd := exec.Command("docker", "start", alias)
		startOutput, err := startCmd.CombinedOutput()
		if err != nil {
			log.Printf("[Signal:%s] Error starting Docker container: %v, output: %s", alias, err, string(startOutput))
			return fmt.Errorf("failed to start Docker container: %w", err)
		}
		log.Printf("[Signal:%s] Docker container started successfully", alias)
	} else {
		// Container doesn't exist, this is an error as it should have been created during installation
		log.Printf("[Signal:%s] Docker container does not exist, integration may not be properly installed", alias)
		return fmt.Errorf("Docker container for integration %s does not exist", alias)
	}

	// Wait a moment for the container to fully start
	time.Sleep(2 * time.Second)

	// Verify the container is now running
	verifyCmd := exec.Command("docker", "ps", "--filter", fmt.Sprintf("name=%s", alias), "--format", "{{.Names}}")
	verifyOutput, err := verifyCmd.CombinedOutput()
	if err != nil {
		log.Printf("[Signal:%s] Error verifying Docker container is running: %v", alias, err)
		return fmt.Errorf("failed to verify Docker container is running: %w", err)
	}

	verifyName := strings.TrimSpace(string(verifyOutput))
	if verifyName != alias {
		log.Printf("[Signal:%s] Docker container failed to start properly", alias)
		return fmt.Errorf("Docker container failed to start properly")
	}

	log.Printf("[Signal:%s] Docker container is now running", alias)
	return nil
}

// StartIntegration starts a WebSocket connection for a specific Signal integration
func (s *SignalIntegrationService) StartIntegration(integration database.Integration) error {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, ok := config["alias"].(string)
	if !ok {
		return fmt.Errorf("invalid alias in integration config")
	}

	port, ok := config["port"].(float64)
	if !ok {
		return fmt.Errorf("invalid port in integration config")
	}

	phoneNumber, ok := config["phone_number"].(string)
	if !ok {
		return fmt.Errorf("invalid phone_number in integration config")
	}

	// Check if we already have a connection for this integration
	s.mu.Lock()
	if _, exists := s.activeConnections[alias]; exists {
		s.mu.Unlock()
		return fmt.Errorf("connection for integration %s already exists", alias)
	}
	s.mu.Unlock()

	// Check if Docker container is running and start it if needed
	if err := s.checkAndStartDockerContainer(alias, int(port)); err != nil {
		return fmt.Errorf("failed to ensure Docker container is running: %w", err)
	}

	// Create WebSocket URL
	u := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("localhost:%d", int(port)),
		Path:   fmt.Sprintf("/v1/receive/%s", phoneNumber),
	}
	log.Printf("[Signal:%s] Connecting to WebSocket at %s", alias, u.String())

	// Connect to WebSocket with proper headers
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Create a new connection
	connection := &SignalConnection{
		Integration: integration,
		PhoneNumber: phoneNumber,
		Port:        int(port),
		Alias:       alias,
		Conn:        conn,
		Done:        make(chan struct{}),
	}

	// Add the connection to the active connections map
	s.mu.Lock()
	s.activeConnections[alias] = connection
	s.mu.Unlock()

	// Start listening for messages and wait for it to complete
	// This will block until the listener exits, allowing the restart mechanism to detect failures
	err = s.listenForMessages(connection)

	// Clean up the connection from active connections when it exits
	s.mu.Lock()
	delete(s.activeConnections, alias)
	s.mu.Unlock()

	log.Printf("[Signal:%s] WebSocket connection closed", alias)
	return err
}

// StopIntegration stops a WebSocket connection for a specific Signal integration
func (s *SignalIntegrationService) StopIntegration(alias string) error {
	// First, stop the restart loop if it exists
	s.stopMu.Lock()
	if stopCh, exists := s.stopChannels[alias]; exists {
		close(stopCh)
		delete(s.stopChannels, alias)
		log.Printf("[Signal:%s] Stopped restart loop", alias)
	}
	s.stopMu.Unlock()

	s.mu.Lock()
	connection, exists := s.activeConnections[alias]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("no active connection for integration %s", alias)
	}
	s.mu.Unlock()

	// Signal the listener to stop
	close(connection.Done)

	// Close the WebSocket connection if it exists
	if connection.Conn != nil {
		if err := connection.Conn.Close(); err != nil {
			log.Printf("[Signal:%s] Error closing WebSocket connection: %v", alias, err)
		}
	}

	// Remove the connection from the active connections map
	s.mu.Lock()
	delete(s.activeConnections, alias)
	s.mu.Unlock()

	log.Printf("[Signal:%s] Integration stopped", alias)
	return nil
}

// StopAllIntegrations stops all active WebSocket connections
func (s *SignalIntegrationService) StopAllIntegrations() {
	// Stop all restart loops
	s.stopMu.Lock()
	for alias, stopCh := range s.stopChannels {
		close(stopCh)
		log.Printf("[Signal:%s] Stopped restart loop", alias)
	}
	s.stopChannels = make(map[string]chan struct{}) // Clear the map
	s.stopMu.Unlock()

	s.mu.Lock()
	aliases := make([]string, 0, len(s.activeConnections))
	for alias := range s.activeConnections {
		aliases = append(aliases, alias)
	}
	s.mu.Unlock()

	for _, alias := range aliases {
		if err := s.StopIntegration(alias); err != nil {
			log.Printf("[Signal:Service] Error stopping Signal integration %s: %v", alias, err)
		}
	}
}

// listenForMessages listens for messages from the WebSocket
func (s *SignalIntegrationService) listenForMessages(connection *SignalConnection) error {
	for {
		select {
		case <-connection.Done:
			log.Printf("[Signal:%s] Integration stopped: Connection closed", connection.Alias)
			return nil // Return nil to indicate successful exit
		default:
			// Read message from WebSocket
			_, message, err := connection.Conn.ReadMessage()
			if err != nil {
				log.Printf("[Signal:%s] Error reading from WebSocket: %v", connection.Alias, err)

				// Try to reconnect
				time.Sleep(5 * time.Second)
				if err := s.reconnect(connection); err != nil {
					log.Printf("[Signal:%s] Failed to reconnect: %v", connection.Alias, err)

					// If reconnection fails, check if the integration is still active
					var integration database.Integration
					if err := s.DB.First(&integration, connection.Integration.ID).Error; err != nil {
						log.Printf("[Signal:%s] Integration no longer exists, stopping listener", connection.Alias)
						return nil // Return nil to indicate successful exit
					}

					if !integration.Active {
						log.Printf("[Signal:%s] Integration is no longer active, stopping listener", connection.Alias)
						return nil // Return nil to indicate successful exit
					}

					// If integration is still active but reconnection failed, return error to trigger restart
					return fmt.Errorf("failed to reconnect to WebSocket after multiple attempts: %w", err)
				}
				continue
			}

			log.Printf("[Signal:%s] Integration is listening", connection.Alias)

			// Process the message
			if err := s.processMessage(connection, message); err != nil {
				log.Printf("[Signal:%s] Error processing message: %v", connection.Alias, err)
				// Don't return error for message processing failures, just log and continue
			}
		}
	}
}

// reconnect attempts to reconnect to the WebSocket
func (s *SignalIntegrationService) reconnect(connection *SignalConnection) error {
	// Create WebSocket URL
	u := url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("localhost:%d", connection.Port),
		Path:   fmt.Sprintf("/v1/receive/%s", connection.PhoneNumber),
	}
	log.Printf("[Signal:%s] Reconnecting to WebSocket at %s", connection.Alias, u.String())

	// Connect to WebSocket with proper headers
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to reconnect to WebSocket: %w", err)
	}

	// Update the connection
	connection.Conn = conn
	log.Printf("[Signal:%s] WebSocket reconnection successful", connection.Alias)
	return nil
}

// Helper function to check if a message is a group message
func isGroupMessage(message map[string]interface{}) bool {
	// Check if this is a group message by looking for groupInfo or groupId
	if envelope, ok := message["envelope"].(map[string]interface{}); ok {
		// Check for groupInfo in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if _, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
				return true
			}
		}

		// Check for groupV2 in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if _, ok := dataMessage["groupV2"].(map[string]interface{}); ok {
				return true
			}
		}

		// Check for groupInfo in syncMessage.sentMessage (for sent group messages)
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if _, ok := sentMessage["groupInfo"].(map[string]interface{}); ok {
					return true
				}
			}
		}

		// Check in content.dataMessage
		if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if _, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
					return true
				}
				if _, ok := dataMessage["groupV2"].(map[string]interface{}); ok {
					return true
				}
			}
		}
	}
	return false
}

// Helper function to extract group information from a message
func extractGroupInfo(message map[string]interface{}) (string, string, bool) {
	if envelope, ok := message["envelope"].(map[string]interface{}); ok {
		// Check for groupInfo in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
				if groupId, ok := groupInfo["groupId"].(string); ok {
					groupName := ""
					if name, ok := groupInfo["groupName"].(string); ok {
						groupName = name
					}
					return groupId, groupName, true
				}
			}
		}

		// Check for groupInfo in syncMessage.sentMessage (for sent group messages)
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := sentMessage["groupInfo"].(map[string]interface{}); ok {
					if groupId, ok := groupInfo["groupId"].(string); ok {
						groupName := ""
						if name, ok := groupInfo["groupName"].(string); ok {
							groupName = name
						}
						return groupId, groupName, true
					}
				}
			}
		}

		// Check in content.dataMessage
		if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
					if groupId, ok := groupInfo["groupId"].(string); ok {
						groupName := ""
						if name, ok := groupInfo["groupName"].(string); ok {
							groupName = name
						}
						return groupId, groupName, true
					}
				}
			}
		}
	}
	return "", "", false
}

// Helper function to check if a number is in the whitelist
func isNumberInWhitelist(number string, integration database.Integration) bool {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("[Signal:Whitelist] Error parsing integration config: %v", err)
		return false
	}

	// Get the whitelist from the config
	if whitelistInterface, exists := config["whitelist"]; exists {
		if whitelistArray, ok := whitelistInterface.([]interface{}); ok {
			// If whitelist is empty, don't auto-process as AI
			if len(whitelistArray) == 0 {
				log.Printf("[Signal:Whitelist] Whitelist exists but is empty, not auto-processing as AI")
				return false
			}

			// Check if the number is in the whitelist
			for _, item := range whitelistArray {
				if str, ok := item.(string); ok && str == number {
					log.Printf("[Signal:Whitelist] Number %s found in whitelist", number)
					return true
				}
			}

			log.Printf("[Signal:Whitelist] Number %s not found in whitelist of %d entries", number, len(whitelistArray))
		} else {
			log.Printf("[Signal:Whitelist] Whitelist is not an array, cannot check if number is whitelisted")
		}
	} else {
		// No whitelist at all
		log.Printf("[Signal:Whitelist] No whitelist found in integration config")
		return false
	}

	return false
}

// Helper function to check if a group ID is in the whitelist
func isGroupIdInWhitelist(groupId string, integration database.Integration) bool {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("[Signal:Whitelist] Error parsing integration config: %v", err)
		return false
	}

	// Get the whitelist from the config
	if whitelistInterface, exists := config["whitelist"]; exists {
		if whitelistArray, ok := whitelistInterface.([]interface{}); ok {
			// If whitelist is empty, don't auto-process as AI
			if len(whitelistArray) == 0 {
				log.Printf("[Signal:Whitelist] Whitelist exists but is empty, not auto-processing group messages")
				return false
			}

			// Check if the group ID is in the whitelist
			for _, item := range whitelistArray {
				if str, ok := item.(string); ok && str == groupId {
					log.Printf("[Signal:Whitelist] Group ID %s found in whitelist", groupId)
					return true
				}
			}

			log.Printf("[Signal:Whitelist] Group ID %s not found in whitelist of %d entries", groupId, len(whitelistArray))
		} else {
			log.Printf("[Signal:Whitelist] Whitelist is not an array, cannot check if group ID is whitelisted")
		}
	} else {
		// No whitelist at all
		log.Printf("[Signal:Whitelist] No whitelist found in integration config")
		return false
	}

	return false
}

// downloadSignalAttachment downloads an attachment from Signal REST API
func (s *SignalIntegrationService) downloadSignalAttachment(connection *SignalConnection, attachmentID string) ([]byte, string, error) {
	// Create the URL for downloading the attachment
	attachmentURL := fmt.Sprintf("http://localhost:%d/v1/attachments/%s", connection.Port, attachmentID)
	log.Printf("[Signal:%s] Downloading attachment from: %s", connection.Alias, attachmentURL)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Make the request
	resp, err := client.Get(attachmentURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download attachment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("failed to download attachment, status: %d", resp.StatusCode)
	}

	// Read the attachment data
	attachmentData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read attachment data: %w", err)
	}

	// Get content type from response headers
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	log.Printf("[Signal:%s] Successfully downloaded attachment %s (%d bytes, type: %s)",
		connection.Alias, attachmentID, len(attachmentData), contentType)

	return attachmentData, contentType, nil
}

// uploadAttachmentToBackend uploads an attachment to our backend file system
func (s *SignalIntegrationService) uploadAttachmentToBackend(attachmentData []byte, filename, contentType string, integrationOwner database.User) (*database.UploadedFile, error) {
	// Generate unique file ID
	fileID := uuid.New().String()

	// Create uploads directory if it doesn't exist
	uploadsDir := "./uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create uploads directory: %w", err)
	}

	// Create file path
	filePath := filepath.Join(uploadsDir, fileID)

	// Create the file on disk
	dst, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file on server: %w", err)
	}
	defer dst.Close()

	// Write the attachment data to the file
	_, err = io.Copy(dst, strings.NewReader(string(attachmentData)))
	if err != nil {
		// Clean up file if write fails
		os.Remove(filePath)
		return nil, fmt.Errorf("failed to write attachment data: %w", err)
	}

	// Create database record
	uploadedFile := database.UploadedFile{
		FileID:     fileID,
		FileName:   filename,
		Size:       int64(len(attachmentData)),
		MIMEType:   contentType,
		StorageURL: filePath,
		OwnerID:    integrationOwner.ID,
	}

	// Add Signal-specific metadata
	metadata := map[string]interface{}{
		"source":            "signal_integration",
		"original_filename": filename,
	}
	metadataBytes, _ := json.Marshal(metadata)
	uploadedFile.MetaData = metadataBytes

	// Save to database
	if err := s.DB.Create(&uploadedFile).Error; err != nil {
		// Clean up file if database save fails
		os.Remove(filePath)
		return nil, fmt.Errorf("failed to save file record: %w", err)
	}

	log.Printf("[Signal] Successfully uploaded attachment %s to backend (FileID: %s)", filename, fileID)
	return &uploadedFile, nil
}

// checkForAttachments checks if a message contains attachments
func (s *SignalIntegrationService) checkForAttachments(messageData map[string]interface{}) bool {
	if envelope, ok := messageData["envelope"].(map[string]interface{}); ok {
		// Check for attachments in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if atts, ok := dataMessage["attachments"].([]interface{}); ok && len(atts) > 0 {
				return true
			}
		}

		// Check for attachments in syncMessage.sentMessage
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if atts, ok := sentMessage["attachments"].([]interface{}); ok && len(atts) > 0 {
					return true
				}
			}
		}
	}
	return false
}

// processAttachments processes attachments from a Signal message
func (s *SignalIntegrationService) processAttachments(connection *SignalConnection, messageData map[string]interface{}, integrationOwner database.User, signalUser database.User) ([]map[string]interface{}, error) {
	var attachments []map[string]interface{}

	// Extract attachments from the message
	var signalAttachments []SignalAttachment

	// Check for attachments in different message structures
	if envelope, ok := messageData["envelope"].(map[string]interface{}); ok {
		// Check for attachments in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if atts, ok := dataMessage["attachments"].([]interface{}); ok {
				for _, att := range atts {
					if attMap, ok := att.(map[string]interface{}); ok {
						attachment := SignalAttachment{}
						if id, ok := attMap["id"].(string); ok {
							attachment.ID = id
						}
						if contentType, ok := attMap["contentType"].(string); ok {
							attachment.ContentType = contentType
						}
						if filename, ok := attMap["filename"].(string); ok {
							attachment.Filename = filename
						}
						if size, ok := attMap["size"].(float64); ok {
							attachment.Size = int64(size)
						}
						if width, ok := attMap["width"].(float64); ok {
							w := int(width)
							attachment.Width = &w
						}
						if height, ok := attMap["height"].(float64); ok {
							h := int(height)
							attachment.Height = &h
						}
						if caption, ok := attMap["caption"].(string); ok {
							attachment.Caption = &caption
						}
						if uploadTimestamp, ok := attMap["uploadTimestamp"].(float64); ok {
							ts := int64(uploadTimestamp)
							attachment.UploadTimestamp = &ts
						}
						signalAttachments = append(signalAttachments, attachment)
					}
				}
			}
		}

		// Check for attachments in syncMessage.sentMessage
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if atts, ok := sentMessage["attachments"].([]interface{}); ok {
					for _, att := range atts {
						if attMap, ok := att.(map[string]interface{}); ok {
							attachment := SignalAttachment{}
							if id, ok := attMap["id"].(string); ok {
								attachment.ID = id
							}
							if contentType, ok := attMap["contentType"].(string); ok {
								attachment.ContentType = contentType
							}
							if filename, ok := attMap["filename"].(string); ok {
								attachment.Filename = filename
							}
							if size, ok := attMap["size"].(float64); ok {
								attachment.Size = int64(size)
							}
							if width, ok := attMap["width"].(float64); ok {
								w := int(width)
								attachment.Width = &w
							}
							if height, ok := attMap["height"].(float64); ok {
								h := int(height)
								attachment.Height = &h
							}
							if caption, ok := attMap["caption"].(string); ok {
								attachment.Caption = &caption
							}
							if uploadTimestamp, ok := attMap["uploadTimestamp"].(float64); ok {
								ts := int64(uploadTimestamp)
								attachment.UploadTimestamp = &ts
							}
							signalAttachments = append(signalAttachments, attachment)
						}
					}
				}
			}
		}
	}

	// Process each attachment
	for _, signalAttachment := range signalAttachments {
		log.Printf("[Signal:%s] Processing attachment: %s (%s, %d bytes)",
			connection.Alias, signalAttachment.ID, signalAttachment.Filename, signalAttachment.Size)

		// Download the attachment from Signal REST API
		attachmentData, contentType, err := s.downloadSignalAttachment(connection, signalAttachment.ID)
		if err != nil {
			log.Printf("[Signal:%s] Failed to download attachment %s: %v", connection.Alias, signalAttachment.ID, err)
			continue
		}

		// Upload to our backend
		uploadedFile, err := s.uploadAttachmentToBackend(attachmentData, signalAttachment.Filename, contentType, integrationOwner)
		if err != nil {
			log.Printf("[Signal:%s] Failed to upload attachment %s to backend: %v", connection.Alias, signalAttachment.ID, err)
			continue
		}

		// Ensure both chat participants have access to the file:
		// - File is owned by integration owner (who uploaded it)
		// - File is shared with Signal user (the other chat participant)
		// This ensures both participants can view/download the attachment
		var existingAccess database.FileAccess
		result := s.DB.Where("user_id = ? AND uploaded_file_id = ?", signalUser.ID, uploadedFile.ID).First(&existingAccess)
		if result.Error != nil {
			// File access doesn't exist, create it
			fileAccess := database.FileAccess{
				UserID:         signalUser.ID,
				UploadedFileID: uploadedFile.ID,
				Permission:     "view",
				CreatedAt:      time.Now(),
			}
			if err := s.DB.Create(&fileAccess).Error; err != nil {
				log.Printf("[Signal:%s] Error sharing file %s (ID: %d) with Signal user %d: %v",
					connection.Alias, uploadedFile.FileID, uploadedFile.ID, signalUser.ID, err)
				// Don't fail the attachment processing if file sharing fails
			} else {
				log.Printf("[Signal:%s] Successfully shared file %s (ID: %d) with Signal user %d - both chat participants now have access",
					connection.Alias, uploadedFile.FileID, uploadedFile.ID, signalUser.ID)
			}
		} else {
			log.Printf("[Signal:%s] File %s (ID: %d) already shared with Signal user %d - both chat participants have access",
				connection.Alias, uploadedFile.FileID, uploadedFile.ID, signalUser.ID)
		}

		// Create attachment metadata for the message
		attachmentMeta := map[string]interface{}{
			"file_id":      uploadedFile.FileID,
			"file_name":    uploadedFile.FileName,
			"display_name": signalAttachment.Filename,
			"file_size":    uploadedFile.Size,
			"mime_type":    uploadedFile.MIMEType,
			"signal_id":    signalAttachment.ID,
		}

		// Add optional fields if available
		if signalAttachment.Width != nil {
			attachmentMeta["width"] = *signalAttachment.Width
		}
		if signalAttachment.Height != nil {
			attachmentMeta["height"] = *signalAttachment.Height
		}
		if signalAttachment.Caption != nil {
			attachmentMeta["caption"] = *signalAttachment.Caption
		}

		attachments = append(attachments, attachmentMeta)
		log.Printf("[Signal:%s] Successfully processed attachment %s", connection.Alias, signalAttachment.ID)
	}

	return attachments, nil
}

// processMessage processes a message received from the WebSocket
func (s *SignalIntegrationService) processMessage(connection *SignalConnection, messageBytes []byte) error {
	// Parse the message
	var messageData map[string]interface{}
	if err := json.Unmarshal(messageBytes, &messageData); err != nil {
		return fmt.Errorf("failed to parse message: %w", err)
	}

	// DEBUG: Print the parsed message as formatted JSON
	prettyJSON, err := json.MarshalIndent(messageData, "", "  ")
	if err != nil {
		log.Printf("[Signal:%s] Error formatting message JSON: %v", connection.Alias, err)
	} else {
		log.Printf("[Signal:%s] Parsed message JSON:\n%s", connection.Alias, string(prettyJSON))
	}

	// Check if this is a group message
	isGroupMsg := isGroupMessage(messageData)
	var groupId, groupName string
	if isGroupMsg {
		groupId, groupName, _ = extractGroupInfo(messageData)
		log.Printf("[Signal:%s] Processing group message - Group ID: %s, Group Name: %s", connection.Alias, groupId, groupName)

		// Check if the group ID is in the whitelist
		if !isGroupIdInWhitelist(groupId, connection.Integration) {
			log.Printf("[Signal:%s] Group ID %s not in whitelist, skipping group message", connection.Alias, groupId)
			return nil
		}

		// TODO: Future enhancement - Add group chat message processing logic here
		// For now, group messages are logged but not processed as AI messages
	}

	// Extract the source number and message text
	var sourceNumber, messageText, destinationNumber string
	var isSyncMessage bool
	var timestamp int64

	if envelope, ok := messageData["envelope"].(map[string]interface{}); ok {
		// Extract source number - with improved handling for UUID-only sources
		if source, ok := envelope["source"].(string); ok {
			// If source is available, use it (could be UUID or phone number)
			sourceNumber = source

			// If sourceNumber field exists and is not null, it has precedence
			if sourceNum, ok := envelope["sourceNumber"].(string); ok && sourceNum != "" {
				sourceNumber = sourceNum
			}
		} else if sourceNum, ok := envelope["sourceNumber"].(string); ok && sourceNum != "" {
			sourceNumber = sourceNum
		} else if sourceDevice, ok := envelope["sourceDevice"].(float64); ok {
			// This might be a sync message from our own device
			sourceNumber = connection.PhoneNumber
			isSyncMessage = sourceDevice > 0
		}

		// Extract timestamp
		if ts, ok := envelope["timestamp"].(float64); ok {
			timestamp = int64(ts)
		}

		// Extract message text - added direct check for dataMessage at envelope level
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if text, ok := dataMessage["message"].(string); ok {
				messageText = text
			}
		} else if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			isSyncMessage = true
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if text, ok := sentMessage["message"].(string); ok {
					messageText = text
				}

				// Extract destination from sentMessage
				if destination, ok := sentMessage["destination"].(string); ok {
					destinationNumber = destination
				} else if destNumber, ok := sentMessage["destinationNumber"].(string); ok {
					destinationNumber = destNumber
				}

				// Use timestamp from sentMessage if available
				if ts, ok := sentMessage["timestamp"].(float64); ok {
					timestamp = int64(ts)
				}
			}
		} else if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if text, ok := dataMessage["message"].(string); ok {
					messageText = text
				}
			}

			// Also check for syncMessage in content (some message formats)
			if syncMessage, ok := content["syncMessage"].(map[string]interface{}); ok {
				isSyncMessage = true
				if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
					if text, ok := sentMessage["message"].(string); ok && messageText == "" {
						messageText = text
					}
					// For sync messages, we need to get the destination as the source
					if destination, ok := sentMessage["destination"].(string); ok {
						destinationNumber = destination
					} else if destNumber, ok := sentMessage["destinationNumber"].(string); ok {
						destinationNumber = destNumber
					} else if destinations, ok := sentMessage["destinations"].([]interface{}); ok && len(destinations) > 0 {
						if dest, ok := destinations[0].(string); ok {
							destinationNumber = dest
						}
					}
				}
			}
		}
	}

	// Default destination number to account phone number if empty
	if destinationNumber == "" {
		// First check if account field exists at root level
		if account, ok := messageData["account"].(string); ok && account != "" {
			destinationNumber = account
		} else {
			// Fall back to integration phone number
			destinationNumber = connection.PhoneNumber
		}
	}

	log.Printf("[Signal:%s] sourceNumber: %s, messageText: %s, destinationNumber: %s, isSyncMessage: %v",
		connection.Alias, sourceNumber, messageText, destinationNumber, isSyncMessage)

	// Skip if we couldn't extract the necessary information
	if sourceNumber == "" || messageText == "" {
		return nil
	}

	// Check if this is a "Note-To-Self" message (a sync message with no destination)
	isNoteToSelf := isSyncMessage && destinationNumber == ""
	if isNoteToSelf {
		log.Printf("[Signal:%s] Skipping Note-To-Self message", connection.Alias)
		return nil
	}

	// Determine the actual conversation participants
	// For sync messages (sent by owner), use the destination as the conversation partner
	// For received messages, use the source as the conversation partner
	// For group messages, use the group ID as the conversation identifier
	var conversationPartner string
	if isGroupMsg {
		conversationPartner = groupId
	} else if isSyncMessage {
		conversationPartner = destinationNumber
	} else {
		conversationPartner = sourceNumber
	}

	// Check if the message is directed to the integration owner
	isToOwner := destinationNumber == connection.PhoneNumber
	isFromOwner := sourceNumber == connection.PhoneNumber

	// Check if the number is in the whitelist
	isWhitelisted := isNumberInWhitelist(sourceNumber, connection.Integration)

	// Check if this is from the admin (integration owner)
	isAdmin := sourceNumber == connection.PhoneNumber

	// First, check if the message has the /ai prefix
	hasAIPrefix := strings.HasPrefix(strings.ToLower(messageText), "/ai")

	// Process the message if:
	// 1. It's TO the owner (messages received by the integration phone number) AND
	// 2. Either:
	//    a. It's NOT a group message AND either has AI prefix OR is whitelisted, OR
	//    b. It IS a group message AND the sender is the integration owner AND has AI prefix
	// 3. For group messages, the group must be whitelisted (already checked above)
	shouldProcessMessage := false

	if isGroupMsg {
		// For group messages: only process if sender is integration owner AND has AI prefix
		shouldProcessMessage = isFromOwner && hasAIPrefix
	} else {
		// For individual messages: process if TO owner AND (has AI prefix OR is whitelisted)
		shouldProcessMessage = isToOwner && (hasAIPrefix ||
			(scheduler.DONT_REQUIRE_AI_COMMAND_PREFIX && isWhitelisted))
	}

	// Debug logging with proper Signal prefix
	log.Printf("[Signal:%s] Debug - isGroupMsg: %v, isToOwner: %v, isFromOwner: %v, hasAIPrefix: %v, isWhitelisted: %v, DONT_REQUIRE_AI_COMMAND_PREFIX: %v",
		connection.Alias, isGroupMsg, isToOwner, isFromOwner, hasAIPrefix, isWhitelisted, scheduler.DONT_REQUIRE_AI_COMMAND_PREFIX)

	// Find the integration owner
	var integrationOwner database.User
	if err := s.DB.First(&integrationOwner, connection.Integration.UserID).Error; err != nil {
		log.Printf("[Signal:%s] Error finding integration owner: %v", connection.Alias, err)
		return err
	}

	// Store the message in the database regardless of whether we'll process it with AI
	// Find the Signal user
	var signalUser database.User
	if err := s.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
		log.Printf("[Signal:%s] Error finding Signal user: %v", connection.Alias, err)
		return err
	}

	// Process attachments if any
	var processedAttachments []map[string]interface{}
	if hasAttachments := s.checkForAttachments(messageData); hasAttachments {
		log.Printf("[Signal:%s] Message contains attachments, processing them", connection.Alias)
		attachments, err := s.processAttachments(connection, messageData, integrationOwner, signalUser)
		if err != nil {
			log.Printf("[Signal:%s] Error processing attachments: %v", connection.Alias, err)
			// Continue processing the message even if attachment processing fails
		} else {
			processedAttachments = attachments
			log.Printf("[Signal:%s] Successfully processed %d attachments", connection.Alias, len(attachments))
		}
	}

	// Find or create the chat for this Signal conversation
	var chat database.Chat
	var chatFound bool

	// Use a transaction for database operations
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// Look for an existing chat between these users that matches the conversation partner and alias
		var chats []database.Chat
		if err := tx.Preload("SharedConfig").
			Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
				signalUser.ID, integrationOwner.ID, integrationOwner.ID, signalUser.ID).
			Find(&chats).Error; err != nil {
			return err
		}

		// Find the specific chat for this conversation partner and alias
		for _, c := range chats {
			if c.SharedConfig != nil {
				var configData map[string]interface{}
				if err := json.Unmarshal(c.SharedConfig.ConfigData, &configData); err == nil {
					configAlias, aliasOk := configData["signal_alias"].(string)

					if aliasOk && configAlias == connection.Alias {
						if isGroupMsg {
							// For group messages, check group ID
							configGroupId, groupIdOk := configData["signal_group_id"].(string)
							if groupIdOk && configGroupId == conversationPartner {
								chat = c
								chatFound = true
								break
							}
						} else {
							// For individual messages, check phone number
							configPhone, phoneOk := configData["signal_phone"].(string)
							if phoneOk && configPhone == conversationPartner {
								chat = c
								chatFound = true
								break
							}
						}
					}
				}
			}
		}

		// If no chat was found, create a new one
		if !chatFound {
			if isGroupMsg {
				log.Printf("[Signal:%s] Creating new group chat for group %s (%s)", connection.Alias, groupName, conversationPartner)
			} else {
				log.Printf("[Signal:%s] Creating new chat for conversation with %s", connection.Alias, conversationPartner)
			}

			// Create the chat first
			chatType := "integration:signal"
			if isGroupMsg {
				chatType = "integration:signal:group"
			}

			chat = database.Chat{
				User1Id:  integrationOwner.ID,
				User2Id:  signalUser.ID,
				ChatType: chatType,
			}

			if err := tx.Create(&chat).Error; err != nil {
				return err
			}

			// Now create a shared configuration for this chat
			configData := map[string]interface{}{
				"signal_alias":     connection.Alias,
				"integration_id":   connection.Integration.ID,
				"integration_uuid": connection.Integration.UUID,
			}

			if isGroupMsg {
				configData["signal_group_id"] = conversationPartner
				configData["signal_group_name"] = groupName
			} else {
				configData["signal_phone"] = conversationPartner
			}

			configBytes, err := json.Marshal(configData)
			if err != nil {
				return err
			}

			sharedConfig := database.SharedChatConfig{
				ChatId:     chat.ID,
				ConfigData: configBytes,
			}

			if err := tx.Create(&sharedConfig).Error; err != nil {
				return err
			}

			// Link the shared config to the chat
			chat.SharedConfigId = &sharedConfig.ID
			if err := tx.Save(&chat).Error; err != nil {
				return err
			}

			chatFound = true
		} else {
			if scheduler.LOG_SIGNAL_EVENTS {
				log.Printf("[Signal:%s] Found existing chat for conversation partner %s", connection.Alias, conversationPartner)
				log.Printf("[Signal:%s] Chat type: %s", connection.Alias, chat.ChatType)
				log.Printf("[Signal:%s] Chat UUID: %s", connection.Alias, chat.UUID)
				log.Printf("[Signal:%s] Chat ID: %d", connection.Alias, chat.ID)
			}
		}

		// Determine sender and receiver based on message source
		var senderId, receiverId uint
		if isFromOwner {
			senderId = integrationOwner.ID
			receiverId = signalUser.ID
		} else {
			senderId = signalUser.ID
			receiverId = integrationOwner.ID
		}

		// Create the message
		// For group messages, prefix the message with the sender's phone number
		messageTextToStore := messageText
		if isGroupMsg {
			messageTextToStore = fmt.Sprintf("[%s]: %s", sourceNumber, messageText)
		}

		dbMessage := database.Message{
			ChatId:     chat.ID,
			SenderId:   senderId,
			ReceiverId: receiverId,
			Text:       &messageTextToStore,
			DataType:   "text",
		}

		// Add metadata about the signal message
		metaData := map[string]interface{}{
			"signal_source":      sourceNumber,
			"signal_destination": destinationNumber,
			"signal_timestamp":   timestamp,
			"signal_alias":       connection.Alias,
			"is_sync_message":    isSyncMessage,
		}

		// Add group-specific metadata for group messages
		if isGroupMsg {
			metaData["signal_group_id"] = groupId
			metaData["signal_group_name"] = groupName
			metaData["is_group_message"] = true
		}

		// Add attachments metadata if any were processed
		if len(processedAttachments) > 0 {
			metaData["attachments"] = processedAttachments
		}

		metaDataBytes, err := json.Marshal(metaData)
		if err != nil {
			return err
		}

		dbMessage.MetaData = metaDataBytes

		// Save the message
		if err := tx.Create(&dbMessage).Error; err != nil {
			return err
		}

		// Update the chat's latest message
		chat.LatestMessageId = &dbMessage.ID
		if err := tx.Save(&chat).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		log.Printf("[Signal:%s] Error storing message in database: %v", connection.Alias, err)
		return err
	}

	// Process with AI if needed
	if shouldProcessMessage {
		if isGroupMsg {
			log.Printf("[Signal:%s] Processing group message from %s as AI (integration owner with /ai prefix)",
				connection.Alias, sourceNumber)
		} else {
			log.Printf("[Signal:%s] Processing message from %s (whitelisted: %v, admin: %v, has AI prefix: %v)",
				connection.Alias, sourceNumber, isWhitelisted, isAdmin, hasAIPrefix)
		}

		// If message has the /ai prefix, remove it before processing
		if hasAIPrefix {
			// Remove the /ai prefix and trim any leading whitespace
			messageText = strings.TrimSpace(strings.TrimPrefix(messageText, "/ai"))
		}

		return s.ProcessAIMessage(messageText, sourceNumber, connection.Alias, connection.Integration, processedAttachments)
	}

	if isGroupMsg {
		if isFromOwner && !hasAIPrefix {
			log.Printf("[Signal:%s] Group message from %s (integration owner) stored but not processed as AI (missing /ai prefix)",
				connection.Alias, sourceNumber)
		} else if !isFromOwner {
			log.Printf("[Signal:%s] Group message from %s stored but not processed as AI (not from integration owner)",
				connection.Alias, sourceNumber)
		} else {
			log.Printf("[Signal:%s] Group message from %s stored but not processed as AI (no /ai prefix)",
				connection.Alias, sourceNumber)
		}
	} else {
		log.Printf("[Signal:%s] Skipping AI processing for message from %s (not whitelisted or admin)",
			connection.Alias, sourceNumber)
	}
	return nil
}

// ProcessAIMessage processes a message with the AI
func (s *SignalIntegrationService) ProcessAIMessage(messageText, sourceNumber, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	log.Printf("[Signal:%s] ProcessAIMessage called with %d attachments", alias, len(attachments))
	if len(attachments) > 0 {
		log.Printf("[Signal:%s] Attachment details: %+v", alias, attachments)
	}

	// Call the scheduler's ProcessSignalMessage method
	return s.SchedulerService.ProcessSignalMessage(messageText, sourceNumber, alias, integration, attachments)
}
