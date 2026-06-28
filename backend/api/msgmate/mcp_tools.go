package msgmate

import (
	"backend/database"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

type mcpRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpIntegrationConfig struct {
	Transport             string
	URL                   string
	RequestTimeoutSeconds int
}

func parseMCPIntegrationConfig(raw map[string]interface{}) (mcpIntegrationConfig, error) {
	out := mcpIntegrationConfig{Transport: "http", RequestTimeoutSeconds: 25}
	if raw == nil {
		return out, fmt.Errorf("config is required")
	}
	if transportRaw, ok := raw["transport"]; ok {
		if transport, ok := transportRaw.(string); ok {
			out.Transport = strings.TrimSpace(strings.ToLower(transport))
		}
	}
	if out.Transport == "" {
		out.Transport = "http"
	}
	if out.Transport != "http" && out.Transport != "http_streamable" {
		return out, fmt.Errorf("config.transport must be 'http' or 'http_streamable'")
	}
	urlRaw, _ := raw["url"].(string)
	out.URL = strings.TrimSpace(urlRaw)
	if out.URL == "" {
		return out, fmt.Errorf("config.url is required")
	}
	if !strings.HasPrefix(strings.ToLower(out.URL), "https://") {
		return out, fmt.Errorf("config.url must use https")
	}
	if timeoutRaw, ok := raw["request_timeout_seconds"]; ok {
		switch v := timeoutRaw.(type) {
		case float64:
			if v >= 1 && v <= 120 {
				out.RequestTimeoutSeconds = int(v)
			}
		case int:
			if v >= 1 && v <= 120 {
				out.RequestTimeoutSeconds = v
			}
		}
	}
	return out, nil
}

func parseMCPAuthHeaders(raw map[string]interface{}) map[string]string {
	headers := map[string]string{}
	if raw == nil {
		return headers
	}
	if bearerRaw, ok := raw["bearer_token"]; ok {
		if bearer, ok := bearerRaw.(string); ok {
			trimmed := strings.TrimSpace(bearer)
			if trimmed != "" {
				headers["Authorization"] = "Bearer " + trimmed
			}
		}
	}
	if headersRaw, ok := raw["headers"].(map[string]interface{}); ok {
		for key, value := range headersRaw {
			name := strings.TrimSpace(key)
			if name == "" {
				continue
			}
			if strValue, ok := value.(string); ok {
				trimmed := strings.TrimSpace(strValue)
				if trimmed != "" {
					headers[name] = trimmed
				}
			}
		}
	}
	return headers
}

func mcpCall(config mcpIntegrationConfig, auth map[string]interface{}, method string, params interface{}) (mcpRPCResponse, error) {
	body, err := json.Marshal(mcpRPCRequest{JSONRPC: "2.0", ID: "open-chat", Method: method, Params: params})
	if err != nil {
		return mcpRPCResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return mcpRPCResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/x-ndjson")
	for key, value := range parseMCPAuthHeaders(auth) {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: time.Duration(config.RequestTimeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return mcpRPCResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return mcpRPCResponse{}, fmt.Errorf("mcp request failed: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return mcpRPCResponse{}, err
	}
	parsed, err := parseMCPResponseBody(responseBody)
	if err != nil {
		return mcpRPCResponse{}, err
	}
	if parsed.Error != nil {
		return mcpRPCResponse{}, fmt.Errorf("mcp error %d: %s", parsed.Error.Code, parsed.Error.Message)
	}
	return parsed, nil
}

func parseMCPResponseBody(body []byte) (mcpRPCResponse, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return mcpRPCResponse{}, fmt.Errorf("empty mcp response")
	}
	if strings.HasPrefix(trimmed, "{") {
		var parsed mcpRPCResponse
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return mcpRPCResponse{}, err
		}
		return parsed, nil
	}
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var parsed mcpRPCResponse
		if err := json.Unmarshal([]byte(line), &parsed); err == nil {
			return parsed, nil
		}
	}
	return mcpRPCResponse{}, fmt.Errorf("invalid mcp response payload")
}

func DiscoverMCPTools(config map[string]interface{}, auth map[string]interface{}) ([]map[string]interface{}, error) {
	parsed, err := parseMCPIntegrationConfig(config)
	if err != nil {
		return nil, err
	}
	resp, err := mcpCall(parsed, auth, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("invalid tools/list result: %w", err)
	}
	rawTools, ok := result["tools"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("tools/list result missing tools array")
	}
	rows := make([]map[string]interface{}, 0, len(rawTools))
	for _, raw := range rawTools {
		tool, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		rows = append(rows, tool)
	}
	return rows, nil
}

func BuildMCPToolsSnapshotFromIntegrations(rows []database.MCPIntegrationConfig) (map[string]interface{}, []string, error) {
	snapshot := map[string]interface{}{}
	toolNames := []string{}
	for _, row := range rows {
		config := map[string]interface{}{}
		auth := map[string]interface{}{}
		if len(row.Config) > 0 {
			if err := json.Unmarshal(row.Config, &config); err != nil {
				return nil, nil, fmt.Errorf("invalid integration config for %q: %w", row.Name, err)
			}
		}
		if len(row.AuthData) > 0 {
			if err := json.Unmarshal(row.AuthData, &auth); err != nil {
				return nil, nil, fmt.Errorf("invalid integration auth_data for %q: %w", row.Name, err)
			}
		}
		tools, err := DiscoverMCPTools(config, auth)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to discover MCP tools for %q: %w", row.Name, err)
		}
		for _, remoteTool := range tools {
			remoteName, _ := remoteTool["name"].(string)
			remoteName = strings.TrimSpace(remoteName)
			if remoteName == "" {
				continue
			}
			namespacedName := "mcp:" + row.Name + ":" + remoteName
			snapshot[namespacedName] = map[string]interface{}{
				"name":             namespacedName,
				"integration_name": row.Name,
				"remote_tool_name": remoteName,
				"description":      remoteTool["description"],
				"input_schema":     remoteTool["inputSchema"],
				"config":           config,
				"auth_data":        auth,
			}
			toolNames = append(toolNames, namespacedName)
		}
	}
	sort.Strings(toolNames)
	return snapshot, toolNames, nil
}

