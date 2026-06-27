package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	ParamBindings                   []map[string]interface{} `json:"param_bindings,omitempty"`
	SafetyPolicy                    map[string]interface{} `json:"safety_policy,omitempty"`
}

func (h *ToolsHandler) ListDynamicRESTTools(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil || !user.IsAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var rows []database.DynamicRESTTool
	if err := DB.Order("name asc").Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list dynamic tools", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"rows": rows})
}

func (h *ToolsHandler) UpsertDynamicRESTTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil || !user.IsAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	nameFromPath := strings.TrimSpace(r.PathValue("tool_name"))
	var req DynamicRESTToolUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = nameFromPath
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	bindJSON, err := json.Marshal(req.ParamBindings)
	if err != nil {
		http.Error(w, "invalid param_bindings", http.StatusBadRequest)
		return
	}
	safetyJSON, err := json.Marshal(req.SafetyPolicy)
	if err != nil {
		http.Error(w, "invalid safety_policy", http.StatusBadRequest)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row := database.DynamicRESTTool{
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
		ParamBindings:                   bindJSON,
		SafetyPolicy:                    safetyJSON,
	}

	if _, err := msgmate.BuildDynamicRESTToolDefinition(row); err != nil {
		http.Error(w, fmt.Sprintf("invalid dynamic rest tool definition: %v", err), http.StatusBadRequest)
		return
	}

	var existing database.DynamicRESTTool
	query := DB.Where("name = ?", req.Name)
	if nameFromPath != "" {
		query = DB.Where("name = ?", nameFromPath)
	}
	if err := query.First(&existing).Error; err == nil {
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
		existing.ParamBindings = row.ParamBindings
		existing.SafetyPolicy = row.SafetyPolicy
		if err := DB.Save(&existing).Error; err != nil {
			http.Error(w, "failed to update dynamic rest tool", http.StatusInternalServerError)
			return
		}
	} else {
		if err := DB.Create(&row).Error; err != nil {
			http.Error(w, "failed to create dynamic rest tool", http.StatusInternalServerError)
			return
		}
	}

	if err := msgmate.LoadDynamicRESTTools(DB); err != nil {
		http.Error(w, "failed to reload dynamic rest tools", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": req.Name})
}

func (h *ToolsHandler) DeleteDynamicRESTTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil || !user.IsAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	toolName := strings.TrimSpace(r.PathValue("tool_name"))
	if toolName == "" {
		http.Error(w, "tool_name is required", http.StatusBadRequest)
		return
	}
	if err := DB.Where("name = ?", toolName).Delete(&database.DynamicRESTTool{}).Error; err != nil {
		http.Error(w, "failed to delete dynamic rest tool", http.StatusInternalServerError)
		return
	}
	if err := msgmate.LoadDynamicRESTTools(DB); err != nil {
		http.Error(w, "failed to reload dynamic rest tools", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

func (h *ToolsHandler) ReloadDynamicRESTTools(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if user == nil || !user.IsAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err := msgmate.LoadDynamicRESTTools(DB); err != nil {
		http.Error(w, "failed to reload dynamic rest tools", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
