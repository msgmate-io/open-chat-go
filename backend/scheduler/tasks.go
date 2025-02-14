package scheduler

import (
	"backend/database"
	"log"
	"time"

	"gorm.io/gorm"
)

// Task represents a scheduled task
type Task struct {
	Name        string
	Description string
	Schedule    string
	Enabled     bool
	Handler     func() error
}

// SystemMaintenanceTasks returns tasks related to system maintenance
func SystemMaintenanceTasks(DB *gorm.DB) []Task {
	return []Task{
		{
			Name:        "backup_database",
			Description: "Backup the database",
			Schedule:    "0 3 * * *", // 3 AM every day
			Enabled:     true,
			Handler: func() error {
				//timestamp := time.Now().Format("20060102-150405")
				//backupPath := fmt.Sprintf("/var/backups/openchat/db-%s.backup", timestamp)

				// Implementation depends on your database type
				// This is just an example for SQLite
				//cmd := exec.Command("cp", DB.Name(), backupPath)
				//return cmd.Run()
				return nil
			},
		},
	}
}

// DataMaintenanceTasks returns tasks related to data maintenance
func DataMaintenanceTasks(DB *gorm.DB) []Task {
	return []Task{
		{
			Name:        "prune_old_sessions",
			Description: "Remove expired sessions",
			Schedule:    "0 4 * * *", // 4 AM every day
			Enabled:     true,
			Handler: func() error {
				result := DB.Where("expiry < ?", time.Now()).Delete(&database.Session{})
				if result.Error != nil {
					return result.Error
				}
				log.Printf("Pruned %d expired sessions", result.RowsAffected)
				return nil
			},
		},
	}
}
