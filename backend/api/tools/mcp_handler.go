package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"backend/server/util"
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// MCPHandler handles Model Context Protocol (MCP) requests
type MCPHandler struct{}

// MCPRequest represents a JSON-RPC 2.0 request
type MCPRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	ID      interface{}            `json:"id"`
}

// MCPResponse represents a JSON-RPC 2.0 response
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// MCPError represents a JSON-RPC 2.0 error
type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCPTool represents a tool in MCP format
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPToolListResult represents the result of tools/list
type MCPToolListResult struct {
	Tools []MCPTool `json:"tools"`
}

// MCPToolCallResult represents the result of tools/call
type MCPToolCallResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent represents content in MCP format
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Standard MCP error codes
const (
	MCPErrorParse          = -32700
	MCPErrorInvalidRequest = -32600
	MCPErrorMethodNotFound = -32601
	MCPErrorInvalidParams  = -32602
	MCPErrorInternal       = -32603
)

// HandleMCP handles MCP JSON-RPC requests for a specific chat using HTTP Streamable transport
//
//	@Summary      Handle MCP JSON-RPC streaming requests
//	@Description  Handle Model Context Protocol streaming JSON-RPC requests for tool discovery and execution (bot users only)
//	@Tags         tools
//	@Accept       json
//	@Produce      application/x-ndjson
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {string} string "Streaming NDJSON responses"
//	@Failure      400 {object} MCPResponse
//	@Failure      403 {object} MCPResponse
//	@Failure      404 {object} MCPResponse
//	@Router       /api/v1/interactions/{chat_uuid}/mcp [post]
func (h *MCPHandler) HandleMCP(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInternal, "Unable to get database or user", nil)
		return
	}

	// Check if the user is a bot user
	if !isBotUser(user) {
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInternal, "Access denied: Only bot users can access MCP server", nil)
		return
	}

	chatUuid := r.PathValue("chat_uuid")
	if chatUuid == "" {
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInvalidParams, "Invalid chat UUID", nil)
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
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInternal, "Chat not found or access denied", nil)
		return
	}

	// Set up streaming headers for HTTP Streamable MCP
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Ensure the response writer supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInternal, "Streaming not supported", nil)
		return
	}

	log.Printf("[MCP Streamable] Bot user %s connected to chat %s", user.Name, chatUuid)

	// Create a scanner to read streaming input
	scanner := bufio.NewScanner(r.Body)

	// Process streaming JSON-RPC messages
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue // Skip empty lines
		}

		// Parse the MCP request
		var request MCPRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			h.sendMCPErrorNDJSON(w, nil, MCPErrorParse, "Invalid JSON", nil)
			flusher.Flush()
			continue
		}

		// Validate JSON-RPC 2.0 format
		if request.JSONRPC != "2.0" {
			h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInvalidRequest, "Invalid JSON-RPC version", nil)
			flusher.Flush()
			continue
		}

		log.Printf("[MCP Streamable] Processing method %s for user %s", request.Method, user.Name)

		// Route the request based on method
		switch request.Method {
		case "tools/list":
			h.handleToolsListStreamable(w, &request, &chat, DB, user)
		case "tools/call":
			h.handleToolsCallStreamable(w, &request, &chat, DB, user)
		default:
			h.sendMCPErrorNDJSON(w, request.ID, MCPErrorMethodNotFound, fmt.Sprintf("Unknown method: %s", request.Method), nil)
		}

		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[MCP Streamable] Scanner error: %v", err)
		h.sendMCPErrorNDJSON(w, nil, MCPErrorInternal, "Stream reading error", nil)
		flusher.Flush()
	}

	log.Printf("[MCP Streamable] Connection closed for user %s", user.Name)
}

// handleToolsList handles the tools/list MCP method
func (h *MCPHandler) handleToolsList(w http.ResponseWriter, request *MCPRequest, chat *database.Chat, DB *gorm.DB, user *database.User) {
	_ = DB // Parameter reserved for future use

	// Get the same tools that would be available to n8n
	toolsList := h.getAvailableToolsForChat(chat, user)

	// Convert tools to MCP format
	mcpTools := make([]MCPTool, 0, len(toolsList))

	for _, toolName := range toolsList {
		toolInstance := msgmate.GetNewToolInstanceByName(toolName, map[string]interface{}{})
		if toolInstance != nil {
			toolInfo := toolInstance.ConstructTool().(map[string]interface{})

			mcpTool := MCPTool{
				Name:        toolName,
				Description: h.getToolDescription(toolInfo),
				InputSchema: h.convertToMCPInputSchema(toolInfo),
			}
			mcpTools = append(mcpTools, mcpTool)
		}
	}

	result := MCPToolListResult{
		Tools: mcpTools,
	}

	h.sendMCPResponse(w, request.ID, result)
}

