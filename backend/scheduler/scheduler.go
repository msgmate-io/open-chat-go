package scheduler

import (
	"backend/api"
	"backend/api/msgmate"
	"backend/client"
	"backend/database"
	"backend/server/util"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/go-co-op/gocron"
	"gorm.io/gorm"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Flag to control logging of Signal events
const LOG_SIGNAL_EVENTS = false

// Flag to control whether AI processing requires the /ai prefix
const DONT_REQUIRE_AI_COMMAND_PREFIX = false

// Helper function to check if a number is in the whitelist
func isNumberInWhitelist(number string, integration database.Integration) bool {
	// Parse the integration config
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("Error parsing integration config: %v", err)
		return false
	}

	// Get the whitelist from the config
	if whitelistInterface, exists := config["whitelist"]; exists {
		if whitelistArray, ok := whitelistInterface.([]interface{}); ok {
			// If whitelist is empty, don't auto-process as AI
			if len(whitelistArray) == 0 {
				log.Printf("Whitelist exists but is empty, not auto-processing as AI")
				return false
			}

			// Check if the number is in the whitelist
			for _, item := range whitelistArray {
				if str, ok := item.(string); ok && str == number {
					log.Printf("Number %s found in whitelist", number)
					return true
				}
			}

			log.Printf("Number %s not found in whitelist of %d entries", number, len(whitelistArray))
		} else {
			log.Printf("Whitelist is not an array, cannot check if number is whitelisted")
		}
	} else {
		// No whitelist at all
		log.Printf("No whitelist found in integration config")
		return false
	}

	return false
}

// Add this function near the isNumberInWhitelist function
func isGroupMessage(message map[string]interface{}) bool {
	// Check if this is a group message by looking for groupInfo or groupId
	if envelope, ok := message["envelope"].(map[string]interface{}); ok {
		// Check for groupInfo in dataMessage
		if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
			if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
				return groupInfo != nil
			}
		}

		// Check for groupId in content.dataMessage.groupInfo
		if content, ok := envelope["content"].(map[string]interface{}); ok {
			if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := dataMessage["groupInfo"].(map[string]interface{}); ok {
					return groupInfo != nil
				}
			}

			// Check for groupId in typingMessage
			if typingMessage, ok := content["typingMessage"].(map[string]interface{}); ok {
				if _, ok := typingMessage["groupId"].(string); ok {
					return true
				}
			}
		}

		// Check for syncMessage with groupInfo
		if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
			if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
				if groupInfo, ok := sentMessage["groupInfo"].(map[string]interface{}); ok {
					return groupInfo != nil
				}
			}
		}
	}

	return false
}

// SchedulerService manages all scheduled tasks
type SchedulerService struct {
	scheduler         *gocron.Scheduler
	DB                *gorm.DB
	ctx               context.Context
	cancel            context.CancelFunc
	federationHandler api.FederationHandlerInterface
	registeredTasks   map[string]Task
	serverURL         string // Add this field
}

// NewSchedulerService creates a new scheduler service
func NewSchedulerService(DB *gorm.DB, federationHandler api.FederationHandlerInterface, serverURL string) *SchedulerService {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a scheduler with UTC timezone
	s := gocron.NewScheduler(time.UTC)

	service := &SchedulerService{
		scheduler:         s,
		DB:                DB,
		ctx:               ctx,
		cancel:            cancel,
		federationHandler: federationHandler,
		registeredTasks:   make(map[string]Task),
		serverURL:         serverURL, // Store the server URL
	}

	return service
}

// Start begins running the scheduler
func (s *SchedulerService) Start() {
	log.Println("Starting scheduler service...")
	//s.InitializeIntegrationTasks()
	s.scheduler.StartAsync()
}

// Stop halts all scheduled jobs
func (s *SchedulerService) Stop() {
	log.Println("Stopping scheduler service...")
	s.scheduler.Stop()
	s.cancel()
}

// RegisterTasks sets up all scheduled tasks
func (s *SchedulerService) RegisterTasks() {
	// Register system maintenance tasks
	s.registerTaskGroup(SystemMaintenanceTasks(s.DB))

	// Register data maintenance tasks
	s.registerTaskGroup(DataMaintenanceTasks(s.DB))

	// Register network tasks if federation handler is available
	//if s.federationHandler != nil {
	//	s.registerTaskGroup(NetworkTasks(s.DB, s.federationHandler))
	//}

	log.Printf("Registered %d scheduled tasks", len(s.registeredTasks))
}

// registerTaskGroup registers a group of tasks
func (s *SchedulerService) registerTaskGroup(tasks []Task) {
	for _, task := range tasks {
		if !task.Enabled {
			log.Printf("Skipping disabled task: %s", task.Name)
			continue
		}

		s.registerTask(task)
	}
}

// registerTask registers a single task with the scheduler
func (s *SchedulerService) registerTask(task Task) {
	// Store the task in our registry
	s.registeredTasks[task.Name] = task

	// Parse the cron schedule
	job, err := s.scheduler.Cron(task.Schedule).Do(func() {
		log.Printf("Running scheduled task: %s - %s", task.Name, task.Description)

		if err := task.Handler(); err != nil {
			log.Printf("Error in task %s: %v", task.Name, err)
		} else {
			log.Printf("Task %s completed successfully", task.Name)
		}
	})

	if err != nil {
		log.Printf("Error scheduling task %s: %v", task.Name, err)
		return
	}

	// Set job metadata
	job.Tag(task.Name)

	log.Printf("Registered task: %s (%s)", task.Name, task.Schedule)
}

// GetTaskByName returns a task by its name
func (s *SchedulerService) GetTaskByName(name string) (Task, bool) {
	task, exists := s.registeredTasks[name]
	return task, exists
}