func NewMCPToolFromSnapshot(toolName string, mcpToolsRaw interface{}) (Tool, bool, error) {
	mcpToolsMap, ok := mcpToolsRaw.(map[string]interface{})
	if !ok || mcpToolsMap == nil {
		return nil, false, nil
	}
	rawDef, exists := mcpToolsMap[toolName]
	if !exists {
		return nil, false, nil
	}
	defMap, ok := rawDef.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("mcp tool %q definition must be an object", toolName)
	}

	integrationName, _ := defMap["integration_name"].(string)
	remoteToolName, _ := defMap["remote_tool_name"].(string)
	description, _ := defMap["description"].(string)
	config, _ := defMap["config"].(map[string]interface{})
	auth, _ := defMap["auth_data"].(map[string]interface{})
	inputSchema, _ := defMap["input_schema"].(map[string]interface{})
	if strings.TrimSpace(integrationName) == "" || strings.TrimSpace(remoteToolName) == "" {
		return nil, false, fmt.Errorf("mcp tool %q is missing integration_name or remote_tool_name", toolName)
	}
	if inputSchema == nil {
		inputSchema = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		}
	}

	functionName := mcpFunctionName(integrationName, remoteToolName)
	if functionName == "" {
		functionName = "mcp_tool"
	}

	def := ToolDefinition{
		Name:         toolName,
		FunctionName: functionName,
		Description:  strings.TrimSpace(description),
		InputType:    map[string]interface{}{},
		InputSchema:  inputSchema,
		Parameters:   map[string]interface{}{},
		RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
			args := map[string]interface{}{}
			if input != nil {
				if parsed, ok := input.(map[string]interface{}); ok {
					args = parsed
				}
			}
			parsedConfig, err := parseMCPIntegrationConfig(config)
			if err != nil {
				return "", err
			}
			resp, err := mcpCall(parsedConfig, auth, "tools/call", map[string]interface{}{
				"name":      remoteToolName,
				"arguments": args,
			})
			if err != nil {
				return "", err
			}
			result := map[string]interface{}{}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return "", fmt.Errorf("invalid tools/call result: %w", err)
			}
			if isError, _ := result["isError"].(bool); isError {
				resultJSON, _ := json.Marshal(result)
				return "", fmt.Errorf("mcp tool call returned isError=true: %s", string(resultJSON))
			}
			if content, ok := result["content"].([]interface{}); ok && len(content) > 0 {
				parts := make([]string, 0, len(content))
				for _, item := range content {
					if obj, ok := item.(map[string]interface{}); ok {
						if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
							parts = append(parts, text)
						}
					}
				}
				if len(parts) > 0 {
					return strings.Join(parts, "\n"), nil
				}
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}
	if strings.TrimSpace(def.Description) == "" {
		def.Description = "MCP tool from integration " + integrationName
	}
	return NewToolFromDefinition(def), true, nil
}

func mcpFunctionName(integrationName string, remoteToolName string) string {
	raw := "mcp_" + integrationName + "_" + remoteToolName
	var out []rune
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			out = append(out, r)
			continue
		}
		out = append(out, '_')
	}
	trimmed := strings.Trim(string(out), "_-")
	if len(trimmed) > 64 {
		return trimmed[:64]
	}
	return trimmed
}
