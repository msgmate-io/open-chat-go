package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ToolExecutionRequest represents the request to execute a tool
type ToolExecutionRequest struct {
	InputParameters map[string]interface{} `json:"input_parameters"`
}

// ToolExecutionResponse represents the response from tool execution
type ToolExecutionResponse struct {
	Success bool                   `json:"success"`
	Result  string                 `json:"result,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Tool    map[string]interface{} `json:"tool_info,omitempty"`
}

// ToolsHandler handles tool execution requests
type ToolsHandler struct{}

// isBotUser checks if the given user is a bot user
func isBotUser(user *database.User) bool {
	// Bot users are identified by specific name patterns
	fmt.Println("user.Name", user.Name)
	botNames := []string{"signal", "bot", "msgmate"}
	userName := strings.ToLower(user.Name)

	for _, botName := range botNames {
		if userName == botName {
			return true
		}
	}
	return false
}

// ExecuteTool handles tool execution requests from bot users
//
//	@Summary      Execute a tool
//	@Description  Execute a tool for a specific chat interaction (bot users only)
//	@Tags         tools
//	@Accept       json
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        tool_name path string true "Tool Name"
//	@Param        request body ToolExecutionRequest true "Tool execution request"
//	@Success      200 {object} ToolExecutionResponse
//	@Failure      400 {object} map[string]string
//	@Failure      403 {object} map[string]string
//	@Failure      404 {object} map[string]string
//	@Router       /api/v1/interactions/{chat_uuid}/tools/{tool_name} [post]
func (h *ToolsHandler) ExecuteTool(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if the user is a bot user
	if !isBotUser(user) {
		http.Error(w, "Access denied: Only bot users can execute tools", http.StatusForbidden)
		return
	}

	chatUuid := r.PathValue("chat_uuid")
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	toolName := r.PathValue("tool_name")
	if toolName == "" {
		http.Error(w, "Invalid tool name", http.StatusBadRequest)
		return
	}

	// Validate that the chat exists and the user has access to it
	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Chat not found or access denied", http.StatusNotFound)
		return
	}

	// Parse the request body
	var request ToolExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Get tool initialization data from chat configuration
	toolInitData := make(map[string]interface{})
	if chat.SharedConfig != nil && chat.SharedConfig.ConfigData != nil {
		var configData map[string]interface{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			log.Printf("[ToolExecution] Chat config data: %+v", configData)
			if toolInit, exists := configData["tool_init"]; exists {
				log.Printf("[ToolExecution] Tool init data found: %+v", toolInit)
				if toolInitMap, ok := toolInit.(map[string]interface{}); ok {
					log.Printf("[ToolExecution] Looking for tool %s in tool init map", toolName)
					if initData, exists := toolInitMap[toolName]; exists {
						log.Printf("[ToolExecution] Found init data for tool %s: %+v", toolName, initData)
						if initDataMap, ok := initData.(map[string]interface{}); ok {
							toolInitData = initDataMap
						}
					} else {
						log.Printf("[ToolExecution] Tool %s not found in tool init map. Available tools: %v", toolName, getKeys(toolInitMap))
					}
				}
			} else {
				log.Printf("[ToolExecution] No tool_init key found in chat config")
			}
		} else {
			log.Printf("[ToolExecution] Failed to unmarshal chat config: %v", err)
		}
	} else {
		log.Printf("[ToolExecution] Chat has no SharedConfig or ConfigData")
	}

	if len(toolInitData) == 0 {
		log.Printf("[ToolExecution] No init data found for tool %s in chat %s", toolName, chatUuid)
	}

	// Get the tool instance
	toolInstance := msgmate.GetNewToolInstanceByName(toolName, toolInitData)
	if toolInstance == nil {
		http.Error(w, fmt.Sprintf("Tool '%s' not found", toolName), http.StatusNotFound)
		return
	}

	// Get tool information
	toolInfo := toolInstance.ConstructTool().(map[string]interface{})

	// Execute the tool
	log.Printf("[ToolExecution] Bot user %s executing tool %s in chat %s", user.Name, toolName, chatUuid)

	var toolResult string
	var executionError error

	// Check if the tool requires specific input parameters
	if request.InputParameters != nil {
		// Convert input parameters to the tool's expected input type
		toolInput, err := toolInstance.ParseArguments(convertMapToJSON(request.InputParameters))
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid tool input parameters: %v", err), http.StatusBadRequest)
			return
		}

		toolResult, executionError = toolInstance.RunTool(toolInput)
	} else {
		// Execute tool without specific input parameters - try to create a default input
		toolInput, err := toolInstance.ParseArguments("{}")
		if err != nil {
			// If parsing fails, try with nil (for tools that don't require input)
			toolResult, executionError = toolInstance.RunTool(nil)
		} else {
			toolResult, executionError = toolInstance.RunTool(toolInput)
		}
	}

	// Prepare response
	response := ToolExecutionResponse{
		Success: executionError == nil,
		Tool:    toolInfo,
	}

	if executionError != nil {
		response.Error = executionError.Error()
		log.Printf("[ToolExecution] Tool %s execution failed: %v", toolName, executionError)
	} else {
		response.Result = toolResult
		log.Printf("[ToolExecution] Tool %s executed successfully", toolName)
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetAvailableTools returns the list of available tools for a chat
//
//	@Summary      Get available tools
//	@Description  Get the list of available tools for a specific chat (bot users only)
//	@Tags         tools
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} map[string]interface{}
//	@Failure      400 {object} map[string]string
//	@Failure      403 {object} map[string]string
//	@Failure      404 {object} map[string]string
//	@Router       /api/v1/interactions/{chat_uuid}/tools [get]
func (h *ToolsHandler) GetAvailableTools(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if the user is a bot user
	if !isBotUser(user) {
		http.Error(w, "Access denied: Only bot users can access tools", http.StatusForbidden)
		return
	}

	chatUuid := r.PathValue("chat_uuid")
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	// Validate that the chat exists and the user has access to it
	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Chat not found or access denied", http.StatusNotFound)
		return
	}

	// Get all available tools
	availableTools := make(map[string]interface{})
	for _, tool := range msgmate.AllTools {
		toolInfo := tool.ConstructTool().(map[string]interface{})
		availableTools[tool.GetToolName()] = toolInfo
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tools":   availableTools,
		"count":   len(availableTools),
	})
}

// StoreToolInitDataRequest represents the request to store tool initialization data
type StoreToolInitDataRequest struct {
	ToolName  string                 `json:"tool_name"`
	InitData  map[string]interface{} `json:"init_data"`
	ExpiresAt *string                `json:"expires_at,omitempty"` // ISO 8601 timestamp
}

// StoreToolInitData stores tool initialization data for a chat (bot users only)
//
//	@Summary      Store tool initialization data
//	@Description  Store tool initialization data for a specific chat and tool (bot users only)
//	@Tags         tools
//	@Accept       json
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Param        request body StoreToolInitDataRequest true "Tool init data request"
//	@Success      200 {object} map[string]string
//	@Failure      400 {object} map[string]string
//	@Failure      403 {object} map[string]string
//	@Failure      404 {object} map[string]string
//	@Router       /api/v1/interactions/{chat_uuid}/tools/init [post]
func (h *ToolsHandler) StoreToolInitData(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Check if the user is a bot user
	if !isBotUser(user) {
		http.Error(w, "Access denied: Only bot users can store tool init data", http.StatusForbidden)
		return
	}

	chatUuid := r.PathValue("chat_uuid")
	if chatUuid == "" {
		http.Error(w, "Invalid chat UUID", http.StatusBadRequest)
		return
	}

	// Validate that the chat exists and the user has access to it
	var chat database.Chat
	result := DB.Preload("User1").
		Preload("User2").
		Preload("SharedConfig").
		Where("uuid = ? AND (user1_id = ? OR user2_id = ?)", chatUuid, user.ID, user.ID).
		First(&chat)

	if result.Error != nil {
		http.Error(w, "Chat not found or access denied", http.StatusNotFound)
		return
	}

	// Parse the request body
	var request StoreToolInitDataRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if request.ToolName == "" {
		http.Error(w, "Tool name is required", http.StatusBadRequest)
		return
	}

	// Store tool initialization data
	toolInitManager := database.NewToolInitDataManager(DB)
	err = toolInitManager.StoreToolInitData(chat.ID, request.ToolName, request.InitData)
	if err != nil {
		log.Printf("[ToolInitData] Failed to store init data for tool %s in chat %s: %v", request.ToolName, chatUuid, err)
		http.Error(w, "Failed to store tool init data", http.StatusInternalServerError)
		return
	}

	// Set expiration if provided
	if request.ExpiresAt != nil {
		expiresAt, err := time.Parse(time.RFC3339, *request.ExpiresAt)
		if err != nil {
			http.Error(w, "Invalid expiration timestamp format", http.StatusBadRequest)
			return
		}
		err = toolInitManager.SetExpiration(chat.ID, request.ToolName, expiresAt)
		if err != nil {
			log.Printf("[ToolInitData] Failed to set expiration for tool %s in chat %s: %v", request.ToolName, chatUuid, err)
			// Don't fail the request for this
		}
	}

	log.Printf("[ToolInitData] Stored init data for tool %s in chat %s", request.ToolName, chatUuid)

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"success": "Tool init data stored successfully",
	})
}

// convertMapToJSON converts a map to JSON string
func convertMapToJSON(data map[string]interface{}) string {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[ToolExecution] Error converting map to JSON: %v", err)
		return "{}"
	}
	return string(jsonBytes)
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
