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

// Delete handles the deletion of an integration by ID for the authenticated user.
// DELETE /api/v1/integrations/{id}
func (h *IntegrationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	// Handle integration-specific cleanup before deleting from database
	if err := h.handleIntegrationDeletion(integration); err != nil {
		log.Printf("Warning: Failed to handle integration-specific deletion actions: %v", err)
		// Continue with the database deletion even if cleanup operations fail
	}

	// Delete the integration from the database
	if err := DB.Delete(&integration).Error; err != nil {
		http.Error(w, "Failed to delete integration", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": fmt.Sprintf("Integration '%s' deleted successfully", integration.IntegrationName),
	})
}

// handleIntegrationDeletion handles integration-specific cleanup actions
func (h *IntegrationsHandler) handleIntegrationDeletion(integration database.Integration) error {
	switch integration.IntegrationType {
	case "signal":
		return h.handleSignalIntegrationDeletion(integration)
	default:
		// For other integration types, no special cleanup needed
		return nil
	}
}

// handleSignalIntegrationDeletion handles Signal integration cleanup including Docker container removal
func (h *IntegrationsHandler) handleSignalIntegrationDeletion(integration database.Integration) error {
	// Parse the integration config to get the alias
	var config map[string]interface{}
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return fmt.Errorf("failed to parse integration config: %w", err)
	}

	alias, ok := config["alias"].(string)
	if !ok || alias == "" {
		return fmt.Errorf("invalid alias in integration config")
	}

	log.Printf("Cleaning up Signal integration %s", alias)

	// Step 1: Stop the Docker container if it's running
	log.Printf("Stopping Docker container for %s", alias)
	stopCmd := exec.Command("docker", "stop", alias)
	_, err := stopCmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to stop Docker container %s (may not be running): %v", alias, err)
		// Continue with removal even if stop fails
	}

	// Step 2: Remove the Docker container
	log.Printf("Removing Docker container for %s", alias)
	rmCmd := exec.Command("docker", "rm", "-f", alias)
	output, err := rmCmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to remove Docker container %s: %v\nOutput: %s", alias, err, string(output))
		// Continue with cleanup even if container removal fails
	} else {
		log.Printf("Successfully removed Docker container %s", alias)
	}

	// Step 3: Remove the integration directory
	integrationPath := fmt.Sprintf("/var/lib/openchat/integrations/signal/%s", alias)
	log.Printf("Removing integration directory %s", integrationPath)
	rmDirCmd := exec.Command("sudo", "rm", "-rf", integrationPath)
	output, err = rmDirCmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to remove integration directory %s: %v\nOutput: %s", integrationPath, err, string(output))
		// Continue even if directory removal fails
	} else {
		log.Printf("Successfully removed integration directory %s", integrationPath)
	}

	// Step 4: Scheduler tasks are now managed by SignalBotService
	log.Printf("Signal integration deleted - scheduler tasks managed by SignalBotService")

	log.Printf("Successfully cleaned up Signal integration %s", alias)
	return nil
}
