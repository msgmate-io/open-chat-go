package msgmate

import (
	"backend/api/chats"
	"backend/api/contacts"
	"backend/client"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// DefaultBotConfig represents the default configuration for a bot
type DefaultBotConfig struct {
	Title         string                 `json:"title"`
	Description   string                 `json:"description"`
	Configuration map[string]interface{} `json:"configuration"`
}

// LocalInteractionClient provides a way to create local interactions with bots
type LocalInteractionClient struct {
	client    *client.Client
	botConfig map[string]interface{}
}

// NewLocalInteractionClient creates a new LocalInteractionClient
func NewLocalInteractionClient(host, sessionId string) (*LocalInteractionClient, error) {
	ocClient := client.NewClient(host)
	ocClient.SetSessionId(sessionId)

	// Default bot configuration similar to the Python client
	defaultConfig := map[string]interface{}{
		"temperature":   0.7,
		"max_tokens":    4096,
		"model":         "o3-mini-2025-01-31",
		"endpoint":      "https://api.openai.com/v1/",
		"backend":       "openai",
		"context":       10,
		"system_prompt": "You are a helpful assistant.",
	}

	return &LocalInteractionClient{
		client:    ocClient,
		botConfig: defaultConfig,
	}, nil
}

// SetBotConfig allows customizing the bot configuration
func (l *LocalInteractionClient) SetBotConfig(config map[string]interface{}) {
	l.botConfig = config
}

// RetrieveDefaultBot finds the default bot in the contacts list
func (l *LocalInteractionClient) RetrieveDefaultBot() (*contacts.ListedContact, error) {
	err, contactsList := l.client.ListContacts(1, 100) // Reasonable limit to find the bot
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve contacts: %w", err)
	}

	// Look for a contact with name "bot" in the contacts
	for _, contact := range contactsList.Rows {
		// The contact is already a ListedContact with Name field
		if contact.Name == "bot" {
			// Return the contact directly since it's already a ListedContact
			return &contacts.ListedContact{
				ContactToken: contact.ContactToken,
				Name:         contact.Name,
				UserUUID:     contact.UserUUID,
				IsOnline:     contact.IsOnline,
				ProfileData:  contact.ProfileData,
			}, nil
		}
	}

	return nil, fmt.Errorf("no default bot found in contacts")
}

