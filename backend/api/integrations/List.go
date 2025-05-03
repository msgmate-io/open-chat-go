package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type ListedIntegration struct {
	ID              uint                   `json:"id"`
	IntegrationName string                 `json:"integration_name"`
	IntegrationType string                 `json:"integration_type"`
	Active          bool                   `json:"active"`
	LastUsed        *time.Time             `json:"last_used,omitempty"`
	UserID          uint                   `json:"user_id"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	Config          map[string]interface{} `json:"config,omitempty"`
}

func convertIntegrationToListedIntegration(integration database.Integration) ListedIntegration {
	listedIntegration := ListedIntegration{
		ID:              integration.ID,
		IntegrationName: integration.IntegrationName,
		IntegrationType: integration.IntegrationType,
		Active:          integration.Active,
		LastUsed:        integration.LastUsed,
		UserID:          integration.UserID,
		CreatedAt:       integration.CreatedAt,
		UpdatedAt:       integration.UpdatedAt,
	}

	// Try to unmarshal the config if it exists
	if integration.Config != nil && len(integration.Config) > 0 {
		var config map[string]interface{}
		if err := json.Unmarshal(integration.Config, &config); err == nil {
			listedIntegration.Config = config
		}
	}

	return listedIntegration
}

// List returns a paginated list of integrations for the authenticated user.
//
//	@Summary      Get user integrations
//	@Description  Retrieve a paginated list of integrations for the authenticated user
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Param        page  query  int  false  "Page number"  default(1)
//	@Param        limit query  int  false  "Page size"     default(20)
//	@Param        integration_type query string false "Integration type to filter by"
//	@Param        active query bool false "Filter by active status"
//	@Success      200 {object} database.Pagination "Paginated list of integrations"
//	@Failure      400 {string} string "Unable to get database or user"
//	@Failure      500 {string} string "Internal server error"
//	@Router       /api/v1/integrations/list [get]
func (h *IntegrationsHandler) List(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	pagination := database.Pagination{Page: 1, Limit: 20}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// Build the base query
	query := DB.Where("user_id = ?", user.ID)

	// Handle integration_type filter
	if integrationType := r.URL.Query().Get("integration_type"); integrationType != "" {
		query = query.Where("integration_type = ?", integrationType)
	}

	// Handle active filter
	if activeParam := r.URL.Query().Get("active"); activeParam != "" {
		if active, err := strconv.ParseBool(activeParam); err == nil {
			query = query.Where("active = ?", active)
		}
	}

	var integrations []database.Integration
	q := query.Scopes(database.Paginate(&integrations, &pagination, DB)).
		Preload("User").
		Find(&integrations)

	if q.Error != nil {
		http.Error(w, q.Error.Error(), http.StatusInternalServerError)
		return
	}

	// Convert to listed integrations
	listedIntegrations := make([]ListedIntegration, len(integrations))
	for i, integration := range integrations {
		listedIntegrations[i] = convertIntegrationToListedIntegration(integration)
	}

	pagination.Rows = listedIntegrations

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagination)
}
