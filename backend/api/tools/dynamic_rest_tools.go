package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

type DynamicRESTToolUpsertRequest struct {
	Name                            string                 `json:"name"`
	FunctionName                    string                 `json:"function_name"`
	Description                     string                 `json:"description"`
	AdminOnly                       bool                   `json:"admin_only"`
	RequiresConfirmation            bool                   `json:"requires_confirmation"`
	StopOnFirstConfirmableToolCall  bool                   `json:"stop_on_first_confirmable_tool_call"`
	ConfirmationBlockMessage        string                 `json:"confirmation_block_message"`
	Enabled                         *bool                  `json:"enabled,omitempty"`
	OpenAPISourceType               string                 `json:"openapi_source_type"`
	OpenAPISource                   string                 `json:"openapi_source"`
	OperationID                     string                 `json:"operation_id,omitempty"`
	HTTPMethod                      string                 `json:"http_method,omitempty"`
	Path                            string                 `json:"path,omitempty"`
	BaseURLSource                   string                 `json:"base_url_source,omitempty"`
	BaseURLInputName                string                 `json:"base_url_input_name,omitempty"`
	ParamBindings                   []map[string]interface{} `json:"param_bindings,omitempty"`
	SafetyPolicy                    map[string]interface{} `json:"safety_policy,omitempty"`
}

func (h *ToolsHandler) ListDynamicRESTTools(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var rows []database.DynamicRESTTool
	if err := DB.Where("owner_user_id = ?", user.ID).Order("name asc").Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list dynamic tools", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"rows": rows})
}