// CreateInteraction creates a new interaction with the default bot
func (l *LocalInteractionClient) CreateInteraction(toolInit map[string]interface{}, firstMessage string) (map[string]interface{}, error) {
	log.Printf("=== LocalInteractionClient.CreateInteraction START ===")
	log.Printf("First message: %s", firstMessage)
	log.Printf("ToolInit: %+v", toolInit)

	// Find the default bot
	defaultBot, err := l.RetrieveDefaultBot()
	if err != nil {
		log.Printf("Failed to retrieve default bot: %v", err)
		return nil, err
	}
	log.Printf("Found default bot: %+v", defaultBot)

	// Debug: Print the botConfig and toolInit to identify problematic fields
	log.Printf("botConfig keys: %v", getMapKeys(l.botConfig))
	log.Printf("toolInit keys: %v", getMapKeys(toolInit))

	// Prepare the shared config with bot config and tool init
	sharedConfig := map[string]interface{}{}
	for k, v := range l.botConfig {
		// Skip function values or other non-serializable types
		if isNonSerializable(v) {
			log.Printf("Skipping non-serializable value in botConfig for key: %s (type: %T)", k, v)
			continue
		}
		sharedConfig[k] = v
	}

	// Clean the toolInit map to remove any non-serializable values
	cleanedToolInit := cleanMap(toolInit)
	sharedConfig["tool_init"] = cleanedToolInit
	log.Printf("Cleaned toolInit: %+v", cleanedToolInit)

	// Convert to JSON
	configJSON, err := json.Marshal(sharedConfig)
	if err != nil {
		log.Printf("Failed to marshal shared config: %v", err)
		return nil, fmt.Errorf("failed to marshal shared config: %w", err)
	}

	// Check if firstMessage is a JSON content array (for attachments)
	var firstMessageAttachments *[]chats.FileAttachment
	firstMessageToSend := firstMessage

	// Try to parse as JSON content array
	var contentArray []map[string]interface{}
	if err := json.Unmarshal([]byte(firstMessage), &contentArray); err == nil && len(contentArray) > 0 {
		log.Printf("Detected JSON content array with %d items", len(contentArray))

		// Check if this looks like a content array (has type field)
		if _, hasType := contentArray[0]["type"]; hasType {
			log.Printf("Confirmed content array format, processing attachments")

			// Extract text content
			var textParts []string
			var attachments []chats.FileAttachment

			for i, item := range contentArray {
				log.Printf("Processing content item %d: %+v", i, item)

				if itemType, ok := item["type"].(string); ok {
					switch itemType {
					case "text":
						if text, ok := item["text"].(string); ok {
							textParts = append(textParts, text)
							log.Printf("Added text content: %s", text)
						}
					case "file":
						// For Signal integration, the file_id in content array is the OpenAI file ID
						// But we need to find the original internal file ID for chat creation
						if openaiFileID, ok := item["file_id"].(string); ok {
							log.Printf("Found OpenAI file ID in content: %s", openaiFileID)

							// Try to find the original internal file ID from the toolInit
							// The scheduler should have stored the mapping in toolInit
							if toolInit != nil {
								if fileMapping, ok := toolInit["file_mapping"].(map[string]interface{}); ok {
									log.Printf("Found file_mapping: %+v", fileMapping)
									if internalFileID, ok := fileMapping[openaiFileID].(string); ok {
										log.Printf("Found internal file ID mapping: %s -> %s", openaiFileID, internalFileID)
										attachment := chats.FileAttachment{
											FileID: internalFileID,
										}
										attachments = append(attachments, attachment)
										log.Printf("Added file attachment with internal ID: %s", internalFileID)
									} else {
										log.Printf("No internal file ID mapping found for OpenAI file ID: %s", openaiFileID)
										log.Printf("Available mappings: %+v", fileMapping)
									}
								} else {
									log.Printf("No file_mapping found in toolInit")
									log.Printf("ToolInit keys: %v", getMapKeys(toolInit))
								}
							} else {
								log.Printf("toolInit is nil, cannot look up file mapping")
							}
						}
					}
				}
			}

			// Combine text parts
			firstMessageToSend = strings.Join(textParts, " ")
			log.Printf("Combined text content: %s", firstMessageToSend)

			// Set attachments if any
			if len(attachments) > 0 {
				firstMessageAttachments = &attachments
				log.Printf("Set %d attachments for separate message: %+v", len(attachments), attachments)
			} else {
				log.Printf("No attachments found in content array")
			}
		} else {
			log.Printf("JSON array doesn't have type field, treating as regular message")
		}
	} else {
		log.Printf("First message is not a JSON content array: %s", firstMessage)
	}

	// Create the chat with the first message (and attachments if any)
	var attachments []chats.FileAttachment
	if firstMessageAttachments != nil && len(*firstMessageAttachments) > 0 {
		attachments = *firstMessageAttachments
		log.Printf("Including %d attachments in chat creation: %+v", len(attachments), attachments)
	} else {
		log.Printf("No attachments to include in chat creation")
	}

	log.Printf("About to call CreateChatWithAttachments with:")
	log.Printf("  ContactToken: %s", defaultBot.ContactToken)
	log.Printf("  FirstMessage: %s", firstMessageToSend)
	log.Printf("  Attachments: %+v", attachments)
	log.Printf("  ChatType: interaction")

	err, chat := l.client.CreateChatWithAttachments(
		defaultBot.ContactToken,
		firstMessageToSend,
		attachments,
		configJSON,
		"interaction",
	)
	if err != nil {
		log.Printf("Failed to create chat: %v", err)
		return nil, fmt.Errorf("failed to create chat: %w", err)
	}

	log.Printf("Successfully created chat with UUID: %s", chat.UUID)

	// Return the chat response as the interaction
	chatResponse := map[string]interface{}{
		"uuid":      chat.UUID,
		"config":    sharedConfig,
		"timestamp": time.Now().String(),
		"partner":   chat.Partner,
	}

	log.Printf("=== LocalInteractionClient.CreateInteraction END ===")
	return chatResponse, nil
}

// Helper functions to identify and clean non-serializable values
func isNonSerializable(v interface{}) bool {
	if v == nil {
		return false
	}

	switch v.(type) {
	case func(), func(string) string, func() time.Time:
		return true
	case *gorm.DB, io.Reader, io.Writer, http.ResponseWriter:
		return true
	}

	// Check if it's a channel, function, or complex type
	vType := reflect.TypeOf(v)
	kind := vType.Kind()
	return kind == reflect.Func || kind == reflect.Chan || kind == reflect.UnsafePointer
}

func cleanMap(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		if nestedMap, ok := v.(map[string]interface{}); ok {
			// Recursively clean nested maps
			result[k] = cleanMap(nestedMap)
		} else if !isNonSerializable(v) {
			result[k] = v
		} else {
			log.Printf("Skipping non-serializable value in map for key: %s (type: %T)", k, v)
		}
	}
	return result
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// SendChatMessage sends a message to an existing chat
func (l *LocalInteractionClient) SendChatMessage(chatUUID, text string) error {
	message := client.SendMessage{
		Text: text,
	}

	return l.client.SendChatMessage(chatUUID, message)
}

// GetSessionID returns the current session ID
func (l *LocalInteractionClient) GetSessionID() string {
	return l.client.GetSessionId()
}

// GetClient returns the underlying client
func (l *LocalInteractionClient) GetClient() *client.Client {
	return l.client
}
