package integrations

import (
	"backend/database"
	"log"
)

// SignalTaskHandler handles Signal-specific scheduled tasks
type SignalTaskHandler struct {
	botService *SignalBotService
}

// NewSignalTaskHandler creates a new Signal task handler
func NewSignalTaskHandler(botService *SignalBotService) *SignalTaskHandler {
	return &SignalTaskHandler{
		botService: botService,
	}
}

// CreateSignalPollingTask creates a task for polling Signal messages
func (sth *SignalTaskHandler) CreateSignalPollingTask(integration database.Integration) (func() error, error) {
	// Use default configuration
	config := DefaultSignalBotConfig()

	// Return a task function that processes Signal messages
	return func() error {
		log.Printf("[SignalTask] Starting Signal polling task for integration: %s", integration.IntegrationName)

		if err := sth.botService.ProcessSignalMessages(integration, config); err != nil {
			log.Printf("[SignalTask] Error processing Signal messages for integration %s: %v", integration.IntegrationName, err)
			return err
		}

		log.Printf("[SignalTask] Completed Signal polling task for integration: %s", integration.IntegrationName)
		return nil
	}, nil
}

// ProcessSignalMessage processes a single Signal message (public interface)
func (sth *SignalTaskHandler) ProcessSignalMessage(messageText, sourceNumber, alias string, integration database.Integration, attachments []map[string]interface{}) error {
	config := DefaultSignalBotConfig()
	return sth.botService.processAICommand(messageText, sourceNumber, alias, integration, attachments, config)
}