func buildDynamicRESTToolRowFromRequest(user *database.User, req DynamicRESTToolUpsertRequest) (database.DynamicRESTTool, error) {
	if user == nil {
		return database.DynamicRESTTool{}, fmt.Errorf("user is required")
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return database.DynamicRESTTool{}, fmt.Errorf("name is required")
	}

	bindJSON, err := json.Marshal(req.ParamBindings)
	if err != nil {
		return database.DynamicRESTTool{}, fmt.Errorf("invalid param_bindings")
	}
	safetyJSON, err := json.Marshal(req.SafetyPolicy)
	if err != nil {
		return database.DynamicRESTTool{}, fmt.Errorf("invalid safety_policy")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	return database.DynamicRESTTool{
		OwnerUserId:                     user.ID,
		Name:                            req.Name,
		FunctionName:                    req.FunctionName,
		Description:                     req.Description,
		AdminOnly:                       req.AdminOnly,
		RequiresConfirmation:            req.RequiresConfirmation,
		StopOnFirstConfirmableToolCall:  req.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:        req.ConfirmationBlockMessage,
		Enabled:                         enabled,
		OpenAPISourceType:               req.OpenAPISourceType,
		OpenAPISource:                   req.OpenAPISource,
		OperationID:                     req.OperationID,
		HTTPMethod:                      req.HTTPMethod,
		Path:                            req.Path,
		BaseURLSource:                   req.BaseURLSource,
		BaseURLInputName:                req.BaseURLInputName,
		ParamBindings:                   bindJSON,
		SafetyPolicy:                    safetyJSON,
	}, nil
}

func applyDynamicRESTToolRow(target *database.DynamicRESTTool, row database.DynamicRESTTool) {
	target.OwnerUserId = row.OwnerUserId
	target.Name = row.Name
	target.FunctionName = row.FunctionName
	target.Description = row.Description
	target.AdminOnly = row.AdminOnly
	target.RequiresConfirmation = row.RequiresConfirmation
	target.StopOnFirstConfirmableToolCall = row.StopOnFirstConfirmableToolCall
	target.ConfirmationBlockMessage = row.ConfirmationBlockMessage
	target.Enabled = row.Enabled
	target.OpenAPISourceType = row.OpenAPISourceType
	target.OpenAPISource = row.OpenAPISource
	target.OperationID = row.OperationID
	target.HTTPMethod = row.HTTPMethod
	target.Path = row.Path
	target.BaseURLSource = row.BaseURLSource
	target.BaseURLInputName = row.BaseURLInputName
	target.ParamBindings = row.ParamBindings
	target.SafetyPolicy = row.SafetyPolicy
}

func (h *ToolsHandler) CreateDynamicRESTTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req DynamicRESTToolUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	row, err := buildDynamicRESTToolRowFromRequest(user, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, err := msgmate.BuildDynamicRESTToolDefinition(row); err != nil {
		http.Error(w, fmt.Sprintf("invalid dynamic rest tool definition: %v", err), http.StatusBadRequest)
		return
	}

	var existing database.DynamicRESTTool
	lookupErr := DB.Unscoped().
		Where("owner_user_id = ? AND name = ?", user.ID, row.Name).
		First(&existing).Error
	if lookupErr == nil {
		if existing.DeletedAt.Valid {
			applyDynamicRESTToolRow(&existing, row)
			existing.DeletedAt = gorm.DeletedAt{}
			if err := DB.Unscoped().Save(&existing).Error; err != nil {
				http.Error(w, "failed to restore dynamic rest tool", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": existing.Name})
			return
		}
		http.Error(w, "tool already exists", http.StatusConflict)
		return
	}
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		http.Error(w, "failed to check existing dynamic rest tool", http.StatusInternalServerError)
		return
	}

	if err := DB.Create(&row).Error; err != nil {
		http.Error(w, "failed to create dynamic rest tool", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": row.Name})
}

func (h *ToolsHandler) UpdateDynamicRESTTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	toolName := strings.TrimSpace(r.PathValue("tool_name"))
	if toolName == "" {
		http.Error(w, "tool_name is required", http.StatusBadRequest)
		return
	}

	var req DynamicRESTToolUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = toolName
	}

	row, err := buildDynamicRESTToolRowFromRequest(user, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, err := msgmate.BuildDynamicRESTToolDefinition(row); err != nil {
		http.Error(w, fmt.Sprintf("invalid dynamic rest tool definition: %v", err), http.StatusBadRequest)
		return
	}

	var existing database.DynamicRESTTool
	if err := DB.Where("owner_user_id = ? AND name = ?", user.ID, toolName).First(&existing).Error; err != nil {
		http.Error(w, "tool not found", http.StatusNotFound)
		return
	}

	existing.OwnerUserId = user.ID
	existing.Name = row.Name
	existing.FunctionName = row.FunctionName
	existing.Description = row.Description
	existing.AdminOnly = row.AdminOnly
	existing.RequiresConfirmation = row.RequiresConfirmation
	existing.StopOnFirstConfirmableToolCall = row.StopOnFirstConfirmableToolCall
	existing.ConfirmationBlockMessage = row.ConfirmationBlockMessage
	existing.Enabled = row.Enabled
	existing.OpenAPISourceType = row.OpenAPISourceType
	existing.OpenAPISource = row.OpenAPISource
	existing.OperationID = row.OperationID
	existing.HTTPMethod = row.HTTPMethod
	existing.Path = row.Path
	existing.BaseURLSource = row.BaseURLSource
	existing.BaseURLInputName = row.BaseURLInputName
	existing.ParamBindings = row.ParamBindings
	existing.SafetyPolicy = row.SafetyPolicy
	if err := DB.Save(&existing).Error; err != nil {
		http.Error(w, "failed to update dynamic rest tool", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": existing.Name})
}

func (h *ToolsHandler) DeleteDynamicRESTTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	toolName := strings.TrimSpace(r.PathValue("tool_name"))
	if toolName == "" {
		http.Error(w, "tool_name is required", http.StatusBadRequest)
		return
	}
	if err := DB.Unscoped().Where("owner_user_id = ? AND name = ?", user.ID, toolName).Delete(&database.DynamicRESTTool{}).Error; err != nil {
		http.Error(w, "failed to delete dynamic rest tool", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
