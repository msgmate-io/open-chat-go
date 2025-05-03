package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Constants for Signal user
const (
	SignalUserName  = "signal"
	SignalUserEmail = "signal@system.local"
)

type InstallSignalRestRequest struct {
	Alias       string `json:"alias"`
	PhoneNumber string `json:"phone_number"`
	Port        int    `json:"port"`
	Mode        string `json:"mode"`
}

func (h *IntegrationsHandler) InstallSignalRest(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	var data InstallSignalRestRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if data.Alias == "" || data.PhoneNumber == "" || data.Port <= 0 || data.Mode == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Check if integration with this alias already exists
	var existingIntegration database.Integration
	result := DB.Where("integration_name = ? AND user_id = ?", data.Alias, user.ID).First(&existingIntegration)
	if result.Error == nil {
		// If the integration exists but is inactive, reactivate it
		if !existingIntegration.Active {
			return // Do nothing
		}
		// If it's active continue processing to restart it if required
	}

	// Check if there's any other active Signal integration with the same port
	var conflictingIntegration database.Integration
	result = DB.Where("integration_type = ? AND active = ? AND user_id = ?", "signal", true, user.ID).First(&conflictingIntegration)
	if result.Error == nil {
		// Need to check if the port conflicts
		var config map[string]interface{}
		if err := json.Unmarshal(conflictingIntegration.Config, &config); err == nil {
			if port, ok := config["port"].(float64); ok && int(port) == data.Port {
				http.Error(w, fmt.Sprintf("Another active Signal integration '%s' is already using port %d",
					conflictingIntegration.IntegrationName, data.Port), http.StatusBadRequest)
				return
			}
		}
	}

	// Check if Docker is available
	if err := checkDockerAvailability(); err != nil {
		http.Error(w, fmt.Sprintf("Docker is not available: %v", err), http.StatusBadRequest)
		return
	}

	// Create the directory structure if it doesn't exist
	integrationPath := filepath.Join("/var/lib/openchat/integrations/signal", data.Alias)
	if err := os.MkdirAll(integrationPath, 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create integration directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Run the Docker container
	// The Signal REST API runs on port 8080 internally, so we map the external port to 8080
	dockerCmd := fmt.Sprintf("docker run -d --name %s --restart=always -p %d:8080 -v /var/lib/openchat/integrations/signal/%s/:/home/.local/share/signal-cli/ -e MODE=json-rpc bbernhard/signal-cli-rest-api",
		data.Alias, data.Port, data.Alias)

	cmd := exec.Command("sudo", "sh", "-c", dockerCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to start Docker container: %v\nOutput: %s", err, output), http.StatusInternalServerError)
		return
	}

	// Check if Signal user exists, create if not
	var signalUser database.User
	result = DB.Where("name = ?", SignalUserName).First(&signalUser)
	if result.Error != nil {
		// Signal user doesn't exist, create it
		log.Printf("Creating Signal system user")
		// Generate a random password for the Signal user
		randomPassword := []byte(fmt.Sprintf("signal-%d", time.Now().UnixNano()))
		newSignalUser, err := database.RegisterUser(
			DB,
			SignalUserName,
			SignalUserEmail,
			randomPassword,
		)
		if err != nil {
			log.Printf("Warning: Failed to create Signal user: %v", err)
			// Continue anyway, as the integration is still usable without the Signal user
		} else {
			log.Printf("Created Signal user with ID: %d", newSignalUser.ID)
			signalUser = *newSignalUser

			// Create contact relationship between the installing user and Signal user
			contact := database.Contact{
				OwningUserId:  user.ID,
				ContactUserId: signalUser.ID,
			}

			if err := DB.Create(&contact).Error; err != nil {
				log.Printf("Warning: Failed to create contact relationship with Signal user: %v", err)
			} else {
				log.Printf("Created contact relationship between user %d and Signal user %d", user.ID, signalUser.ID)
			}
		}
	} else {
		log.Printf("Signal user already exists with ID: %d", signalUser.ID)

		// Check if contact relationship already exists
		var existingContact database.Contact
		contactResult := DB.Where("owning_user_id = ? AND contact_user_id = ?", user.ID, signalUser.ID).First(&existingContact)

		if contactResult.Error != nil {
			// Contact doesn't exist, create it
			contact := database.Contact{
				OwningUserId:  user.ID,
				ContactUserId: signalUser.ID,
			}

			if err := DB.Create(&contact).Error; err != nil {
				log.Printf("Warning: Failed to create contact relationship with Signal user: %v", err)
			} else {
				log.Printf("Created contact relationship between user %d and Signal user %d", user.ID, signalUser.ID)
			}
		} else {
			log.Printf("Contact relationship already exists between user %d and Signal user %d", user.ID, signalUser.ID)
		}
	}

	// Store the integration in the database
	configData := map[string]interface{}{
		"alias":        data.Alias,
		"phone_number": data.PhoneNumber,
		"port":         data.Port,
		"mode":         data.Mode,
		"whitelist":    []string{}, // Initialize with empty whitelist
	}

	configBytes, err := json.Marshal(configData)
	if err != nil {
		http.Error(w, "Failed to serialize configuration", http.StatusInternalServerError)
		return
	}

	integration := database.Integration{
		IntegrationName: data.Alias,
		IntegrationType: "signal",
		Active:          true,
		Config:          configBytes,
		UserID:          user.ID,
	}

	if err := DB.Create(&integration).Error; err != nil {
		http.Error(w, fmt.Sprintf("Failed to save integration: %v", err), http.StatusInternalServerError)
		return
	}

	// Create and add the polling task if scheduler service is available
	if h.SchedulerService != nil {
		err = CreateSignalPollingTask(DB, h.SchedulerService, integration)
		if err != nil {
			log.Printf("Warning: Failed to create Signal polling task: %v", err)
			// Continue anyway, as the integration is still usable without the polling task
		} else {
			log.Printf("Created Signal polling task for integration %s", data.Alias)
		}
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Signal REST API integration installed successfully",
		"integration": integration,
	})
}

