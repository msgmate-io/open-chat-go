package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
)

type ToggleActiveRequest struct {
	Active bool `json:"active"`
}

// ToggleActive activates or deactivates an integration by ID for the authenticated user.
//
//	@Summary      Toggle integration active status
//	@Description  Activate or deactivate an integration by its ID for the authenticated user
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        id path int true "Integration ID"
//	@Param        request body ToggleActiveRequest true "Toggle request"
//	@Success      200 {object} ListedIntegration "Updated integration details"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      404 {string} string "Integration not found"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/{id}/toggle-active [post]
func (h *IntegrationsHandler) ToggleActive(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Get integration ID from path
	integrationIDStr := r.PathValue("id")
	if integrationIDStr == "" {
		http.Error(w, "Integration ID is required", http.StatusBadRequest)
		return
	}

	integrationID, err := strconv.ParseUint(integrationIDStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid integration ID", http.StatusBadRequest)
		return
	}

	// Parse request body
	var req ToggleActiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Find the integration and ensure it belongs to the user
	var integration database.Integration
	q := DB.Where("id = ? AND user_id = ?", integrationID, user.ID).First(&integration)
	if q.Error != nil {
		if q.Error.Error() == "record not found" {
			http.Error(w, "Integration not found", http.StatusNotFound)
		} else {
			http.Error(w, q.Error.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Handle integration-specific actions before updating the database
	if err := h.handleIntegrationToggle(integration, req.Active); err != nil {
		log.Printf("Warning: Failed to handle integration-specific toggle actions: %v", err)
		// Continue with the database update even if Docker operations fail
	}

	// Update the active status
	integration.Active = req.Active

	// Save the changes
	if err := DB.Save(&integration).Error; err != nil {
		http.Error(w, "Failed to update integration", http.StatusInternalServerError)
		return
	}

	// Convert to listed integration format and return
	listedIntegration := convertIntegrationToListedIntegration(integration)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedIntegration)
}

// handleIntegrationToggle handles integration-specific actions when toggling active status
func (h *IntegrationsHandler) handleIntegrationToggle(integration database.Integration, active bool) error {
	switch integration.IntegrationType {
	case "signal":
		return h.handleSignalIntegrationToggle(integration, active)
	default:
		// For other integration types, no special handling needed
		return nil
	}
}

// handleSignalIntegrationToggle handles Signal integration Docker container start/stop
func (h *IntegrationsHandler) handleSignalIntegrationToggle(integration database.Integration, active bool) error {
	// Parse the integration config to get the alias
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, ok := config["alias"].(string)
	if !ok || alias == "" {
		return fmt.Errorf("invalid alias in integration config")
	}

	if active {
		// Start the Docker container
		log.Printf("Starting Signal Docker container for integration %s", alias)
		startCmd := exec.Command("docker", "start", alias)
		output, err := startCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to start Docker container %s: %v\nOutput: %s", alias, err, string(output))
		}
		log.Printf("Successfully started Signal Docker container %s", alias)
	} else {
		// Stop the Docker container
		log.Printf("Stopping Signal Docker container for integration %s", alias)
		stopCmd := exec.Command("docker", "stop", alias)
		output, err := stopCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to stop Docker container %s: %v\nOutput: %s", alias, err, string(output))
		}
		log.Printf("Successfully stopped Signal Docker container %s", alias)
	}

	return nil
}