// ListTasks returns all registered tasks
func (s *SchedulerService) ListTasks() []Task {
	tasks := make([]Task, 0, len(s.registeredTasks))
	for _, task := range s.registeredTasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// RunTaskNow runs a task immediately by name
func (s *SchedulerService) RunTaskNow(name string) error {
	task, exists := s.registeredTasks[name]
	if !exists {
		return fmt.Errorf("task %s not found", name)
	}

	return task.Handler()
}

// Task implementations

// cleanExpiredSessions removes expired sessions from the database
func (s *SchedulerService) cleanExpiredSessions() {
	result := s.DB.Where("expiry < ?", time.Now()).Delete(&database.Session{})
	if result.Error != nil {
		log.Printf("Error cleaning expired sessions: %v", result.Error)
		return
	}
	log.Printf("Cleaned %d expired sessions", result.RowsAffected)
}

// archiveOldMessages archives messages older than a certain threshold
func (s *SchedulerService) archiveOldMessages() {
	// Example implementation - you would customize this based on your needs
	// This might involve moving messages to an archive table or marking them as archived
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	var oldMessages []database.Message
	result := s.DB.Where("created_at < ?", thirtyDaysAgo).Find(&oldMessages)
	if result.Error != nil {
		log.Printf("Error finding old messages: %v", result.Error)
		return
	}

	log.Printf("Found %d messages to archive", len(oldMessages))
	// Implement your archiving logic here
}

// checkIntegrationsHealth checks the health of all active integrations
func (s *SchedulerService) checkIntegrationsHealth() {
	var integrations []database.Integration
	result := s.DB.Where("active = ?", true).Find(&integrations)
	if result.Error != nil {
		log.Printf("Error finding active integrations: %v", result.Error)
		return
	}

	for _, integration := range integrations {
		// Check integration health based on type
		switch integration.IntegrationType {
		case "signal":
			s.checkSignalIntegrationHealth(integration)
		// Add other integration types as needed
		default:
			log.Printf("Unknown integration type: %s", integration.IntegrationType)
		}
	}
}

// checkSignalIntegrationHealth checks if a Signal integration is healthy
func (s *SchedulerService) checkSignalIntegrationHealth(integration database.Integration) {
	// Parse the integration config
	var config map[string]interface{}
	if err := util.ParseJSON(integration.Config, &config); err != nil {
		log.Printf("Error parsing integration config: %v", err)
		return
	}

	// Example implementation - check if the Docker container is running
	alias, ok := config["alias"].(string)
	if !ok {
		log.Printf("Invalid alias in integration config")
		return
	}

	// Update last_used timestamp if the integration is healthy
	now := time.Now()
	integration.LastUsed = &now
	if err := s.DB.Save(&integration).Error; err != nil {
		log.Printf("Error updating integration last_used: %v", err)
	}

	log.Printf("Signal integration %s is healthy", alias)
}

// syncNetworks synchronizes network data
func (s *SchedulerService) syncNetworks() {
	var networks []database.Network
	result := s.DB.Find(&networks)
	if result.Error != nil {
		log.Printf("Error finding networks: %v", result.Error)
		return
	}

	for _, network := range networks {
		log.Printf("Syncing network: %s", network.NetworkName)
		// Implement your network sync logic here
	}
}

// AddTask adds a new task to the scheduler dynamically
func (s *SchedulerService) AddTask(task Task) error {
	// Check if a task with this name already exists
	if _, exists := s.registeredTasks[task.Name]; exists {
		return fmt.Errorf("task with name '%s' already exists", task.Name)
	}

	// Register the task with the scheduler
	s.registerTask(task)

	return nil
}

// RemoveTask removes a task from the scheduler by name
func (s *SchedulerService) RemoveTask(taskName string) error {
	// Check if the task exists
	if _, exists := s.registeredTasks[taskName]; !exists {
		return fmt.Errorf("task with name '%s' does not exist", taskName)
	}

	// Remove the task from our registry
	delete(s.registeredTasks, taskName)

	// Remove the job from the scheduler
	s.scheduler.RemoveByTag(taskName)

	log.Printf("Removed task: %s", taskName)
	return nil
}

// InitializeIntegrationTasks sets up tasks for existing integrations
func (s *SchedulerService) InitializeIntegrationTasks() {
	// Find all active integrations
	var integrations []database.Integration
	if err := s.DB.Where("active = ?", true).Find(&integrations).Error; err != nil {
		log.Printf("Error finding active integrations: %v", err)
		return
	}

	log.Printf("Initializing tasks for %d active integrations", len(integrations))

	// For each integration, create appropriate tasks based on type
	for _, integration := range integrations {
		switch integration.IntegrationType {
		case "signal":
			// Parse the integration config
			var config map[string]interface{}
			if err := json.Unmarshal(integration.Config, &config); err != nil {
				log.Printf("Error parsing integration config: %v", err)
				continue
			}

			alias, ok := config["alias"].(string)
			if !ok {
				log.Printf("Invalid alias in integration config")
				continue
			}

			port, ok := config["port"].(float64)
			if !ok {
				log.Printf("Invalid port in integration config")
				continue
			}

			// Create a task for polling Signal messages
			taskName := fmt.Sprintf("signal_poll_%s", alias)
			task := Task{
				Name:        taskName,
				Description: fmt.Sprintf("Poll Signal messages for integration %s", alias),
				Schedule:    "@every 4s", // Every 4 seconds instead of "*/1 * * * *"
				Enabled:     true,
				Handler: func() error {
					// Get the phone number from the config
					phoneNumber, ok := config["phone_number"].(string)
					if !ok {
						return fmt.Errorf("invalid phone_number in integration config")
					}

					// Log the configuration details
					log.Printf("[Signal:%s] Configuration - Phone: %s, Port: %d", alias, phoneNumber, int(port))

					// Construct the URL for the Signal REST API with the phone number
					url := fmt.Sprintf("http://localhost:%d/v1/receive/%s", int(port), phoneNumber)
					log.Printf("[Signal:%s] Requesting messages from: %s", alias, url)

					// Create a new HTTP client with a longer timeout
					client := &http.Client{
						Timeout: 30 * time.Second, // Increase timeout to 30 seconds
					}

					// Make the request with retry logic
					var resp *http.Response
					var err error
					maxRetries := 3
					for attempt := 1; attempt <= maxRetries; attempt++ {
						log.Printf("[Signal:%s] Attempt %d of %d to connect to Signal REST API", alias, attempt, maxRetries)

						resp, err = client.Get(url)
						if err == nil {
							break // Success, exit retry loop
						}

						if attempt < maxRetries {
							retryDelay := time.Duration(attempt) * 2 * time.Second
							log.Printf("[Signal:%s] Connection failed, retrying in %v: %v", alias, retryDelay, err)
							time.Sleep(retryDelay)
						}
					}

					if err != nil {
						log.Printf("[Signal:%s] All connection attempts failed: %v", alias, err)
						return fmt.Errorf("failed to connect to Signal REST API after %d attempts: %w", maxRetries, err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						log.Printf("[Signal:%s] Signal REST API returned non-OK status: %d", alias, resp.StatusCode)
						// Read and log response body for more details
						bodyBytes, _ := io.ReadAll(resp.Body)
						log.Printf("[Signal:%s] Response body: %s", alias, string(bodyBytes))
						return fmt.Errorf("Signal REST API returned non-OK status: %d", resp.StatusCode)
					}

					log.Printf("[Signal:%s] Received response with status: %d", alias, resp.StatusCode)

					// Read the response body into a byte slice
					bodyBytes, err := io.ReadAll(resp.Body)
					if err != nil {
						log.Printf("[Signal:%s] Failed to read response body: %v", alias, err)
						return fmt.Errorf("failed to read response body: %w", err)
					}

					// Log the raw JSON response
					log.Printf("[Signal:%s] Raw JSON response: %s", alias, string(bodyBytes))

					// Create a new reader from the byte slice for JSON decoding
					bodyReader := bytes.NewReader(bodyBytes)

					var messages []map[string]interface{}
					if err := json.NewDecoder(bodyReader).Decode(&messages); err != nil {
						log.Printf("[Signal:%s] Failed to parse Signal messages: %v", alias, err)
						return fmt.Errorf("failed to parse Signal messages: %w", err)
					}

					log.Printf("[Signal:%s] Parsed %d messages", alias, len(messages))

					// Find the Signal user
					var signalUser database.User
					if err := s.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
						log.Printf("[Signal:%s] Error finding Signal user: %v", alias, err)
						return fmt.Errorf("error finding Signal user: %w", err)
					}

					// Find the integration owner
					var integrationOwner database.User
					if err := s.DB.First(&integrationOwner, integration.UserID).Error; err != nil {
						log.Printf("[Signal:%s] Error finding integration owner: %v", alias, err)
						return fmt.Errorf("error finding integration owner: %w", err)
					}

					// Process each message
					for _, message := range messages {
						// First, check if this is a group message and skip it early
						if isGroupMessage(message) {
							jsonBytes, _ := json.Marshal(message)
							log.Printf("[Signal:%s] Skipping group message: %s", alias, string(jsonBytes))
							continue
						}

						var sourceNumber, destinationNumber, messageText string
						var timestamp int64
						var isTypingMessage, isReadReceipt, isBlockedMessage, isSyncMessage bool

						// Extract message details from envelope
						if envelope, ok := message["envelope"].(map[string]interface{}); ok {
							// Extract source number
							if source, ok := envelope["source"].(string); ok {
								sourceNumber = source
							} else if sourceNumber, ok := envelope["sourceNumber"].(string); ok {
								sourceNumber = sourceNumber
							}

							// Check for various message types
							// Check for syncMessage (when user sends a message)
							if syncMessage, ok := envelope["syncMessage"].(map[string]interface{}); ok {
								isSyncMessage = true

								// Check for blocked contacts
								if blockedNumbers, ok := syncMessage["blockedNumbers"]; ok {
									isBlockedMessage = true
									if LOG_SIGNAL_EVENTS {
										// Create a descriptive message for blocked numbers event
										messageText = fmt.Sprintf("[signal-event] Blocked numbers updated: %v", blockedNumbers)
									}
								}

								if sentMessage, ok := syncMessage["sentMessage"].(map[string]interface{}); ok {
									if text, ok := sentMessage["message"].(string); ok {
										messageText = text
									} else if LOG_SIGNAL_EVENTS {
										messageText = "[signal-event] Empty message sent"
									}

									if ts, ok := sentMessage["timestamp"].(float64); ok {
										timestamp = int64(ts)
									} else if ts, ok := envelope["timestamp"].(float64); ok {
										timestamp = int64(ts)
									}

									// Extract destination number for sync messages
									if destination, ok := sentMessage["destination"].(string); ok {
										destinationNumber = destination
									} else if destNumber, ok := sentMessage["destinationNumber"].(string); ok {
										destinationNumber = destNumber
									}
								} else if LOG_SIGNAL_EVENTS && messageText == "" {
									// Handle other sync message types
									syncMessageBytes, _ := json.Marshal(syncMessage)
									messageText = fmt.Sprintf("[signal-event] Sync event: %s", string(syncMessageBytes))
								}
							}

							// Check for dataMessage (when receiving a message from someone else)
							if dataMessage, ok := envelope["dataMessage"].(map[string]interface{}); ok {
								if text, ok := dataMessage["message"].(string); ok {
									messageText = text
								} else if LOG_SIGNAL_EVENTS && messageText == "" {
									dataMessageBytes, _ := json.Marshal(dataMessage)
									messageText = fmt.Sprintf("[signal-event] Data message event: %s", string(dataMessageBytes))
								}

								if ts, ok := dataMessage["timestamp"].(float64); ok {
									timestamp = int64(ts)
								}
							}

							// Also check for dataMessage at the content level for some message formats
							if content, ok := envelope["content"].(map[string]interface{}); ok {
								if dataMessage, ok := content["dataMessage"].(map[string]interface{}); ok {
									if text, ok := dataMessage["message"].(string); ok {
										messageText = text
									} else if LOG_SIGNAL_EVENTS && messageText == "" {
										dataMessageBytes, _ := json.Marshal(dataMessage)
										messageText = fmt.Sprintf("[signal-event] Content data message event: %s", string(dataMessageBytes))
									}

									if ts, ok := dataMessage["timestamp"].(float64); ok {
										timestamp = int64(ts)
									}
								}

								// Check for typing indicator
								if typingMessage, ok := content["typingMessage"].(map[string]interface{}); ok {
									isTypingMessage = true
									if LOG_SIGNAL_EVENTS {
										messageText = "[signal-event] Typing indicator"
									}

									if ts, ok := typingMessage["timestamp"].(float64); ok {
										timestamp = int64(ts)
									}
								}

								// Check for read receipts
								if receiptMessage, ok := content["receiptMessage"].(map[string]interface{}); ok {
									isReadReceipt = true
									if LOG_SIGNAL_EVENTS {
										messageText = "[signal-event] Read receipt"
									}

									if ts, ok := receiptMessage["timestamp"].(float64); ok {
										timestamp = int64(ts)
									}
								}
							}

							// If we still don't have a timestamp, try to get it from the envelope
							if timestamp == 0 {
								if ts, ok := envelope["timestamp"].(float64); ok {
									timestamp = int64(ts)
								}
							}
						}

						// Skip processing for typing indicators, read receipts, and other non-message events
						if (isTypingMessage || isReadReceipt || isBlockedMessage) && !LOG_SIGNAL_EVENTS {
							jsonBytes, _ := json.Marshal(message)
							log.Printf("[Signal:%s] Skipping non-message event: %s", alias, string(jsonBytes))
							continue
						}

						// Skip if we don't have actual message text (unless it's a logged event)
						if messageText == "" && !(LOG_SIGNAL_EVENTS && (isTypingMessage || isReadReceipt || isBlockedMessage)) {
							log.Printf("[Signal:%s] Skipping message with empty text", alias)
							continue
						}

						// Determine the conversation participants
						var conversationPartner string

						// If this is a sync message (sent by owner), use the destination as the conversation partner
						if isSyncMessage && destinationNumber != "" {
							conversationPartner = destinationNumber
							log.Printf("[Signal:%s] Processing sync message - From: %s, To: %s, Message: %s, Time: %s",
								alias,
								sourceNumber,
								destinationNumber,
								messageText,
								time.Unix(timestamp/1000, 0).Format(time.RFC3339))
						} else {
							// For received messages, use the source as the conversation partner
							conversationPartner = sourceNumber
							log.Printf("[Signal:%s] Processing received message - From: %s, Message: %s, Time: %s",
								alias,
								sourceNumber,
								messageText,
								time.Unix(timestamp/1000, 0).Format(time.RFC3339))
						}

						// Skip if conversation partner is empty
						if conversationPartner == "" {
							log.Printf("[Signal:%s] Skipping message with no clear conversation partner", alias)
							continue
						}

						// Determine if the message is from the integration owner
						isFromOwner := isSyncMessage || sourceNumber == phoneNumber

						// Find the Signal user
						var signalUser database.User
						if err := s.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
							log.Printf("[Signal:%s] Error finding Signal user: %v", alias, err)
							continue
						}

						// Find the integration owner
						var integrationOwner database.User
						if err := s.DB.First(&integrationOwner, integration.UserID).Error; err != nil {
							log.Printf("[Signal:%s] Error finding integration owner: %v", alias, err)
							continue
						}

						// Process the message in a transaction
						err := s.DB.Transaction(func(tx *gorm.DB) error {
							// First, try to find an existing chat for this conversation partner and integration
							var chat database.Chat
							var chatFound bool

							// Look for a chat with shared config containing this conversation partner and alias
							var chats []database.Chat
							if err := tx.Preload("User1").Preload("User2").Preload("SharedConfig").
								Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
									signalUser.ID, integrationOwner.ID, integrationOwner.ID, signalUser.ID).
								Find(&chats).Error; err != nil {
								return err
							}

							// Check each chat's shared config to find one for this conversation partner and alias
							for _, c := range chats {
								if c.SharedConfig != nil {
									var configData map[string]interface{}
									if err := json.Unmarshal(c.SharedConfig.ConfigData, &configData); err == nil {
										configAlias, aliasOk := configData["signal_alias"].(string)
										configPhone, phoneOk := configData["signal_phone"].(string)

										if aliasOk && phoneOk && configAlias == alias && configPhone == conversationPartner {
											chat = c
											chatFound = true
											break
										}
									}
								}
							}

							// If no chat found, create a new one
							if !chatFound {
								log.Printf("[Signal:%s] Creating new chat for conversation partner %s", alias, conversationPartner)

								// Create a new chat between the Signal user and the integration owner
								var newChat database.Chat
								if signalUser.ID < integrationOwner.ID {
									newChat = database.Chat{
										User1Id:  signalUser.ID,
										User2Id:  integrationOwner.ID,
										ChatType: "integration:signal",
									}
								} else {
									newChat = database.Chat{
										User1Id:  integrationOwner.ID,
										User2Id:  signalUser.ID,
										ChatType: "integration:signal",
									}
								}

								if err := tx.Create(&newChat).Error; err != nil {
									return err
								}

								// Create shared config for the chat with conversation partner and alias
								configData := map[string]interface{}{
									"signal_phone":   conversationPartner,
									"signal_alias":   alias,
									"integration_id": integration.ID,
								}

								configBytes, err := json.Marshal(configData)
								if err != nil {
									return err
								}

								sharedConfig := database.SharedChatConfig{
									ChatId:     newChat.ID,
									ConfigData: configBytes,
								}

								if err := tx.Create(&sharedConfig).Error; err != nil {
									return err
								}

								// Update chat with shared config
								newChat.SharedConfigId = &sharedConfig.ID
								if err := tx.Save(&newChat).Error; err != nil {
									return err
								}

								// Load the chat with all relationships
								if err := tx.Preload("User1").Preload("User2").Preload("SharedConfig").
									First(&chat, newChat.ID).Error; err != nil {
									return err
								}
							} else {
								log.Printf("[Signal:%s] Found existing chat for conversation partner %s", alias, conversationPartner)
								log.Printf("[Signal:%s] Chat type: %s", alias, chat.ChatType)
								log.Printf("[Signal:%s] Chat UUID: %s", alias, chat.UUID)
								log.Printf("[Signal:%s] Chat ID: %d", alias, chat.ID)
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
							dbMessage := database.Message{
								ChatId:     chat.ID,
								SenderId:   senderId,
								ReceiverId: receiverId,
								Text:       &messageText,
								DataType:   "text",
							}

							// Add metadata about the signal message
							metaData := map[string]interface{}{
								"signal_source":      sourceNumber,
								"signal_destination": destinationNumber,
								"signal_timestamp":   timestamp,
								"signal_alias":       alias,
								"is_sync_message":    isSyncMessage,
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
							log.Printf("[Signal:%s] Error processing message: %v", alias, err)
							continue
						}

						isToOwner := false
						if isSyncMessage {
							// For sync messages, check if destination matches owner's number
							isToOwner = destinationNumber == phoneNumber
						} else {
							// For received messages, the destination is implicitly the owner's number (integration number)
							isToOwner = true
						}

						// Determine if this is a command message (starts with '/')
						isCommand := len(messageText) > 0 && messageText[0] == '/'

						// Process command if this is a command message
						if isCommand {
							// For sync messages (sent by owner), check if destination is the owner
							// For received messages (from others to owner), the destination is implicitly the owner's number

							if isToOwner {
								parts := strings.SplitN(messageText[1:], " ", 2)
								command := parts[0]
								var commandText string
								if len(parts) > 1 {
									commandText = parts[1]
								} else {
									commandText = ""
								}

								log.Printf("[Signal:%s] Processing command message: /%s %s", alias, command, commandText)

								// Process the command
								if err := s.processSignalCommand(command, commandText, sourceNumber, alias, integration); err != nil {
									log.Printf("[Signal:%s] Error processing command: %v", alias, err)
								} else {
									log.Printf("[Signal:%s] Successfully processed command: %s", alias, command)
								}
							} else {
								log.Printf("[Signal:%s] Command message not directed to owner, skipping command processing", alias)
							}
						} else if DONT_REQUIRE_AI_COMMAND_PREFIX && isToOwner {
							// Check whitelist status with detailed logging
							isWhitelisted := isNumberInWhitelist(sourceNumber, integration)
							log.Printf("[Signal:%s] Message from %s to owner, whitelist check result: %v", alias, sourceNumber, isWhitelisted)

							if isWhitelisted {
								// If the flag is enabled and number is whitelisted, process non-command messages as AI commands
								log.Printf("[Signal:%s] Processing non-command message as AI command from whitelisted number %s: %s", alias, sourceNumber, messageText)
								if err := s.processAICommand(messageText, sourceNumber, alias, integration, nil); err != nil {
									log.Printf("[Signal:%s] Error processing message as AI command: %v", alias, err)
								} else {
									log.Printf("[Signal:%s] Successfully processed message as AI command", alias)
								}
							} else {
								log.Printf("[Signal:%s] Not processing message as AI command because number %s is not whitelisted", alias, sourceNumber)
							}
						} else {
							if DONT_REQUIRE_AI_COMMAND_PREFIX {
								if !isToOwner {
									log.Printf("[Signal:%s] Message not directed to owner, skipping AI processing", alias)
								} else {
									log.Printf("[Signal:%s] Message directed to owner but number %s not whitelisted, skipping AI processing", alias, sourceNumber)
								}
							} else {
								log.Printf("[Signal:%s] AI command prefix required but not present, skipping AI processing", alias)
							}
						}

						// Send websocket notification if available
						// This would require access to the websocket handler
						// For now, we'll just log that we would send a notification
						log.Printf("[Signal:%s] Would send websocket notification for new message", alias)
					}

					// Update the last_used timestamp for the integration
					var integration database.Integration
					if err := s.DB.First(&integration, "integration_name = ?", alias).Error; err == nil {
						now := time.Now()
						integration.LastUsed = &now
						if err := s.DB.Save(&integration).Error; err != nil {
							log.Printf("[Signal:%s] Failed to update integration last_used: %v", alias, err)
						} else {
							log.Printf("[Signal:%s] Updated last_used timestamp", alias)
						}
					}

					return nil
				},
			}

			if err := s.AddTask(task); err != nil {
				log.Printf("Error adding Signal polling task: %v", err)
			} else {
				log.Printf("Added Signal polling task for integration %s", alias)
			}

		// Add cases for other integration types as needed
		default:
			log.Printf("Unknown integration type: %s", integration.IntegrationType)
		}
	}
}

// Add this function at an appropriate location in the file
func (s *SchedulerService) processSignalCommand(command string, message string, sourceNumber string, alias string, integration database.Integration) error {
	log.Printf("[Signal:%s] Processing command: %s from number: %s", alias, command, sourceNumber)

	switch command {
	case "ai":
		log.Printf("[Signal:%s] Recognized AI command, processing with message: %s", alias, message)
		return s.processAICommand(message, sourceNumber, alias, integration, nil)
	default:
		if DONT_REQUIRE_AI_COMMAND_PREFIX {
			// If the flag is enabled, treat unknown commands as AI commands
			fullMessage := "/" + command
			if message != "" {
				fullMessage += " " + message
			}
			log.Printf("[Signal:%s] Processing unknown command as AI command: %s from number: %s", alias, fullMessage, sourceNumber)
			return s.processAICommand(fullMessage, sourceNumber, alias, integration, nil)
		}
		log.Printf("[Signal:%s] Unknown command: %s from number: %s, not processing as AI", alias, command, sourceNumber)
		return fmt.Errorf("unknown command: %s", command)
	}
}

func (s *SchedulerService) processAICommand(message string, sourceNumber string, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	log.Printf("[Signal:%s] Processing AI command with message: %s from number: %s", alias, message, sourceNumber)
	log.Printf("[Signal:%s] Number of attachments received: %d", alias, len(attachments))
	if len(attachments) > 0 {
		log.Printf("[Signal:%s] Attachment details: %+v", alias, attachments)
	}

	// Parse the integration config to get the port and phone number
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		log.Printf("[Signal:%s] Failed to parse integration config: %v", alias, err)
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	port, ok := config["port"].(float64)
	if !ok {
		log.Printf("[Signal:%s] Invalid port in integration config", alias)
		return fmt.Errorf("invalid port in integration config")
	}

	phoneNumber, ok := config["phone_number"].(string)
	if !ok {
		log.Printf("[Signal:%s] Invalid phone_number in integration config", alias)
		return fmt.Errorf("invalid phone_number in integration config")
	}

	log.Printf("[Signal:%s] Using port: %v and phone number: %s", alias, port, phoneNumber)

	// Find the integration owner
	var integrationOwner database.User
	if err := s.DB.First(&integrationOwner, integration.UserID).Error; err != nil {
		return fmt.Errorf("failed to find integration owner: %w", err)
	}

	// Find the Signal user
	var signalUser database.User
	if err := s.DB.Where("name = ?", "signal").First(&signalUser).Error; err != nil {
		return fmt.Errorf("failed to find Signal user: %w", err)
	}

	// Find the chat for this Signal conversation
	var chatUUID string
	var chats []database.Chat
	if err := s.DB.Preload("SharedConfig").
		Where("(user1_id = ? AND user2_id = ?) OR (user1_id = ? AND user2_id = ?)",
			signalUser.ID, integrationOwner.ID, integrationOwner.ID, signalUser.ID).
		Find(&chats).Error; err != nil {
		return fmt.Errorf("failed to find chats: %w", err)
	}

	// Find the specific chat for this phone number and alias
	for _, chat := range chats {
		if chat.SharedConfig != nil {
			var configData map[string]interface{}
			if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
				configAlias, aliasOk := configData["signal_alias"].(string)
				configPhone, phoneOk := configData["signal_phone"].(string)

				if aliasOk && phoneOk && configAlias == alias && configPhone == sourceNumber {
					chatUUID = chat.UUID
					break
				}
			}
		}
	}

	if chatUUID == "" {
		return fmt.Errorf("could not find chat UUID for this Signal conversation")
	}

	// Generate a session token for the integration owner
	token := api.GenerateToken(integrationOwner.Email)
	expiry := time.Now().Add(24 * time.Hour)

	// Create a session in the database
	session := database.Session{
		Token:  token,
		Data:   []byte{},
		Expiry: expiry,
		UserId: integrationOwner.ID,
	}

	if err := s.DB.Create(&session).Error; err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Generate a session token for the Signal user
	signalUserToken := api.GenerateToken(signalUser.Email)
	signalUserExpiry := time.Now().Add(24 * time.Hour)

	// Create a session in the database for the Signal user
	signalUserSession := database.Session{
		Token:  signalUserToken,
		Data:   []byte{},
		Expiry: signalUserExpiry,
		UserId: signalUser.ID,
	}

	if err := s.DB.Create(&signalUserSession).Error; err != nil {
		return fmt.Errorf("failed to create session for Signal user: %w", err)
	}

	// Create a LocalInteractionClient with the server URL and session token
	interactionClient, err := msgmate.NewLocalInteractionClient(s.serverURL, token)
	if err != nil {
		log.Printf("[Signal:%s] Failed to create interaction client: %v", alias, err)
		return fmt.Errorf("failed to create interaction client: %w", err)
	}

	// Define the tools to use based on whether this is admin (source == integration phone)
	toolsList := []string{
		//"signal_send_message",
		"signal_read_past_messages",
		//"signal_show_typing_indicator",
		"get_current_time",
		"interaction_start:run_callback_function",
		"interaction_complete:run_callback_function",
	}

	// Check if the source number is the same as the integration's phone number
	// If so, add the whitelist management tools
	isAdmin := sourceNumber == phoneNumber
	if isAdmin {
		log.Printf("[Signal:%s] Source number matches integration number, enabling admin tools", alias)
		toolsList = append(toolsList,
			"signal_get_whitelist",
			"signal_add_to_whitelist",
			"signal_remove_from_whitelist")
	}

	// Configure the bot
	botConfig := map[string]interface{}{
		"temperature": 0.7,
		"max_tokens":  4096,
		"model":       "gpt-4o",
		"endpoint":    "https://api.openai.com/v1/",
		"backend":     "openai",
		"context":     10,
		"system_prompt": "You are a helpful assistant in a chat conversation.\n\n" +
			"GUIDELINES:\n" +
			"- Always respond in the same language the user wrote their message in.\n" +
			"- Be concise and direct in your communication.\n" +
			"- Provide helpful and clear information.\n" +
			"- Use appropriate formatting with paragraphs, bullet points, or lists when needed.\n" +
			"- Focus on providing valuable responses to user queries.\n\n" +
			"- If you are missing some context use the signal_read_past_messages tool to get the latest messages in the chat.\n\n" +
			"Note: The typing indicator and message sending are handled automatically by the system.",
		"tools": toolsList,
	}
	interactionClient.SetBotConfig(botConfig)

	// Create a common configuration for base tools
	commonToolConfig := map[string]interface{}{
		"recipient_phone":        sourceNumber, // Send response back to the source number
		"sender_phone":           phoneNumber,  // Send from the integration's phone number
		"api_host":               fmt.Sprintf("http://localhost:%d", int(port)),
		"chat_uuid":              chatUUID,
		"backend_host":           s.serverURL,
		"signal_user_session_id": signalUserToken,
		"signal_user_uuid":       signalUser.UUID,
	}

	startFuncId, err := msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
		func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
			log.Printf("[Signal:%s] ============================= TBS =============== TBS ================= Signal interaction start", alias)
			log.Printf("[Signal:%s] Init data: %+v", alias, initData)
			log.Printf("[Signal:%s] Input data: %+v", alias, inputData)
			log.Printf("[Signal:%s] ============================= TBS =============== TBS ================= Signal interaction start", alias)
			showTypingIndicatorTool := msgmate.GetNewToolInstanceByName("signal_show_typing_indicator", commonToolConfig)
			showTypingIndicatorTool.RunTool(msgmate.SignalShowTypingIndicatorToolInput{})
			return nil, nil
		})

	stopFuncId, err := msgmate.GetGlobalMsgmateHandler().QuickRegisterFunction(
		func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
			log.Printf("[Signal:%s] ============================= TBS =============== TBS ================= Signal interaction stop", alias)
			log.Printf("[Signal:%s] Init data: %+v", alias, initData)
			log.Printf("[Signal:%s] Input data: %+v", alias, inputData)
			log.Printf("[Signal:%s] ============================= TBS =============== TBS ================= Signal interaction stop", alias)
			sendSignalMessageTool := msgmate.GetNewToolInstanceByName("signal_send_message", commonToolConfig)
			sendSignalMessageTool.RunTool(msgmate.SignalSendMessageToolInput{
				Message: inputData["last_ai_message"].(string),
			})
			return nil, nil
		})

	if err != nil {
		log.Printf("[Signal:%s] Failed to register signal interaction start function: %v", alias, err)
		return fmt.Errorf("failed to register signal interaction start function: %w", err)
	}

	// Initialize tools
	toolInit := map[string]interface{}{
		"signal_send_message":                        commonToolConfig,
		"signal_read_past_messages":                  commonToolConfig,
		"signal_show_typing_indicator":               commonToolConfig,
		"interaction_start:run_callback_function":    map[string]interface{}{"callback_function_id": startFuncId},
		"interaction_complete:run_callback_function": map[string]interface{}{"callback_function_id": stopFuncId},
	}

	// If the source is the admin, initialize the whitelist management tools
	if isAdmin {
		// Create the whitelist tool config with additional admin parameters
		whitelistToolConfig := map[string]interface{}{}

		// Copy all fields from commonToolConfig
		for k, v := range commonToolConfig {
			whitelistToolConfig[k] = v
		}

		// Add the additional parameters required for whitelist management
		whitelistToolConfig["admin_user_session_id"] = token // Use the integration owner's token
		whitelistToolConfig["integration_alias"] = alias     // Set the integration alias

		// Initialize the whitelist tools
		toolInit["signal_get_whitelist"] = whitelistToolConfig
		toolInit["signal_add_to_whitelist"] = whitelistToolConfig
		toolInit["signal_remove_from_whitelist"] = whitelistToolConfig
	}

	// Process attachments if any
	var processedMessage string
	if len(attachments) > 0 {
		log.Printf("[Signal:%s] Processing %d attachments for AI interaction", alias, len(attachments))

		// Create content array with text and file references (similar to StartBot)
		contentArray := []map[string]interface{}{}

		// Create file mapping for LocalInteractionClient
		fileMapping := make(map[string]interface{})

		// Add text content if it exists
		if message != "" {
			contentArray = append(contentArray, map[string]interface{}{
				"type": "text",
				"text": message,
			})
		}

		// Add each file attachment
		for i, attachment := range attachments {
			log.Printf("[Signal:%s] Processing attachment %d: %+v", alias, i, attachment)

			fileID, ok := attachment["file_id"].(string)
			if !ok {
				log.Printf("[Signal:%s] Skipping attachment without file_id", alias)
				continue
			}

			// Check if backend is OpenAI and if openai_file_id is present
			backend := "openai" // Default to OpenAI for Signal integration
			openaiFileID, hasOpenAIID := attachment["openai_file_id"].(string)

			if backend == "openai" && hasOpenAIID && openaiFileID != "" {
				// Use existing OpenAI file ID
				log.Printf("[Signal:%s] Using existing OpenAI file ID: %s", alias, openaiFileID)
				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": openaiFileID,
				})
				// Store mapping: OpenAI file ID -> internal file ID
				fileMapping[openaiFileID] = fileID
				log.Printf("[Signal:%s] Stored file mapping: %s -> %s", alias, openaiFileID, fileID)
			} else if backend == "openai" {
				// Upload file to OpenAI if not already uploaded
				log.Printf("[Signal:%s] Uploading file %s to OpenAI", alias, fileID)

				// Create a client for file operations
				fileClient := client.NewClient(s.serverURL)
				fileClient.SetSessionId(token)

				// Get file info
				_, mimeType, err := retrieveFileData(fileClient, fileID)
				if err != nil {
					log.Printf("[Signal:%s] Failed to retrieve file data for %s: %v", alias, fileID, err)
					continue
				}

				// Upload to OpenAI
				openaiFileID, err = uploadFileToOpenAI(fileClient, fileID, mimeType)
				if err != nil {
					log.Printf("[Signal:%s] Failed to upload file %s to OpenAI: %v", alias, fileID, err)
					continue
				}

				log.Printf("[Signal:%s] Successfully uploaded file %s to OpenAI with ID: %s", alias, fileID, openaiFileID)

				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": openaiFileID,
				})
				// Store mapping: OpenAI file ID -> internal file ID
				fileMapping[openaiFileID] = fileID
				log.Printf("[Signal:%s] Stored file mapping: %s -> %s", alias, openaiFileID, fileID)
			} else {
				// For non-OpenAI backends, use file_id directly
				contentArray = append(contentArray, map[string]interface{}{
					"type":    "file",
					"file_id": fileID,
				})
				// Store mapping: file_id -> file_id (same for non-OpenAI backends)
				fileMapping[fileID] = fileID
				log.Printf("[Signal:%s] Stored file mapping: %s -> %s", alias, fileID, fileID)
			}
		}

		log.Printf("[Signal:%s] Final content array: %+v", alias, contentArray)
		log.Printf("[Signal:%s] File mapping: %+v", alias, fileMapping)

		// Store file mapping in toolInit for LocalInteractionClient
		toolInit["file_mapping"] = fileMapping

		// Convert content array to JSON string for the message
		contentJSON, err := json.Marshal(contentArray)
		if err != nil {
			log.Printf("[Signal:%s] Failed to marshal content array: %v", alias, err)
			processedMessage = message // Fallback to original message
		} else {
			processedMessage = string(contentJSON)
			log.Printf("[Signal:%s] Final processed message: %s", alias, processedMessage)
		}
	} else {
		processedMessage = message
	}

	// Create the interaction with the AI
	interaction, err := interactionClient.CreateInteraction(toolInit, processedMessage)
	if err != nil {
		log.Printf("[Signal:%s] Failed to create AI interaction: %v", alias, err)
		return fmt.Errorf("failed to create AI interaction: %w", err)
	}

	log.Printf("[Signal:%s] Created AI interaction with UUID: %s", alias, interaction["uuid"])

	// Add more logging before returning
	log.Printf("[Signal:%s] AI interaction setup complete for message from %s", alias, sourceNumber)
	return nil
}