// handleToolsListStreamable handles the tools/list MCP method for streaming transport
func (h *MCPHandler) handleToolsListStreamable(w http.ResponseWriter, request *MCPRequest, chat *database.Chat, DB *gorm.DB, user *database.User) {
	_ = DB // Parameter reserved for future use

	// Get the same tools that would be available to n8n
	toolsList := h.getAvailableToolsForChat(chat, user)

	// Convert tools to MCP format
	mcpTools := make([]MCPTool, 0, len(toolsList))

	for _, toolName := range toolsList {
		toolInstance := msgmate.GetNewToolInstanceByName(toolName, map[string]interface{}{})
		if toolInstance != nil {
			toolInfo := toolInstance.ConstructTool().(map[string]interface{})

			mcpTool := MCPTool{
				Name:        toolName,
				Description: h.getToolDescription(toolInfo),
				InputSchema: h.convertToMCPInputSchema(toolInfo),
			}
			mcpTools = append(mcpTools, mcpTool)
		}
	}

	result := MCPToolListResult{
		Tools: mcpTools,
	}

	h.sendMCPResponseNDJSON(w, request.ID, result)
}

// handleToolsCall handles the tools/call MCP method
func (h *MCPHandler) handleToolsCall(w http.ResponseWriter, request *MCPRequest, chat *database.Chat, DB *gorm.DB, user *database.User) {
	_ = DB // Parameter reserved for future use

	// Extract tool name and arguments from params
	params, ok := request.Params["name"]
	if !ok {
		h.sendMCPError(w, request.ID, MCPErrorInvalidParams, "Missing tool name in params", nil)
		return
	}

	toolName, ok := params.(string)
	if !ok {
		h.sendMCPError(w, request.ID, MCPErrorInvalidParams, "Tool name must be a string", nil)
		return
	}

	// Get tool arguments
	var toolArguments map[string]interface{}
	if argsParam, exists := request.Params["arguments"]; exists {
		if args, ok := argsParam.(map[string]interface{}); ok {
			toolArguments = args
		}
	}

	// Check if tool is available for this chat
	availableTools := h.getAvailableToolsForChat(chat, user)
	toolAvailable := false
	for _, availableTool := range availableTools {
		if availableTool == toolName {
			toolAvailable = true
			break
		}
	}

	if !toolAvailable {
		h.sendMCPError(w, request.ID, MCPErrorInvalidParams, fmt.Sprintf("Tool '%s' not available for this chat", toolName), nil)
		return
	}

	// Get tool initialization data from chat configuration
	toolInitData := make(map[string]interface{})
	if chat.SharedConfig != nil && chat.SharedConfig.ConfigData != nil {
		var configData map[string]interface{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			if toolInit, exists := configData["tool_init"]; exists {
				if toolInitMap, ok := toolInit.(map[string]interface{}); ok {
					if initData, exists := toolInitMap[toolName]; exists {
						if initDataMap, ok := initData.(map[string]interface{}); ok {
							toolInitData = initDataMap
						}
					}
				}
			}
		}
	}

	// Get the tool instance
	toolInstance := msgmate.GetNewToolInstanceByName(toolName, toolInitData)
	if toolInstance == nil {
		h.sendMCPError(w, request.ID, MCPErrorInternal, fmt.Sprintf("Tool '%s' not found", toolName), nil)
		return
	}

	// Execute the tool
	log.Printf("[MCP] Executing tool %s for bot user %s in chat %s", toolName, user.Name, chat.UUID)

	var toolResult string
	var executionError error

	if toolArguments != nil {
		// Convert arguments to tool's expected input type
		toolInput, err := toolInstance.ParseArguments(convertMapToJSON(toolArguments))
		if err != nil {
			h.sendMCPError(w, request.ID, MCPErrorInvalidParams, fmt.Sprintf("Invalid tool arguments: %v", err), nil)
			return
		}
		toolResult, executionError = toolInstance.RunTool(toolInput)
	} else {
		// Execute tool without specific arguments
		toolInput, err := toolInstance.ParseArguments("{}")
		if err != nil {
			// Try with nil for tools that don't require input
			toolResult, executionError = toolInstance.RunTool(nil)
		} else {
			toolResult, executionError = toolInstance.RunTool(toolInput)
		}
	}

	// Prepare MCP response
	if executionError != nil {
		mcpResult := MCPToolCallResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("Error executing tool: %v", executionError),
			}},
			IsError: true,
		}
		h.sendMCPResponse(w, request.ID, mcpResult)
		log.Printf("[MCP] Tool %s execution failed: %v", toolName, executionError)
	} else {
		mcpResult := MCPToolCallResult{
			Content: []MCPContent{{
				Type: "text",
				Text: toolResult,
			}},
			IsError: false,
		}
		h.sendMCPResponse(w, request.ID, mcpResult)
		log.Printf("[MCP] Tool %s executed successfully", toolName)
	}
}