// Helper function to check if Docker is available
func checkDockerAvailability() error {
	cmd := exec.Command("docker", "--version")
	if err := cmd.Run(); err != nil {
		return errors.New("Docker is not installed or not accessible")
	}
	return nil
}

type UninstallSignalRestRequest struct {
	Alias string `json:"alias"`
}

func (h *IntegrationsHandler) UninstallSignalRest(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	var data UninstallSignalRestRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if data.Alias == "" {
		http.Error(w, "Missing required alias field", http.StatusBadRequest)
		return
	}

	// Find the integration in the database
	var integration database.Integration
	result := DB.Where("integration_name = ? AND integration_type = ? AND user_id = ?",
		data.Alias, "signal", user.ID).First(&integration)

	if result.Error != nil {
		http.Error(w, fmt.Sprintf("Integration with alias '%s' not found", data.Alias), http.StatusNotFound)
		return
	}

	// Step 1: Stop and remove the Docker container - continue even if this fails
	log.Printf("Stopping and removing Docker container for %s", data.Alias)
	dockerStopCmd := fmt.Sprintf("docker stop %s", data.Alias)
	cmd := exec.Command("sudo", "sh", "-c", dockerStopCmd)
	_, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to stop Docker container: %v", err)
		// Continue with uninstallation
	}

	// Force remove the Docker container with -f flag
	dockerRmCmd := fmt.Sprintf("docker rm -f %s", data.Alias)
	cmd = exec.Command("sudo", "sh", "-c", dockerRmCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to remove Docker container: %v, output: %s", err, string(output))
		// Continue with uninstallation
	}

	// Also check if the container exists and try to remove it again if needed
	dockerPsCmd := fmt.Sprintf("docker ps -a --filter name=%s --format '{{.Names}}'", data.Alias)
	cmd = exec.Command("sudo", "sh", "-c", dockerPsCmd)
	output, err = cmd.CombinedOutput()
	if err == nil && strings.TrimSpace(string(output)) != "" {
		// Container still exists, try one more time with force
		log.Printf("Container still exists, trying force removal again")
		dockerRmCmd := fmt.Sprintf("docker rm -f %s", data.Alias)
		cmd = exec.Command("sudo", "sh", "-c", dockerRmCmd)
		cmd.CombinedOutput()
	}

	// Step 2: Archive files if they exist
	timestamp := time.Now().Format("20060102-150405")
	archiveDir := "/var/lib/openchat/archive/integrations"
	archivePath := filepath.Join(archiveDir, fmt.Sprintf("%s-%s-%s", timestamp, data.Alias, "signal"))

	// Create archive directory - don't return error if this fails
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		log.Printf("Failed to create archive directory: %v", err)
	} else {
		// Create timestamped archive folder - don't return error if this fails
		if err := os.MkdirAll(archivePath, 0755); err != nil {
			log.Printf("Failed to create archive path: %v", err)
		} else {
			// Try to archive files, but don't fail if there are none
			sourcePath := filepath.Join("/var/lib/openchat/integrations/signal", data.Alias)
			if _, err := os.Stat(sourcePath); err == nil {
				// Use a shell command that won't fail if no files exist
				mvCmd := fmt.Sprintf("if [ -d \"%s\" ] && [ \"$(ls -A %s 2>/dev/null)\" ]; then mv %s/* %s/ 2>/dev/null || true; fi",
					sourcePath, sourcePath, sourcePath, archivePath)
				cmd = exec.Command("sudo", "sh", "-c", mvCmd)
				cmd.CombinedOutput() // Ignore any errors

				// Try to remove the original directory, but don't fail if it doesn't work
				os.RemoveAll(sourcePath) // Ignore any errors
			}
		}
	}

	// Before deactivating the integration, remove any associated polling task
	if h.SchedulerService != nil {
		taskName := fmt.Sprintf("signal_poll_%s", data.Alias)
		if err := h.SchedulerService.RemoveTask(taskName); err != nil {
			log.Printf("Warning: Failed to remove Signal polling task: %v", err)
			// Continue anyway, as we still want to uninstall the integration
		} else {
			log.Printf("Removed Signal polling task for integration %s", data.Alias)
		}
	}

	// Step 3: Update the integration record in the database (mark as inactive)
	// This is the most important step and should happen regardless of previous failures
	log.Printf("Updating database record for %s", data.Alias)
	integration.Active = false
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, fmt.Sprintf("Failed to update integration status: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if there are any remaining active Signal integrations
	var activeSignalIntegrations int64
	DB.Model(&database.Integration{}).Where("integration_type = ? AND active = ?", "signal", true).Count(&activeSignalIntegrations)

	// If no active Signal integrations remain, consider removing the Signal user
	// For now, we'll just log this - actual removal might be risky if there are existing chats
	if activeSignalIntegrations == 0 {
		log.Printf("No active Signal integrations remain. The Signal user could be removed if needed.")
		// Note: We're not actually removing the user as it might be referenced by existing chats
		// If you want to implement actual removal, you would need to handle all references first
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      fmt.Sprintf("Signal REST API integration '%s' uninstalled successfully", data.Alias),
		"archive_path": archivePath,
	})
}
