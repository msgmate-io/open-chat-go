package integrations

import (
	"backend/database"
	"backend/scheduler"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
	"time"
)

// CreateSignalPollingTask creates a scheduled task for polling a Signal integration
func CreateSignalPollingTask(
	DB *gorm.DB,
	schedulerService *scheduler.SchedulerService,
	integration database.Integration,
) error {
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

	// Create a unique task name for this integration
	taskName := fmt.Sprintf("signal_poll_%s", alias)

	// Check if task already exists and remove it if it does
	if _, exists := schedulerService.GetTaskByName(taskName); exists {
		if err := schedulerService.RemoveTask(taskName); err != nil {
			log.Printf("Warning: Failed to remove existing task: %v", err)
		}
	}

	// Create the new task
	task := scheduler.Task{
		Name:        taskName,
		Description: fmt.Sprintf("Poll Signal messages for integration %s", alias),
		Schedule:    "*/1 * * * *", // Every minute
		Enabled:     true,
		Handler: func() error {
			return pollSignalMessages(int(port), alias, DB, integration.UserID)
		},
	}

	// Add the task to the scheduler
	return schedulerService.AddTask(task)
}

// pollSignalMessages fetches new messages from the Signal REST API
func pollSignalMessages(port int, alias string, DB *gorm.DB, userID uint) error {
	// First, get the integration to retrieve the phone number
	var integration database.Integration
	log.Printf("[Signal:%s] Looking up integration for userID: %d", alias, userID)
	if err := DB.First(&integration, "user_id = ? AND integration_name = ?", userID, alias).Error; err != nil {
		log.Printf("[Signal:%s] Failed to find integration: %v", alias, err)
		return fmt.Errorf("failed to find integration: %w", err)
	}

	// Parse the integration config to get the phone number
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("[Signal:%s] Failed to parse integration config: %v", alias, err)
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	phoneNumber, ok := config["phone_number"].(string)
	if !ok {
		log.Printf("[Signal:%s] Invalid phone_number in integration config", alias)
		return fmt.Errorf("invalid phone_number in integration config")
	}

	// Log the configuration details
	log.Printf("[Signal:%s] Configuration - Phone: %s, Port: %d", alias, phoneNumber, port)

	// Construct the URL for the Signal REST API with the phone number
	url := fmt.Sprintf("http://localhost:%d/v1/receive/%s", port, phoneNumber)
	log.Printf("[Signal:%s] Requesting messages from: %s", alias, url)

	// Create a new HTTP client with a timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Make the request
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("[Signal:%s] Failed to connect to Signal REST API: %v", alias, err)
		return fmt.Errorf("failed to connect to Signal REST API: %w", err)
	}
	defer resp.Body.Close()

	// Check the response status
	if resp.StatusCode != http.StatusOK {
		log.Printf("[Signal:%s] Signal REST API returned non-OK status: %d", alias, resp.StatusCode)
		// Read and log response body for more details
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("[Signal:%s] Response body: %s", alias, string(bodyBytes))
		return fmt.Errorf("Signal REST API returned non-OK status: %d", resp.StatusCode)
	}

	log.Printf("[Signal:%s] Received response with status: %d", alias, resp.StatusCode)

	// Parse the response
	var messages []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		log.Printf("[Signal:%s] Failed to parse Signal messages: %v", alias, err)
		return fmt.Errorf("failed to parse Signal messages: %w", err)
	}

	log.Printf("[Signal:%s] Parsed %d messages", alias, len(messages))

	// Process each message
	for _, message := range messages {
		// Extract relevant fields from the message
		var sourceNumber, messageText string
		var timestamp int64

		if source, ok := message["source"].(string); ok {
			sourceNumber = source
		}

		if envelope, ok := message["envelope"].(map[string]interface{}); ok {
			if content, ok := envelope["content"].(map[string]interface{}); ok {
				if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
					if text, ok := dataMessage["message"].(string); ok {
						messageText = text
					}
					if ts, ok := dataMessage["timestamp"].(float64); ok {
						timestamp = int64(ts)
					}
				}
			}
		}

		// Just print the message to the console
		log.Printf("[Signal:%s] From: %s, Message: %s, Time: %s",
			alias,
			sourceNumber,
			messageText,
			time.Unix(timestamp/1000, 0).Format(time.RFC3339))
	}

	// Update the last_used timestamp for the integration
	now := time.Now()
	integration.LastUsed = &now
	if err := DB.Save(&integration).Error; err != nil {
		log.Printf("[Signal:%s] Failed to update integration last_used: %v", alias, err)
	} else {
		log.Printf("[Signal:%s] Updated last_used timestamp", alias)
	}

	return nil
}