// handleToolsCallStreamable handles the tools/call MCP method for streaming transport
func (h *MCPHandler) handleToolsCallStreamable(w http.ResponseWriter, request *MCPRequest, chat *database.Chat, DB *gorm.DB, user *database.User) {
	_ = DB // Parameter reserved for future use

	// Extract tool name and arguments from params
	params, ok := request.Params["name"]
	if !ok {
		h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInvalidParams, "Missing tool name in params", nil)
		return
	}

	toolName, ok := params.(string)
	if !ok {
		h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInvalidParams, "Tool name must be a string", nil)
		return
	}

	// Get tool arguments
	var toolArguments map[string]interface{}
	if argsParam, exists := request.Params["arguments"]; exists {
		if args, ok := argsParam.(map[string]interface{}); ok {
			toolArguments = args
		}
	}

	// Check if tool is available for this chat
	availableTools := h.getAvailableToolsForChat(chat, user)
	toolAvailable := false
	for _, availableTool := range availableTools {
		if availableTool == toolName {
			toolAvailable = true
			break
		}
	}

	if !toolAvailable {
		h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInvalidParams, fmt.Sprintf("Tool '%s' not available for this chat", toolName), nil)
		return
	}

	// Get tool initialization data from chat configuration
	toolInitData := make(map[string]interface{})
	if chat.SharedConfig != nil && chat.SharedConfig.ConfigData != nil {
		var configData map[string]interface{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			if toolInit, exists := configData["tool_init"]; exists {
				if toolInitMap, ok := toolInit.(map[string]interface{}); ok {
					if initData, exists := toolInitMap[toolName]; exists {
						if initDataMap, ok := initData.(map[string]interface{}); ok {
							toolInitData = initDataMap
						}
					}
				}
			}
		}
	}

	// Get the tool instance
	toolInstance := msgmate.GetNewToolInstanceByName(toolName, toolInitData)
	if toolInstance == nil {
		h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInternal, fmt.Sprintf("Tool '%s' not found", toolName), nil)
		return
	}

	// Execute the tool
	log.Printf("[MCP Streamable] Executing tool %s for bot user %s in chat %s", toolName, user.Name, chat.UUID)

	var toolResult string
	var executionError error

	if toolArguments != nil {
		// Convert arguments to tool's expected input type
		toolInput, err := toolInstance.ParseArguments(convertMapToJSON(toolArguments))
		if err != nil {
			h.sendMCPErrorNDJSON(w, request.ID, MCPErrorInvalidParams, fmt.Sprintf("Invalid tool arguments: %v", err), nil)
			return
		}
		toolResult, executionError = toolInstance.RunTool(toolInput)
	} else {
		// Execute tool without specific arguments
		toolInput, err := toolInstance.ParseArguments("{}")
		if err != nil {
			// Try with nil for tools that don't require input
			toolResult, executionError = toolInstance.RunTool(nil)
		} else {
			toolResult, executionError = toolInstance.RunTool(toolInput)
		}
	}

	// Prepare MCP response
	if executionError != nil {
		mcpResult := MCPToolCallResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("Error executing tool: %v", executionError),
			}},
			IsError: true,
		}
		h.sendMCPResponseNDJSON(w, request.ID, mcpResult)
		log.Printf("[MCP Streamable] Tool %s execution failed: %v", toolName, executionError)
	} else {
		mcpResult := MCPToolCallResult{
			Content: []MCPContent{{
				Type: "text",
				Text: toolResult,
			}},
			IsError: false,
		}
		h.sendMCPResponseNDJSON(w, request.ID, mcpResult)
		log.Printf("[MCP Streamable] Tool %s executed successfully", toolName)
	}
}

