package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type MCPIntegrationUpsertRequest struct {
	Name     string                 `json:"name"`
	Config   map[string]interface{} `json:"config"`
	AuthData map[string]interface{} `json:"auth_data,omitempty"`
	Enabled  *bool                  `json:"enabled,omitempty"`
}

type MCPIntegrationResponseRow struct {
	Name          string                 `json:"name"`
	Config        map[string]interface{} `json:"config"`
	Enabled       bool                   `json:"enabled"`
	HasAuthData   bool                   `json:"has_auth_data"`
	CreatedAtUnix int64                  `json:"created_at_unix"`
	UpdatedAtUnix int64                  `json:"updated_at_unix"`
}

var mcpIntegrationNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,80}$`)

func normalizeMCPIntegrationName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func validateMCPIntegrationRequest(req MCPIntegrationUpsertRequest) (MCPIntegrationUpsertRequest, error) {
	req.Name = normalizeMCPIntegrationName(req.Name)
	if req.Name == "" {
		return req, fmt.Errorf("name is required")
	}
	if !mcpIntegrationNamePattern.MatchString(req.Name) {
		return req, fmt.Errorf("name must match ^[a-z0-9][a-z0-9_-]{1,80}$")
	}
	if req.Config == nil {
		return req, fmt.Errorf("config is required")
	}
	if _, err := msgmate.DiscoverMCPTools(req.Config, req.AuthData); err != nil {
		return req, fmt.Errorf("unable to connect to MCP server: %w", err)
	}
	return req, nil
}

func decodeMCPIntegrationRow(row database.MCPIntegrationConfig) MCPIntegrationResponseRow {
	config := map[string]interface{}{}
	_ = json.Unmarshal(row.Config, &config)
	hasAuthData := len(strings.TrimSpace(string(row.AuthData))) > 0 && strings.TrimSpace(string(row.AuthData)) != "{}"
	return MCPIntegrationResponseRow{
		Name:          row.Name,
		Config:        config,
		Enabled:       row.Enabled,
		HasAuthData:   hasAuthData,
		CreatedAtUnix: row.CreatedAt.Unix(),
		UpdatedAtUnix: row.UpdatedAt.Unix(),
	}
}

func (h *ToolsHandler) ListMCPIntegrations(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	rows := []database.MCPIntegrationConfig{}
	if err := DB.Where("owner_user_id = ?", user.ID).Order("name asc").Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list MCP integrations", http.StatusInternalServerError)
		return
	}
	items := make([]MCPIntegrationResponseRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, decodeMCPIntegrationRow(row))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"rows": items})
}

func (h *ToolsHandler) CreateMCPIntegration(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req MCPIntegrationUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req, err = validateMCPIntegrationRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	configJSON, _ := json.Marshal(req.Config)
	authJSON, _ := json.Marshal(req.AuthData)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := database.MCPIntegrationConfig{
		OwnerUserId: user.ID,
		Name:        req.Name,
		Config:      configJSON,
		AuthData:    authJSON,
		Enabled:     enabled,
	}
	if err := DB.Create(&row).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(w, "integration already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create MCP integration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "row": decodeMCPIntegrationRow(row)})
}

func (h *ToolsHandler) UpdateMCPIntegration(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := normalizeMCPIntegrationName(r.PathValue("integration_name"))
	if name == "" {
		http.Error(w, "integration_name is required", http.StatusBadRequest)
		return
	}
	var req MCPIntegrationUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = name
	}
	req, err = validateMCPIntegrationRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var existing database.MCPIntegrationConfig
	if err := DB.Where("owner_user_id = ? AND name = ?", user.ID, name).First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "integration not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to load MCP integration", http.StatusInternalServerError)
		return
	}
	configJSON, _ := json.Marshal(req.Config)
	authJSON, _ := json.Marshal(req.AuthData)
	updates := map[string]interface{}{
		"name":      req.Name,
		"config":    configJSON,
		"auth_data": authJSON,
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if err := DB.Model(&existing).Updates(updates).Error; err != nil {
		http.Error(w, "Failed to update MCP integration", http.StatusInternalServerError)
		return
	}
	if err := DB.Where("id = ?", existing.ID).First(&existing).Error; err != nil {
		http.Error(w, "Failed to load MCP integration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "row": decodeMCPIntegrationRow(existing)})
}

func (h *ToolsHandler) DeleteMCPIntegration(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := normalizeMCPIntegrationName(r.PathValue("integration_name"))
	if name == "" {
		http.Error(w, "integration_name is required", http.StatusBadRequest)
		return
	}
	if err := DB.Where("owner_user_id = ? AND name = ?", user.ID, name).Delete(&database.MCPIntegrationConfig{}).Error; err != nil {
		http.Error(w, "Failed to delete MCP integration", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (h *ToolsHandler) DiscoverMCPIntegration(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := normalizeMCPIntegrationName(r.PathValue("integration_name"))
	if name == "" {
		http.Error(w, "integration_name is required", http.StatusBadRequest)
		return
	}
	var row database.MCPIntegrationConfig
	if err := DB.Where("owner_user_id = ? AND name = ?", user.ID, name).First(&row).Error; err != nil {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}
	config := map[string]interface{}{}
	auth := map[string]interface{}{}
	_ = json.Unmarshal(row.Config, &config)
	_ = json.Unmarshal(row.AuthData, &auth)
	tools, err := msgmate.DiscoverMCPTools(config, auth)
	if err != nil {
		http.Error(w, fmt.Sprintf("Discovery failed: %v", err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "count": len(tools), "tools": tools})
}
