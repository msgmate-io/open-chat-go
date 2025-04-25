package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

type ToolTypeDefinition struct {
	Name                   string                 `json:"name"`
	FunctionName           string                 `json:"function_name"`
	Type                   string                 `json:"type"`
	RequiresInit           bool                   `json:"requires_init"`
	RequiresConfirmation   bool                   `json:"requires_confirmation"`
	CallTypeName           string                 `json:"call_type_name"`
	InitTypeName           string                 `json:"init_type_name,omitempty"`
	CallSchema             map[string]interface{} `json:"call_schema"`
	InitSchema             map[string]interface{} `json:"init_schema,omitempty"`
	ToolInitConfigPathHint string                 `json:"tool_init_config_path_hint,omitempty"`
}

type ToolsTypingResponse struct {
	Rows []ToolTypeDefinition `json:"rows"`
}

type ToolValidatePayloadResponse struct {
	Valid    bool   `json:"valid"`
	ToolName string `json:"tool_name"`
	Kind     string `json:"kind"`
}

func visibleToolsForUser(user *database.User) []msgmate.Tool {
	isAdmin := user != nil && user.IsAdmin
	rows := make([]msgmate.Tool, 0, len(msgmate.AllTools))
	for _, tool := range msgmate.AllTools {
		if tool.GetAdminOnly() && !isAdmin {
			continue
		}
		rows = append(rows, tool)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].GetToolName() < rows[j].GetToolName()
	})
	return rows
}

func cloneSchemaMap(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil
	}
	return cloned
}

func toPascalIdentifier(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "Tool"
	}

	segments := strings.FieldsFunc(raw, func(r rune) bool {
		return !(unicode.IsDigit(r) || unicode.IsLetter(r))
	})
	if len(segments) == 0 {
		return "Tool"
	}

	var builder strings.Builder
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		runes := []rune(strings.ToLower(segment))
		runes[0] = unicode.ToUpper(runes[0])
		builder.WriteString(string(runes))
	}
	name := builder.String()
	if name == "" {
		return "Tool"
	}
	if unicode.IsDigit([]rune(name)[0]) {
		return "Tool" + name
	}
	return name
}

func addSchemaTitle(schema map[string]interface{}, title string) map[string]interface{} {
	cloned := cloneSchemaMap(schema)
	if cloned == nil {
		return map[string]interface{}{"type": "object", "title": title}
	}
	if existing, ok := cloned["title"].(string); ok && strings.TrimSpace(existing) != "" {
		return cloned
	}
	cloned["title"] = title
	return cloned
}

func toToolTypeDefinition(tool msgmate.Tool) ToolTypeDefinition {
	baseName := toPascalIdentifier(tool.GetToolName())
	callTypeName := baseName + "Call"
	initTypeName := ""
	if tool.GetRequiresInit() {
		initTypeName = baseName + "Init"
	}

	callSchema := addSchemaTitle(tool.GetToolInputSchema(), callTypeName)
	var initSchema map[string]interface{}
	if tool.GetRequiresInit() {
		resolved := tool.GetToolInitSchema()
		if resolved == nil {
			resolved = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
				"required":   []string{},
			}
		}
		initSchema = addSchemaTitle(resolved, initTypeName)
	}

	def := ToolTypeDefinition{
		Name:                 tool.GetToolName(),
		FunctionName:         tool.GetToolFunctionName(),
		Type:                 strings.TrimSpace(tool.GetToolType()),
		RequiresInit:         tool.GetRequiresInit(),
		RequiresConfirmation: tool.GetRequiresConfirmation(),
		CallTypeName:         callTypeName,
		InitTypeName:         initTypeName,
		CallSchema:           callSchema,
		InitSchema:           initSchema,
	}
	if def.RequiresInit {
		def.ToolInitConfigPathHint = "shared_config.tool_init." + def.Name
	}
	return def
}

func toolSchemaByKind(tool msgmate.Tool, kind string) (map[string]interface{}, error) {
	switch kind {
	case "call":
		return tool.GetToolInputSchema(), nil
	case "init":
		if !tool.GetRequiresInit() {
			return nil, fmt.Errorf("tool does not require init")
		}
		if schema := tool.GetToolInitSchema(); schema != nil {
			return schema, nil
		}
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported kind")
	}
}

func validateType(value interface{}, schemaType string) bool {
	switch schemaType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float64, float32, int, int32, int64, uint, uint32, uint64:
			return true
		default:
			return false
		}
	case "integer":
		_, ok := value.(float64)
		if !ok {
			return false
		}
		f := value.(float64)
		return float64(int64(f)) == f
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	default:
		return true
	}
}

