package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// CreateIntegrationRequest represents the request for creating a new integration
type CreateIntegrationRequest struct {
	IntegrationType string                 `json:"integration_type"`
	IntegrationName string                 `json:"integration_name"`
	Config          map[string]interface{} `json:"config"`
}

// CreateIntegrationResponse represents the response for creating a new integration
type CreateIntegrationResponse struct {
	Message     string               `json:"message"`
	Integration database.Integration `json:"integration"`
}

// Create handles the creation of new integrations
// POST /api/v1/integrations/create
func (h *IntegrationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	var data CreateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if data.IntegrationType == "" {
		http.Error(w, "Integration type is required", http.StatusBadRequest)
		return
	}

	if data.IntegrationName == "" {
		http.Error(w, "Integration name is required", http.StatusBadRequest)
		return
	}

	if data.Config == nil {
		http.Error(w, "Configuration is required", http.StatusBadRequest)
		return
	}

	// Check if integration with this name already exists for this user
	var existingIntegration database.Integration
	result := DB.Where("integration_name = ? AND user_id = ?", data.IntegrationName, user.ID).First(&existingIntegration)
	if result.Error == nil {
		http.Error(w, fmt.Sprintf("Integration with name '%s' already exists", data.IntegrationName), http.StatusConflict)
		return
	}

	// Handle different integration types
	switch data.IntegrationType {
	case "signal":
		err = h.createSignalIntegration(DB, *user, data)
	default:
		http.Error(w, fmt.Sprintf("Unsupported integration type: %s", data.IntegrationType), http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create integration: %v", err), http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(CreateIntegrationResponse{
		Message: fmt.Sprintf("%s integration created successfully", data.IntegrationType),
	})
}

// createSignalIntegration creates a Signal integration using the existing InstallSignalRest logic
func (h *IntegrationsHandler) createSignalIntegration(DB *gorm.DB, user database.User, data CreateIntegrationRequest) error {
	// Extract Signal-specific configuration
	alias, ok := data.Config["alias"].(string)
	if !ok || alias == "" {
		return fmt.Errorf("alias is required for Signal integration")
	}

	phoneNumber, ok := data.Config["phone_number"].(string)
	if !ok || phoneNumber == "" {
		return fmt.Errorf("phone_number is required for Signal integration")
	}

	port, ok := data.Config["port"].(float64)
	if !ok || int(port) <= 0 {
		return fmt.Errorf("valid port is required for Signal integration")
	}

	mode, ok := data.Config["mode"].(string)
	if !ok || mode == "" {
		mode = "json-rpc" // Default mode
	}

	// Check if there's any other active Signal integration with the same port
	var conflictingIntegration database.Integration
	result := DB.Where("integration_type = ? AND active = ? AND user_id = ?", "signal", true, user.ID).First(&conflictingIntegration)
	if result.Error == nil {
		// Need to check if the port conflicts
		var config map[string]interface{}
		if err := json.Unmarshal(conflictingIntegration.Config, &config); err == nil {
			if existingPort, ok := config["port"].(float64); ok && int(existingPort) == int(port) {
				return fmt.Errorf("another active Signal integration '%s' is already using port %d",
					conflictingIntegration.IntegrationName, int(port))
			}
		}
	}

	// Check if Docker is available
	if err := checkDockerAvailability(); err != nil {
		return fmt.Errorf("Docker is not available: %v", err)
	}

	// Create the directory structure if it doesn't exist
	integrationPath := filepath.Join("/var/lib/openchat/integrations/signal", alias)
	if err := os.MkdirAll(integrationPath, 0755); err != nil {
		return fmt.Errorf("failed to create integration directory: %v", err)
	}

	// Run the Docker container
	// The Signal REST API runs on port 8080 internally, so we map the external port to 8080
	dockerCmd := fmt.Sprintf("docker run -d --name %s --restart=always -p %d:8080 -v /var/lib/openchat/integrations/signal/%s/:/home/.local/share/signal-cli/ -e MODE=json-rpc bbernhard/signal-cli-rest-api",
		alias, int(port), alias)

	cmd := exec.Command("sudo", "sh", "-c", dockerCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start Docker container: %v\nOutput: %s", err, output)
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
		"alias":        alias,
		"phone_number": phoneNumber,
		"port":         int(port),
		"mode":         mode,
		"whitelist":    []string{}, // Initialize with empty whitelist
	}

	configBytes, err := json.Marshal(configData)
	if err != nil {
		return fmt.Errorf("failed to serialize configuration: %v", err)
	}

	integration := database.Integration{
		IntegrationName: data.IntegrationName,
		IntegrationType: "signal",
		Active:          true,
		Config:          configBytes,
		UserID:          user.ID,
	}

	if err := DB.Create(&integration).Error; err != nil {
		return fmt.Errorf("failed to save integration: %v", err)
	}

	// Create and add the polling task if scheduler service is available
	if h.SchedulerService != nil {
		err = CreateSignalPollingTask(DB, h.SchedulerService, integration)
		if err != nil {
			log.Printf("Warning: Failed to create Signal polling task: %v", err)
			// Continue anyway, as the integration is still usable without the polling task
		} else {
			log.Printf("Created Signal polling task for integration %s", alias)
		}
	}

	return nil
}