// getAvailableToolsForChat returns the list of tools available for a specific chat
// This mirrors the logic used in the Signal bot service
func (h *MCPHandler) getAvailableToolsForChat(chat *database.Chat, user *database.User) []string {
	// Base tools available to all chats
	toolsList := []string{
		"signal_read_past_messages",
		"signal_send_message",
		"get_current_time",
		"interaction_start:run_callback_function",
		"interaction_complete:run_callback_function",
	}

	// Check if this is an admin user by examining chat config
	isAdmin := h.isAdminForChat(chat, user)
	if isAdmin {
		toolsList = append(toolsList,
			"signal_get_whitelist",
			"signal_add_to_whitelist",
			"signal_remove_from_whitelist")
	}

	// Check if N8N tools should be available
	// This could be determined from environment variables or chat configuration
	// For now, we'll include it if the environment suggests it should be available
	// Note: In practice, you might want to check environment variables or chat config
	toolsList = append(toolsList, "n8n_trigger_workflow_webhook")

	return toolsList
}

// isAdminForChat checks if a user is an admin for a specific chat
func (h *MCPHandler) isAdminForChat(chat *database.Chat, user *database.User) bool {
	// Check chat configuration to determine admin status
	// This would need to be implemented based on your specific logic
	// For now, we'll use a simple heuristic
	if chat.SharedConfig != nil && chat.SharedConfig.ConfigData != nil {
		var configData map[string]interface{}
		if err := json.Unmarshal(chat.SharedConfig.ConfigData, &configData); err == nil {
			// You could store admin information in the chat config
			// For now, we'll assume signal users can be admins
			return user.Name == "signal"
		}
	}
	return false
}

// getToolDescription extracts description from tool info
func (h *MCPHandler) getToolDescription(toolInfo map[string]interface{}) string {
	if desc, exists := toolInfo["description"]; exists {
		if descStr, ok := desc.(string); ok {
			return descStr
		}
	}
	if name, exists := toolInfo["name"]; exists {
		if nameStr, ok := name.(string); ok {
			return fmt.Sprintf("Tool: %s", nameStr)
		}
	}
	return "No description available"
}

// convertToMCPInputSchema converts tool schema to MCP input schema format
func (h *MCPHandler) convertToMCPInputSchema(toolInfo map[string]interface{}) map[string]interface{} {
	// Default schema
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}

	// Try to extract schema from tool info
	if parameters, exists := toolInfo["parameters"]; exists {
		if paramMap, ok := parameters.(map[string]interface{}); ok {
			// If the tool already has a proper schema, use it
			if paramType, hasType := paramMap["type"]; hasType && paramType == "object" {
				return paramMap
			}

			// Otherwise, convert properties
			if properties, hasProps := paramMap["properties"]; hasProps {
				if propsMap, ok := properties.(map[string]interface{}); ok {
					schema["properties"] = propsMap
				}
			}

			if required, hasReq := paramMap["required"]; hasReq {
				if reqArray, ok := required.([]interface{}); ok {
					schema["required"] = reqArray
				}
			}
		}
	}

	return schema
}

// sendMCPResponse sends a successful MCP JSON-RPC response
func (h *MCPHandler) sendMCPResponse(w http.ResponseWriter, id interface{}, result interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// sendMCPError sends an MCP JSON-RPC error response
func (h *MCPHandler) sendMCPError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		Error: &MCPError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // MCP errors are still HTTP 200
	json.NewEncoder(w).Encode(response)
}

// sendMCPResponseNDJSON sends a successful MCP JSON-RPC response as NDJSON (for streaming)
func (h *MCPHandler) sendMCPResponseNDJSON(w http.ResponseWriter, id interface{}, result interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}

	// Marshal to JSON and write as newline-delimited
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("[MCP Streamable] Error marshaling response: %v", err)
		return
	}

	// Write JSON followed by newline for NDJSON format
	w.Write(jsonBytes)
	w.Write([]byte("\n"))
}

// sendMCPErrorNDJSON sends an MCP JSON-RPC error response as NDJSON (for streaming)
func (h *MCPHandler) sendMCPErrorNDJSON(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		Error: &MCPError{
			Code:    code,
			Message: message,
			Data:    data,
		},
		ID: id,
	}

	// Marshal to JSON and write as newline-delimited
	jsonBytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("[MCP Streamable] Error marshaling error response: %v", err)
		return
	}

	// Write JSON followed by newline for NDJSON format
	w.Write(jsonBytes)
	w.Write([]byte("\n"))
}
