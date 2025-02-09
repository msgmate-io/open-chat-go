package integrations

import (
	"backend/database"
	"log"

	"gorm.io/gorm"
)

// CreateSignalPollingTask creates a scheduled task for polling a Signal integration
// This function is deprecated and no longer used with the new SignalBotService architecture
func CreateSignalPollingTask(
	DB *gorm.DB,
	integration database.Integration,
) error {
	log.Printf("CreateSignalPollingTask is deprecated - use SignalBotService instead")
	return nil
}
