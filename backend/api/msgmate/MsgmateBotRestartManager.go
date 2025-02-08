package msgmate

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RestartManagerImpl implements the RestartManager interface
type RestartManagerImpl struct {
	maxRestartDelay    time.Duration
	baseRestartDelay   time.Duration
	maxRestartAttempts int
}

// NewRestartManager creates a new restart manager
func NewRestartManager() *RestartManagerImpl {
	return &RestartManagerImpl{
		maxRestartDelay:    30 * time.Second,
		baseRestartDelay:   5 * time.Second,
		maxRestartAttempts: 1000, // Prevent infinite restarts in case of persistent issues
	}
}

// StartWithRestart starts the bot with automatic restart capability
func (rm *RestartManagerImpl) StartWithRestart(ctx context.Context, bot BotInterface) error {
	go func() {
		restartCount := 0

		defer func() {
			if r := recover(); r != nil {
				log.Printf("Bot restart loop panicked: %v", r)
				rm.LogError(fmt.Errorf("panic: %v", r), restartCount, "unknown")
			}
		}()

		for restartCount < rm.maxRestartAttempts {
			select {
			case <-ctx.Done():
				log.Printf("Bot restart loop cancelled: %v", ctx.Err())
				return
			default:
				// Continue with bot restart logic
			}

			restartCount++
			log.Printf("Starting bot (attempt %d)...", restartCount)

			// Start the bot and capture any errors
			err := bot.Start(ctx)

			// Log the error to disk
			if err != nil {
				rm.LogError(err, restartCount, "unknown")
				log.Printf("Bot crashed (attempt %d): %v", restartCount, err)
			} else {
				log.Printf("Bot stopped normally (attempt %d)", restartCount)
				// If the bot stopped normally (no error), we might want to exit the restart loop
				// For now, we'll continue restarting to handle cases where the bot exits gracefully
				// but we want it to keep running
			}

			// Calculate restart delay with exponential backoff (capped at maxRestartDelay)
			restartDelay := time.Duration(restartCount) * rm.baseRestartDelay
			if restartDelay > rm.maxRestartDelay {
				restartDelay = rm.maxRestartDelay
			}

			log.Printf("Restarting bot in %v (attempt %d)", restartDelay, restartCount+1)

			// Use a timer with context cancellation for the restart delay
			timer := time.NewTimer(restartDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				log.Printf("Bot restart loop cancelled during delay: %v", ctx.Err())
				return
			case <-timer.C:
				// Continue to next iteration
			}
		}

		log.Printf("Bot restart loop stopped after %d attempts (max reached)", rm.maxRestartAttempts)
		rm.LogError(fmt.Errorf("max restart attempts reached (%d)", rm.maxRestartAttempts), restartCount, "unknown")
	}()

	return nil
}

// LogError logs an error to disk
func (rm *RestartManagerImpl) LogError(err error, attempt int, username string) {
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

// RestartManagerFactory creates a restart manager
func RestartManagerFactory() RestartManager {
	return NewRestartManager()
}
