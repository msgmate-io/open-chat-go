package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
)

// Get returns a single integration by ID for the authenticated user.
//
//	@Summary      Get integration by ID
//	@Description  Retrieve a single integration by its ID for the authenticated user
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        id path int true "Integration ID"
//	@Success      200 {object} ListedIntegration "Integration details"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      404 {string} string "Integration not found"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/{id} [get]
func (h *IntegrationsHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	var integration database.Integration
	q := DB.Where("id = ? AND user_id = ?", integrationID, user.ID).
		Preload("User").
		First(&integration)

	if q.Error != nil {
		if q.Error.Error() == "record not found" {
			http.Error(w, "Integration not found", http.StatusNotFound)
		} else {
			http.Error(w, q.Error.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Convert to listed integration format
	listedIntegration := convertIntegrationToListedIntegration(integration)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listedIntegration)
}