// ProcessSignalMessage processes a Signal message with the AI
func (s *SchedulerService) ProcessSignalMessage(messageText, sourceNumber, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	log.Printf("[Signal:%s] ProcessSignalMessage called with %d attachments", alias, len(attachments))
	if len(attachments) > 0 {
		log.Printf("[Signal:%s] Attachment details: %+v", alias, attachments)
	}

	// This is a public wrapper for the private processAICommand method
	return s.processAICommand(messageText, sourceNumber, alias, integration, attachments)
}

// Helper functions for file operations

// retrieveFileData retrieves file data from the server
func retrieveFileData(ocClient *client.Client, fileID string) ([]byte, string, error) {
	// Get file data from the server using the new data endpoint
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/data", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return nil, "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ocClient.GetHost())
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("error response: %v", resp.Status)
	}

	var response struct {
		Data        string `json:"data"`
		ContentType string `json:"content_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, "", fmt.Errorf("error decoding response: %w", err)
	}

	// Decode base64 data back to bytes
	fileBytes, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return nil, "", fmt.Errorf("error decoding base64 data: %w", err)
	}

	return fileBytes, response.ContentType, nil
}

// uploadFileToOpenAI uploads a file to OpenAI's files API
func uploadFileToOpenAI(ocClient *client.Client, fileID string, mimeType string) (string, error) {
	// Get OpenAI API key
	openAIKey := ocClient.GetApiKey("openai")
	if openAIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	// First, get the file data from the server
	fileBytes, _, err := retrieveFileData(ocClient, fileID)
	if err != nil {
		return "", fmt.Errorf("error retrieving file data: %w", err)
	}

	// Get file info to get the filename
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/files/%s/info", ocClient.GetHost(), fileID), nil)
	if err != nil {
		return "", fmt.Errorf("error creating file info request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ocClient.GetHost())
	req.Header.Set("Cookie", fmt.Sprintf("session_id=%s", ocClient.GetSessionId()))

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending file info request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error response from file info: %v", resp.Status)
	}

	var fileInfo struct {
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return "", fmt.Errorf("error decoding file info response: %w", err)
	}

	// Create multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file
	part, err := writer.CreateFormFile("file", fileInfo.FileName)
	if err != nil {
		return "", fmt.Errorf("error creating form file: %w", err)
	}
	part.Write(fileBytes)

	// Add the purpose
	writer.WriteField("purpose", "assistants")

	writer.Close()

	// Create the request to OpenAI
	req, err = http.NewRequest("POST", "https://api.openai.com/v1/files", body)
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+openAIKey)

	// Send the request
	resp, err = httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("error response from OpenAI: %v - %s", resp.Status, string(bodyBytes))
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
