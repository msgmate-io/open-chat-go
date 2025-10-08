package msgmate

import (
	wsapi "backend/api/websocket"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StartBotWithRestart starts the bot with automatic restart capability and error logging
func StartBotWithRestart(host string, ch *wsapi.WebSocketHandler, username string, password string) {
	StartBotWithRestartContext(context.Background(), host, ch, username, password)
}

// StartBotWithRestartContext starts the bot with automatic restart capability, error logging, and context cancellation
func StartBotWithRestartContext(ctx context.Context, host string, ch *wsapi.WebSocketHandler, username string, password string) {
	// Create restart manager
	restartManager := RestartManagerFactory()

	// Create bot
	bot, err := NewMsgmateBot(host, username, password, ch)
	if err != nil {
		log.Printf("Failed to create bot: %v", err)
		return
	}

	// Start with restart capability
	restartManager.StartWithRestart(ctx, bot)
}

// logErrorToDisk writes bot errors to a log file
func logErrorToDisk(err error, attempt int, username string) {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Printf("Failed to create logs directory: %v", err)
		return
	}

	// Create log file with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFileName := filepath.Join(logsDir, fmt.Sprintf("bot_errors_%s.log", timestamp))

	// Open log file in append mode
	logFile, err := os.OpenFile(logFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	defer logFile.Close()

	// Get current time for logging
	now := time.Now()

	// Write detailed error entry
	errorEntry := fmt.Sprintf("[%s] Bot crash (attempt %d) for user '%s':\n",
		now.Format("2006-01-02 15:04:05"),
		attempt,
		username)

	errorEntry += fmt.Sprintf("  Error: %v\n", err)
	errorEntry += fmt.Sprintf("  Timestamp: %s\n", now.Format(time.RFC3339))
	errorEntry += fmt.Sprintf("  Attempt: %d\n", attempt)
	errorEntry += fmt.Sprintf("  User: %s\n", username)

	// Add separator for readability
	errorEntry += "  " + strings.Repeat("-", 50) + "\n"

	if _, writeErr := logFile.WriteString(errorEntry); writeErr != nil {
		log.Printf("Failed to write to log file: %v", writeErr)
	}
}

func StartBot(host string, ch *wsapi.WebSocketHandler, username string, password string) error {
	bot, err := NewMsgmateBot(host, username, password, ch)
	if err != nil {
		return err
	}

	ctx := context.Background()
	return bot.Start(ctx)
}

// mapGetOrDefault is a generic utility function to get a value from a map with a default
func mapGetOrDefault[T any](m map[string]interface{}, key string, defaultValue T) T {
	if m == nil {
		return defaultValue
	}

	if val, exists := m[key]; exists {
		// Try direct type conversion first
		if converted, ok := val.(T); ok {
			return converted
		}
		// Special handling for slices/arrays
		switch any(defaultValue).(type) {
		case []string:
			if slice, ok := val.([]interface{}); ok {
				result := make([]string, len(slice))
				for i, item := range slice {
					if str, ok := item.(string); ok {
						result[i] = str
					}
				}
				return any(result).(T)
			}
		}
	}

	return defaultValue
}

// CreateOrUpdateBotProfile creates or updates the bot profile in the database
// This function is now implemented in MsgmateBotProfiles.go