func validatePayloadAgainstSchema(payload map[string]interface{}, schema map[string]interface{}) error {
	if schema == nil {
		return nil
	}

	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return fmt.Errorf("unsupported top-level schema type %q", schemaType)
	}

	requiredRaw, _ := schema["required"].([]interface{})
	required := make(map[string]struct{}, len(requiredRaw))
	for _, field := range requiredRaw {
		if name, ok := field.(string); ok && strings.TrimSpace(name) != "" {
			required[name] = struct{}{}
		}
	}

	properties, _ := schema["properties"].(map[string]interface{})
	for field := range required {
		if _, ok := payload[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}

	for field, value := range payload {
		prop, known := properties[field]
		if !known {
			if allowAdditional, ok := schema["additionalProperties"].(bool); ok && !allowAdditional {
				return fmt.Errorf("field %q is not allowed", field)
			}
			continue
		}

		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}
		enumValues, hasEnum := propMap["enum"].([]interface{})
		if hasEnum && len(enumValues) > 0 {
			matched := false
			for _, allowed := range enumValues {
				if fmt.Sprintf("%v", allowed) == fmt.Sprintf("%v", value) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("field %q has invalid enum value", field)
			}
		}

		if schemaType, ok := propMap["type"].(string); ok && !validateType(value, schemaType) {
			return fmt.Errorf("field %q has invalid type", field)
		}
	}

	return nil
}

func decodePayloadMap(r *http.Request) (map[string]interface{}, error) {
	defer r.Body.Close()
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return payload, nil
}

func findToolByName(name string) msgmate.Tool {
	for _, tool := range msgmate.AllTools {
		if tool.GetToolName() == name {
			return tool
		}
	}
	return nil
}

// ListTyping returns tool call/init schemas in a type-generation friendly format.
//
//	@Summary		List tool typing schemas
//	@Description	List visible tools with JSON Schemas for call payload and init payload so SDKs can auto-generate typed helpers.
//	@Tags			tools
//	@Produce		json
//	@Success		200 {object} tools.ToolsTypingResponse
//	@Router			/api/v1/tools/typing [get]
func (h *ToolsHandler) ListTyping(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value("user").(*database.User)
	visible := visibleToolsForUser(user)

	rows := make([]ToolTypeDefinition, 0, len(visible))
	for _, tool := range visible {
		rows = append(rows, toToolTypeDefinition(tool))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ToolsTypingResponse{Rows: rows})
}

// GetTyping returns one tool's call/init schemas by tool name.
//
//	@Summary		Get tool typing schema
//	@Description	Get a single visible tool typing schema by tool name.
//	@Tags			tools
//	@Produce		json
//	@Param			tool_name path string true "Tool name"
//	@Success		200 {object} tools.ToolTypeDefinition
//	@Failure		404 {string} string "Tool not found"
//	@Router			/api/v1/tools/{tool_name}/typing [get]
func (h *ToolsHandler) GetTyping(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value("user").(*database.User)
	toolName := strings.TrimSpace(r.PathValue("tool_name"))
	if toolName == "" {
		http.Error(w, "Tool not found", http.StatusNotFound)
		return
	}

	for _, tool := range visibleToolsForUser(user) {
		if tool.GetToolName() != toolName {
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toToolTypeDefinition(tool))
		return
	}

	http.Error(w, "Tool not found", http.StatusNotFound)
}

// ValidateCallPayload validates one tool's call payload against its JSON schema.
//
//	@Summary		Validate tool call payload
//	@Description	Validate a JSON object payload for a tool call by tool name.
//	@Tags			tools
//	@Accept			json
//	@Produce		json
//	@Param			tool_name path string true "Tool name"
//	@Param			payload body map[string]interface{} true "Tool call payload"
//	@Success		200 {object} tools.ToolValidatePayloadResponse
//	@Failure		400 {string} string "Invalid payload"
//	@Failure		404 {string} string "Tool not found"
//	@Router			/api/v1/tools/typing/{tool_name}/call/validate [post]
func (h *ToolsHandler) ValidateCallPayload(w http.ResponseWriter, r *http.Request) {
	h.validateToolPayloadByKind(w, r, "call")
}

// ValidateInitPayload validates one tool's init payload against its JSON schema.
//
//	@Summary		Validate tool init payload
//	@Description	Validate a JSON object payload for a tool init config by tool name.
//	@Tags			tools
//	@Accept			json
//	@Produce		json
//	@Param			tool_name path string true "Tool name"
//	@Param			payload body map[string]interface{} true "Tool init payload"
//	@Success		200 {object} tools.ToolValidatePayloadResponse
//	@Failure		400 {string} string "Invalid payload"
//	@Failure		404 {string} string "Tool not found"
//	@Router			/api/v1/tools/typing/{tool_name}/init/validate [post]
func (h *ToolsHandler) ValidateInitPayload(w http.ResponseWriter, r *http.Request) {
	h.validateToolPayloadByKind(w, r, "init")
}

func (h *ToolsHandler) validateToolPayloadByKind(w http.ResponseWriter, r *http.Request, kind string) {
	toolName := strings.TrimSpace(r.PathValue("tool_name"))
	if toolName == "" {
		http.Error(w, "Tool not found", http.StatusNotFound)
		return
	}
	tool := findToolByName(toolName)
	if tool == nil {
		http.Error(w, "Tool not found", http.StatusNotFound)
		return
	}

	payload, err := decodePayloadMap(r)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	schema, err := toolSchemaByKind(tool, kind)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := validatePayloadAgainstSchema(payload, schema); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ToolValidatePayloadResponse{
		Valid:    true,
		ToolName: toolName,
		Kind:     kind,
	})
}
