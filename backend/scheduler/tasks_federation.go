//go:build federation

package scheduler

import (
	"backend/api/federation"
	"backend/database"
	"log"

	"gorm.io/gorm"
)

// NetworkTasks returns tasks related to network operations
// This function is only available when building with federation support
func NetworkTasks(DB *gorm.DB, federationHandler *federation.FederationHandler) []Task {
	return []Task{
		{
			Name:        "network_sync",
			Description: "Synchronize network data",
			Schedule:    "*/30 * * * *", // Every 30 minutes
			Enabled:     true,
			Handler: func() error {
				var networks []database.Network
				if err := DB.Find(&networks).Error; err != nil {
					return err
				}

				for _, network := range networks {
					log.Printf("Syncing network: %s", network.NetworkName)
					federationHandler.StartNetworkSyncProcess(DB, network.NetworkName)
				}

				return nil
			},
		},
		{
			Name:        "peer_health_check",
			Description: "Check health of connected peers",
			Schedule:    "0 */2 * * *", // Every 2 hours
			Enabled:     true,
			Handler: func() error {
				// Implementation would depend on your federation handler
				log.Printf("Checking peer health")
				// Example implementation
				return nil
			},
		},
	}
}
