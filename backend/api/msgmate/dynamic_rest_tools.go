package msgmate

import (
	"backend/api/msgmate/tools"
	"backend/database"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

type restToolParamBinding struct {
	InputName   string `json:"input_name"`
	Source      string `json:"source"`
	In          string `json:"in"`
	Name        string `json:"name"`
	Required    *bool  `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

type restToolSafetyPolicy struct {
	AllowHosts      []string `json:"allow_hosts,omitempty"`
	AllowPrivateIPs bool     `json:"allow_private_ips,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	MaxResponseBody int64    `json:"max_response_body_bytes,omitempty"`
}

type operationParam struct {
	In          string
	Name        string
	Required    bool
	Description string
	Schema      map[string]interface{}
}

type compiledBinding struct {
	restToolParamBinding
	Required bool
}

type dynamicRESTToolSpec struct {
	Method   string
	Path     string
	BaseURL  string
	Bindings []compiledBinding
	Safety   restToolSafetyPolicy
}

func ResolveUserDynamicRESTToolByName(db *gorm.DB, ownerUserID uint, toolName string) (*database.DynamicRESTTool, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	if ownerUserID == 0 {
		return nil, fmt.Errorf("owner user id is required")
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, fmt.Errorf("tool name is required")
	}

	var row database.DynamicRESTTool
	if err := db.Where("owner_user_id = ? AND name = ? AND enabled = ?", ownerUserID, toolName, true).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func BuildDynamicRESTToolSnapshot(row database.DynamicRESTTool) map[string]interface{} {
	return map[string]interface{}{
		"name":                               row.Name,
		"function_name":                      row.FunctionName,
		"description":                        row.Description,
		"admin_only":                         row.AdminOnly,
		"requires_confirmation":              row.RequiresConfirmation,
		"stop_on_first_confirmable_tool_call": row.StopOnFirstConfirmableToolCall,
		"confirmation_block_message":         row.ConfirmationBlockMessage,
		"openapi_source_type":                row.OpenAPISourceType,
		"openapi_source":                     row.OpenAPISource,
		"operation_id":                       row.OperationID,
		"http_method":                        row.HTTPMethod,
		"path":                               row.Path,
		"param_bindings":                     parseJSONArrayOrEmpty(row.ParamBindings),
		"safety_policy":                      parseJSONObjectOrEmpty(row.SafetyPolicy),
	}
}

func NewDynamicRESTToolFromSnapshot(toolName string, dynamicToolsRaw interface{}) (Tool, bool, error) {
	dynamicToolsMap, ok := dynamicToolsRaw.(map[string]interface{})
	if !ok || dynamicToolsMap == nil {
		return nil, false, nil
	}
	rawDef, exists := dynamicToolsMap[toolName]
	if !exists {
		return nil, false, nil
	}
	defMap, ok := rawDef.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("dynamic tool %q definition must be an object", toolName)
	}

	row, err := dynamicRESTToolFromMap(defMap)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(row.Name) == "" {
		row.Name = toolName
	}
	def, err := BuildDynamicRESTToolDefinition(row)
	if err != nil {
		return nil, false, err
	}
	return NewToolFromDefinition(def), true, nil
}

func GetNewToolInstanceByNameOrSnapshot(toolName string, initData map[string]interface{}, dynamicToolsRaw interface{}) (Tool, error) {
	if tool, found := NewToolByName(toolName); found && tool != nil {
		if tool.GetRequiresInit() {
			tool.SetInitData(initData)
		}
		return tool, nil
	}

	dynamicTool, found, err := NewDynamicRESTToolFromSnapshot(toolName, dynamicToolsRaw)
	if err != nil {
		return nil, err
	}
	if !found || dynamicTool == nil {
		return nil, nil
	}
	if dynamicTool.GetRequiresInit() {
		dynamicTool.SetInitData(initData)
	}
	return dynamicTool, nil
}

func dynamicRESTToolFromMap(input map[string]interface{}) (database.DynamicRESTTool, error) {
	row := database.DynamicRESTTool{}
	row.Name, _ = input["name"].(string)
	row.FunctionName, _ = input["function_name"].(string)
	row.Description, _ = input["description"].(string)
	row.AdminOnly, _ = input["admin_only"].(bool)
	row.RequiresConfirmation, _ = input["requires_confirmation"].(bool)
	row.StopOnFirstConfirmableToolCall, _ = input["stop_on_first_confirmable_tool_call"].(bool)
	row.ConfirmationBlockMessage, _ = input["confirmation_block_message"].(string)
	row.OpenAPISourceType, _ = input["openapi_source_type"].(string)
	row.OpenAPISource, _ = input["openapi_source"].(string)
	row.OperationID, _ = input["operation_id"].(string)
	row.HTTPMethod, _ = input["http_method"].(string)
	row.Path, _ = input["path"].(string)

	paramBindingsJSON, err := json.Marshal(input["param_bindings"])
	if err != nil {
		return row, fmt.Errorf("invalid param_bindings: %w", err)
	}
	row.ParamBindings = paramBindingsJSON

	safetyPolicyJSON, err := json.Marshal(input["safety_policy"])
	if err != nil {
		return row, fmt.Errorf("invalid safety_policy: %w", err)
	}
	row.SafetyPolicy = safetyPolicyJSON
	return row, nil
}

func parseJSONArrayOrEmpty(raw json.RawMessage) []map[string]interface{} {
	if len(raw) == 0 {
		return []map[string]interface{}{}
	}
	out := []map[string]interface{}{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func parseJSONObjectOrEmpty(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func BuildDynamicRESTToolDefinition(row database.DynamicRESTTool) (tools.ToolDefinition, error) {
	if strings.TrimSpace(row.Name) == "" {
		return tools.ToolDefinition{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(row.OpenAPISourceType) == "" {
		row.OpenAPISourceType = "inline"
	}

	doc, err := loadOpenAPIDocument(row.OpenAPISourceType, row.OpenAPISource)
	if err != nil {
		return tools.ToolDefinition{}, err
	}
	baseURL, params, method, path, err := resolveOperation(doc, row.OperationID, row.HTTPMethod, row.Path)
	if err != nil {
		return tools.ToolDefinition{}, err
	}

	bindings, safety, err := compileBindingsAndPolicy(params, row.ParamBindings, row.SafetyPolicy)
	if err != nil {
		return tools.ToolDefinition{}, err
	}

	callSchema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false}
	initSchema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false}
	callProps := callSchema["properties"].(map[string]interface{})
	initProps := initSchema["properties"].(map[string]interface{})
	callRequired := []string{}
	initRequired := []string{}

	for _, binding := range bindings {
		prop := map[string]interface{}{"type": "string"}
		if schema := bindingSchema(params, binding.In, binding.Name); schema != nil {
			prop = cloneMap(schema)
		}
		if binding.Description != "" {
			prop["description"] = binding.Description
		}
		if binding.Source == "init" {
			initProps[binding.InputName] = prop
			if binding.Required {
				initRequired = append(initRequired, binding.InputName)
			}
		} else {
			callProps[binding.InputName] = prop
			if binding.Required {
				callRequired = append(callRequired, binding.InputName)
			}
		}
	}
	callSchema["required"] = callRequired
	initSchema["required"] = initRequired

	spec := dynamicRESTToolSpec{Method: method, Path: path, BaseURL: baseURL, Bindings: bindings, Safety: safety}

	functionName := strings.TrimSpace(row.FunctionName)
	if functionName == "" {
		functionName = row.Name
	}

	def := tools.ToolDefinition{
		Name:                           row.Name,
		FunctionName:                   functionName,
		Description:                    row.Description,
		AdminOnly:                      row.AdminOnly,
		RequiresInit:                   len(initProps) > 0,
		InitSchema:                     initSchema,
		RequiresConfirmation:           row.RequiresConfirmation,
		StopOnFirstConfirmableToolCall: row.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:       row.ConfirmationBlockMessage,
		InputType:                      map[string]interface{}{},
		InputSchema:                    callSchema,
		RunFunction:                    runDynamicRESTTool(spec),
	}
	return def, nil
}

func runDynamicRESTTool(spec dynamicRESTToolSpec) func(input interface{}, init map[string]interface{}) (string, error) {
	return func(input interface{}, init map[string]interface{}) (string, error) {
		callInput := normalizeInterfaceMap(input)
		initInput := init
		if initInput == nil {
			initInput = map[string]interface{}{}
		}

		if err := validateOutboundURL(spec.BaseURL, spec.Safety); err != nil {
			return "", err
		}

		path := spec.Path
		query := url.Values{}
		headers := http.Header{}
		cookies := []*http.Cookie{}
		body := map[string]interface{}{}

		for _, binding := range spec.Bindings {
			var source map[string]interface{}
			if binding.Source == "init" {
				source = initInput
			} else {
				source = callInput
			}
			value, exists := source[binding.InputName]
			if !exists {
				if binding.Required {
					return "", fmt.Errorf("missing required %s field %q", binding.Source, binding.InputName)
				}
				continue
			}
			valueStr := fmt.Sprintf("%v", value)
			switch binding.In {
			case "path":
				path = strings.ReplaceAll(path, "{"+binding.Name+"}", url.PathEscape(valueStr))
			case "query":
				query.Set(binding.Name, valueStr)
			case "header":
				headers.Set(binding.Name, valueStr)
			case "cookie":
				cookies = append(cookies, &http.Cookie{Name: binding.Name, Value: valueStr})
			case "body":
				body[binding.Name] = value
			}
		}

		requestURL := strings.TrimRight(spec.BaseURL, "/") + path
		if encoded := query.Encode(); encoded != "" {
			requestURL += "?" + encoded
		}

		var bodyReader io.Reader
		if len(body) > 0 {
			encoded, err := json.Marshal(body)
			if err != nil {
				return "", err
			}
			bodyReader = bytes.NewReader(encoded)
			headers.Set("Content-Type", "application/json")
		}

		req, err := http.NewRequest(spec.Method, requestURL, bodyReader)
		if err != nil {
			return "", err
		}
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}

		timeout := spec.Safety.TimeoutSeconds
		if timeout <= 0 {
			timeout = 20
		}
		client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		maxBytes := spec.Safety.MaxResponseBody
		if maxBytes <= 0 {
			maxBytes = 64 * 1024
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
		if err != nil {
			return "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("received non-success status %d: %s", resp.StatusCode, string(data))
		}
		return string(data), nil
	}
}

func compileBindingsAndPolicy(params []operationParam, bindingsRaw json.RawMessage, safetyRaw json.RawMessage) ([]compiledBinding, restToolSafetyPolicy, error) {
	available := map[string]operationParam{}
	for _, p := range params {
		available[p.In+":"+p.Name] = p
	}

	bindings := []restToolParamBinding{}
	if len(bindingsRaw) > 0 {
		if err := json.Unmarshal(bindingsRaw, &bindings); err != nil {
			return nil, restToolSafetyPolicy{}, fmt.Errorf("invalid param_bindings: %w", err)
		}
	}
	if len(bindings) == 0 {
		for _, p := range params {
			bindings = append(bindings, restToolParamBinding{InputName: p.Name, Source: "call", In: p.In, Name: p.Name})
		}
	}

	out := make([]compiledBinding, 0, len(bindings))
	for idx, binding := range bindings {
		binding.InputName = strings.TrimSpace(binding.InputName)
		binding.Source = strings.ToLower(strings.TrimSpace(binding.Source))
		binding.In = strings.ToLower(strings.TrimSpace(binding.In))
		binding.Name = strings.TrimSpace(binding.Name)
		if binding.InputName == "" || binding.Name == "" || binding.In == "" {
			return nil, restToolSafetyPolicy{}, fmt.Errorf("param_bindings[%d] requires input_name, in, and name", idx)
		}
		if binding.Source != "call" && binding.Source != "init" {
			return nil, restToolSafetyPolicy{}, fmt.Errorf("param_bindings[%d] source must be 'call' or 'init'", idx)
		}
		param, ok := available[binding.In+":"+binding.Name]
		if !ok {
			return nil, restToolSafetyPolicy{}, fmt.Errorf("param_bindings[%d] references unknown operation parameter %s:%s", idx, binding.In, binding.Name)
		}
		required := param.Required
		if binding.Required != nil {
			required = *binding.Required
		}
		out = append(out, compiledBinding{restToolParamBinding: binding, Required: required})
	}

	policy := restToolSafetyPolicy{}
	if len(safetyRaw) > 0 {
		if err := json.Unmarshal(safetyRaw, &policy); err != nil {
			return nil, restToolSafetyPolicy{}, fmt.Errorf("invalid safety_policy: %w", err)
		}
	}
	for i, host := range policy.AllowHosts {
		policy.AllowHosts[i] = strings.ToLower(strings.TrimSpace(host))
	}
	return out, policy, nil
}

func loadOpenAPIDocument(sourceType string, source string) (map[string]interface{}, error) {
	var data []byte
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	if sourceType == "url" {
		parsed, err := url.Parse(strings.TrimSpace(source))
		if err != nil {
			return nil, fmt.Errorf("invalid openapi source url: %w", err)
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return nil, fmt.Errorf("openapi source url must use http or https")
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(parsed.String())
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("failed to fetch openapi source: status %d", resp.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			return nil, err
		}
	} else {
		data = []byte(source)
	}

	doc := map[string]interface{}{}
	if err := json.Unmarshal(data, &doc); err == nil {
		return doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid openapi document: %w", err)
	}
	return doc, nil
}

func resolveOperation(doc map[string]interface{}, operationID, methodRaw, pathRaw string) (string, []operationParam, string, string, error) {
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok {
		return "", nil, "", "", fmt.Errorf("openapi document has no paths object")
	}

	findByOperationID := strings.TrimSpace(operationID) != ""
	method := strings.ToLower(strings.TrimSpace(methodRaw))
	path := strings.TrimSpace(pathRaw)

	selectedPath := ""
	selectedMethod := ""
	var selectedOp map[string]interface{}
	var pathItem map[string]interface{}

	for candidatePath, pathValue := range paths {
		pathMap, ok := pathValue.(map[string]interface{})
		if !ok {
			continue
		}
		for methodKey, opValue := range pathMap {
			opMap, ok := opValue.(map[string]interface{})
			if !ok {
				continue
			}
			methodKeyLower := strings.ToLower(strings.TrimSpace(methodKey))
			if findByOperationID {
				if opID, _ := opMap["operationId"].(string); opID == operationID {
					selectedPath = candidatePath
					selectedMethod = methodKeyLower
					selectedOp = opMap
					pathItem = pathMap
					break
				}
			} else if candidatePath == path && methodKeyLower == method {
				selectedPath = candidatePath
				selectedMethod = methodKeyLower
				selectedOp = opMap
				pathItem = pathMap
				break
			}
		}
		if selectedOp != nil {
			break
		}
	}
	if selectedOp == nil {
		if findByOperationID {
			return "", nil, "", "", fmt.Errorf("operationId %q not found", operationID)
		}
		return "", nil, "", "", fmt.Errorf("operation %s %s not found", method, path)
	}

	params := collectOperationParams(doc, pathItem, selectedOp)
	baseURL := resolveBaseURL(doc)
	return baseURL, params, strings.ToUpper(selectedMethod), selectedPath, nil
}

func collectOperationParams(doc map[string]interface{}, pathItem map[string]interface{}, operation map[string]interface{}) []operationParam {
	result := []operationParam{}
	seen := map[string]struct{}{}
	appendParams := func(raw interface{}) {
		rows, ok := raw.([]interface{})
		if !ok {
			return
		}
		for _, entry := range rows {
			paramMap, ok := normalizeRefObject(doc, entry)
			if !ok {
				continue
			}
			inValue, _ := paramMap["in"].(string)
			nameValue, _ := paramMap["name"].(string)
			if inValue == "" || nameValue == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(inValue)) + ":" + strings.TrimSpace(nameValue)
			if _, exists := seen[key]; exists {
				continue
			}
			required, _ := paramMap["required"].(bool)
			schema := resolveSchemaObject(doc, paramMap["schema"])
			description, _ := paramMap["description"].(string)
			result = append(result, operationParam{In: strings.ToLower(strings.TrimSpace(inValue)), Name: strings.TrimSpace(nameValue), Required: required, Description: description, Schema: schema})
			seen[key] = struct{}{}
		}
	}

	appendParams(pathItem["parameters"])
	appendParams(operation["parameters"])

	requestBody, ok := normalizeRefObject(doc, operation["requestBody"])
	if ok {
		required, _ := requestBody["required"].(bool)
		if content, ok := requestBody["content"].(map[string]interface{}); ok {
			if jsonContent, ok := content["application/json"].(map[string]interface{}); ok {
				schema := resolveSchemaObject(doc, jsonContent["schema"])
				if schema != nil {
					if props, ok := schema["properties"].(map[string]interface{}); ok {
						for propName, rawPropSchema := range props {
							propSchema := resolveSchemaObject(doc, rawPropSchema)
							isRequired := required
							if reqList, ok := schema["required"].([]interface{}); ok {
								isRequired = false
								for _, reqEntry := range reqList {
									if reqName, ok := reqEntry.(string); ok && reqName == propName {
										isRequired = true
										break
									}
								}
							}
							result = append(result, operationParam{In: "body", Name: propName, Required: isRequired, Schema: propSchema})
						}
					}
				}
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].In == result[j].In {
			return result[i].Name < result[j].Name
		}
		return result[i].In < result[j].In
	})
	return result
}

func resolveBaseURL(doc map[string]interface{}) string {
	servers, ok := doc["servers"].([]interface{})
	if !ok || len(servers) == 0 {
		return ""
	}
	first, ok := servers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	urlValue, _ := first["url"].(string)
	return strings.TrimSpace(urlValue)
}

func normalizeRefObject(doc map[string]interface{}, raw interface{}) (map[string]interface{}, bool) {
	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false
	}
	if ref, hasRef := obj["$ref"].(string); hasRef && ref != "" {
		resolved := resolveRef(doc, ref)
		if resolved == nil {
			return nil, false
		}
		return resolved, true
	}
	return obj, true
}

func resolveSchemaObject(doc map[string]interface{}, raw interface{}) map[string]interface{} {
	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	if ref, hasRef := obj["$ref"].(string); hasRef && ref != "" {
		resolved := resolveRef(doc, ref)
		if resolved == nil {
			return nil
		}
		return cloneMap(resolved)
	}
	return cloneMap(obj)
}

func resolveRef(doc map[string]interface{}, ref string) map[string]interface{} {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current interface{} = doc
	for _, segment := range segments {
		nextMap, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = nextMap[segment]
	}
	resolved, _ := current.(map[string]interface{})
	return resolved
}

func bindingSchema(params []operationParam, inValue, name string) map[string]interface{} {
	for _, p := range params {
		if p.In == inValue && p.Name == name {
			if p.Schema != nil {
				return p.Schema
			}
			break
		}
	}
	return nil
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return nil
	}
	encoded, _ := json.Marshal(input)
	cloned := map[string]interface{}{}
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func normalizeInterfaceMap(input interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	if asMap, ok := input.(map[string]interface{}); ok {
		return asMap
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return map[string]interface{}{}
	}
	result := map[string]interface{}{}
	_ = json.Unmarshal(encoded, &result)
	return result
}

func validateOutboundURL(baseURL string, policy restToolSafetyPolicy) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("openapi operation has no server url")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("unsupported URL scheme")
	}

	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname == "" {
		return fmt.Errorf("server url host is required")
	}
	if len(policy.AllowHosts) > 0 {
		allowed := false
		for _, host := range policy.AllowHosts {
			if host == hostname {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("host %q is not in allow_hosts", hostname)
		}
	}

	if policy.AllowPrivateIPs {
		return nil
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("host resolves to private address")
		}
		return nil
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("host resolves to private address")
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast()
}
