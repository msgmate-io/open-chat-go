package msgmate

import (
	tooldefs "backend/api/msgmate/tools"
	"context"
	"encoding/json"
	"strings"
	"sync"

	extiface "github.com/msgmate-io/go-tool-interface/toolinterface"
)

type Tool interface {
	Run(input string) (string, error)
	RunTool(input interface{}) (string, error)
	ParseArguments(input string) (interface{}, error)
	GetToolFunctionName() string
	GetToolDescription() string
	GetToolType() string
	GetToolName() string
	GetToolParameters() map[string]interface{}
	GetToolInputSchema() map[string]interface{}
	GetToolInitSchema() map[string]interface{}
	GetAdminOnly() bool
	GetRequiresInit() bool
	GetRequiresConfirmation() bool
	// ResolveConfirmation returns the call-time confirmation requirement. Tools
	// without a code-defined resolver fall back to GetRequiresConfirmation.
	// When confirmation is not required, a non-empty resolved result replaces
	// the tool's own Run output (e.g. an autonomous answer).
	ResolveConfirmation(input interface{}) (requiresConfirmation bool, resolvedResult string)
	GetStopOnFirstConfirmableToolCall() bool
	GetConfirmationBlockMessage() string
	ConstructTool() interface{}
	SetInitData(data interface{})
}

// ContextTool is the optional cancellation-aware extension of Tool. A tool that
// can honour a user stop (eg the native coder's workspace tools, which call
// back into the integration over HTTP) implements it; every other tool is
// executed through Tool.RunTool as before.
type ContextTool interface {
	RunToolContext(ctx context.Context, input interface{}) (string, error)
}

type ToolConstructor func() Tool

var (
	AllTools         []Tool
	toolConstructors = map[string]ToolConstructor{}
	toolAliases      = map[string]string{}
	externalToolDefs = map[string]struct{}{}
	toolNames        []string
	registryMu       sync.RWMutex
)

func init() {
	registerBuiltinTools()
	EnsureExternalToolsRegistered()
	// ci#680: `coder_context` reports the per-chat context/token budget. It is
	// registered only when no external integration already owns the name (the
	// coding integration may ship its own `coder_context`), so the core can
	// never panic on a duplicate constructor and never silently replace the
	// integration's tool.
	registerToolConstructorIfFree("coder_context", nil, NewCoderContextTool)
	// ci#724: `coder_schedule` lets a lead agent queue a later turn of its own
	// chat, so an autonomous loop needs no external CronJob. Same free-name
	// rule: an integration shipping its own `coder_schedule` keeps ownership.
	registerToolConstructorIfFree("coder_schedule", nil, NewCoderScheduleTool)
	// ci#809: `all_tasks_completed` is the explicit machine-readable end of a
	// task; the turn reader promotes its call into `task_completed` metadata.
	registerToolConstructorIfFree("all_tasks_completed", nil, NewAllTasksCompletedTool)
}

// registerToolConstructorIfFree registers a builtin constructor only when the
// tool name is still unowned (neither a builtin nor an external integration
// tool). It reports whether the constructor was registered.
func registerToolConstructorIfFree(name string, aliases []string, constructor ToolConstructor) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := toolConstructors[name]; exists {
		return false
	}
	if _, exists := toolAliases[name]; exists {
		return false
	}
	registerToolConstructorLocked(name, aliases, constructor)
	refreshAllToolsLocked()
	return true
}

func registerToolConstructor(name string, aliases []string, constructor ToolConstructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registerToolConstructorLocked(name, aliases, constructor)
	refreshAllToolsLocked()
}

func registerToolConstructorLocked(name string, aliases []string, constructor ToolConstructor) {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("tool name cannot be empty")
	}
	if constructor == nil {
		panic("tool constructor cannot be nil")
	}
	if _, exists := toolConstructors[name]; exists {
		panic("tool constructor already registered for: " + name)
	}

	toolConstructors[name] = constructor
	toolNames = append(toolNames, name)

	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, exists := toolConstructors[alias]; exists {
			panic("tool alias conflicts with registered tool name: " + alias)
		}
		toolAliases[alias] = name
	}
}

func registerExternalToolsLocked() {
	updated := false
	for _, externalDef := range extiface.List() {
		name := strings.TrimSpace(externalDef.Name)
		if name == "" {
			continue
		}
		if _, seen := externalToolDefs[name]; seen {
			continue
		}
		// A name already owned by a builtin constructor is never re-registered
		// from the external registry: registerToolConstructorLocked panics on a
		// duplicate, and an integration declaring a name the core already
		// implements (eg the ci#680 `coder_context`) must not crash every turn.
		if _, taken := toolConstructors[name]; taken {
			externalToolDefs[name] = struct{}{}
			continue
		}
		if _, taken := toolAliases[name]; taken {
			externalToolDefs[name] = struct{}{}
			continue
		}
		def := tooldefs.ToolDefinition{
			Name:                           name,
			FunctionName:                   externalDef.FunctionName,
			Description:                    externalDef.Description,
			AdminOnly:                      externalDef.AdminOnly,
			RequiresInit:                   externalDef.RequiresInit,
			InitSchema:                     externalDef.InitSchema,
			RequiresConfirmation:           externalDef.RequiresConfirmation,
			StopOnFirstConfirmableToolCall: externalDef.StopOnFirstConfirmableToolCall,
			ConfirmationBlockMessage:       externalDef.ConfirmationBlockMessage,
			InputType:                      externalDef.InputType,
			RequiredParams:                 externalDef.RequiredParams,
			Parameters:                     externalDef.Parameters,
			RunFunction:                    externalDef.Run,
			ResolveConfirmation:            externalDef.ResolveConfirmation,
		}
		defCopy := def

		registerToolConstructorLocked(defCopy.Name, nil, func() Tool {
			return NewToolFromDefinition(defCopy)
		})
		externalToolDefs[name] = struct{}{}
		updated = true
	}

	if updated {
		refreshAllToolsLocked()
	}
}

func EnsureExternalToolsRegistered() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registerExternalToolsLocked()
}

func refreshAllTools() {
	registryMu.Lock()
	defer registryMu.Unlock()
	refreshAllToolsLocked()
}

func refreshAllToolsLocked() {
	AllTools = make([]Tool, 0, len(toolNames))
	for _, name := range toolNames {
		if constructor, exists := toolConstructors[name]; exists {
			AllTools = append(AllTools, constructor())
		}
	}
}

// NewToolByName maps tool names to their constructor functions
func NewToolByName(name string) (Tool, bool) {
	EnsureExternalToolsRegistered()

	registryMu.RLock()
	defer registryMu.RUnlock()
	name = strings.TrimSpace(name)
	if constructor, found := toolConstructors[name]; found {
		return constructor(), true
	}
	if canonical, found := toolAliases[name]; found {
		if constructor, exists := toolConstructors[canonical]; exists {
			return constructor(), true
		}
	}
	return nil, false
}

func GetNewToolInstanceByName(name string, initData map[string]interface{}) Tool {
	// Create a new instance using the constructor function
	newTool, found := NewToolByName(name)
	if !found {
		return nil
	}

	// Set init data if provided
	if newTool.GetRequiresInit() {
		newTool.SetInitData(initData)
	}

	return newTool
}

type BaseTool struct {
	AdminOnly                      bool
	RequiresInit                   bool
	RequiresConfirmation           bool
	StopOnFirstConfirmableToolCall bool
	ConfirmationBlockMessage       string
	ToolName                       string
	ToolFunctionName               string
	ToolType                       string
	ToolDescription                string
	ToolInput                      interface{}
	ToolInputSchema                map[string]interface{}
	ToolInitSchema                 map[string]interface{}
	ToolInit                       interface{}
	RequiredParams                 []string
	Parameters                     map[string]interface{}
}

func (t *BaseTool) ConstructTool() interface{} {
	return map[string]interface{}{
		"type": t.ToolType,
		"function": map[string]interface{}{
			"name":        t.GetToolFunctionName(),
			"description": t.GetToolDescription(),
			"parameters":  t.GetToolInputSchema(),
		},
	}
}

func (t *BaseTool) RunTool(input interface{}) (string, error) {
	return "", nil // User must overrite this otherwise tool ain't doing anything
}

func (t *BaseTool) ParseArguments(input string) (interface{}, error) {
	toolInput := t.ToolInput
	err := json.Unmarshal([]byte(input), &toolInput)
	if err != nil {
		return nil, err
	}
	return toolInput, nil
}

func (t *BaseTool) GetRequiresInit() bool {
	return t.RequiresInit
}

func (t *BaseTool) GetRequiresConfirmation() bool {
	return t.RequiresConfirmation
}

// ResolveConfirmation is the default confirmation resolution: it mirrors the
// static RequiresConfirmation flag and never auto-resolves. Tools that need a
// call-time decision (eg the coder_ask autonomous answer) override it.
func (t *BaseTool) ResolveConfirmation(input interface{}) (bool, string) {
	return t.RequiresConfirmation, ""
}

func (t *BaseTool) GetStopOnFirstConfirmableToolCall() bool {
	return t.StopOnFirstConfirmableToolCall
}

func (t *BaseTool) GetConfirmationBlockMessage() string {
	return t.ConfirmationBlockMessage
}

func (t *BaseTool) GetAdminOnly() bool {
	return t.AdminOnly
}

func (t *BaseTool) Run(input string) (string, error) {
	toolInput := t.ToolInput
	err := json.Unmarshal([]byte(input), &toolInput)
	if err != nil {
		return "", err
	}
	res, err := t.RunTool(toolInput)
	if err != nil {
		return "", err
	}
	return res, nil
}

func (t *BaseTool) GetToolName() string {
	return t.ToolName
}

func (t *BaseTool) GetToolType() string {
	return t.ToolType
}

func (t *BaseTool) GetToolFunctionName() string {
	if t.ToolFunctionName != "" {
		return t.ToolFunctionName
	}
	return t.ToolName
}

func (t *BaseTool) GetToolDescription() string {
	return t.ToolDescription
}

func (t *BaseTool) GetToolParameters() map[string]interface{} {
	return t.Parameters
}

func (t *BaseTool) GetToolInputSchema() map[string]interface{} {
	var schema map[string]interface{}
	if t.ToolInputSchema != nil {
		schema = normalizeToolInputSchema(t.ToolInputSchema)
	} else {
		schema = normalizeToolInputSchema(map[string]interface{}{
			"type":        "object",
			"properties":  t.GetToolParameters(),
			"required":    t.RequiredParams,
			"description": "The parameters for the tool",
		})
	}
	// ci#723: only the coding integration's tools advertise the optional
	// `reason` argument, so a coder turn can narrate why each tool runs while
	// every other tool keeps its previous schema.
	if IsCoderToolName(t.ToolName) {
		schema = withToolCallReasonProperty(schema)
	}
	return schema
}

// normalizeToolInputSchema guarantees a JSON Schema that strict providers
// accept. OpenRouter rejects a schema whose `"required"` is `null` (as emitted
// when a tool has no RequiredParams) with
// `tools.N.function.parameters/required: invalid JSON Schema`, which would
// otherwise break every coder turn; DeepInfra tolerates it, so the bug only
// surfaced on the OpenRouter-backed suites. `"properties"` is likewise coerced
// to an object (never null) and non-string `required` entries are dropped.
func normalizeToolInputSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		schema = map[string]interface{}{}
	}
	if _, ok := schema["type"].(string); !ok {
		schema["type"] = "object"
	}
	if properties, ok := schema["properties"].(map[string]interface{}); !ok || properties == nil {
		schema["properties"] = map[string]interface{}{}
	}
	switch required := schema["required"].(type) {
	case []string:
		if required == nil {
			schema["required"] = []string{}
		}
	case []interface{}:
		names := make([]string, 0, len(required))
		for _, raw := range required {
			if name, ok := raw.(string); ok {
				names = append(names, name)
			}
		}
		schema["required"] = names
	default:
		schema["required"] = []string{}
	}
	return schema
}

func (t *BaseTool) GetToolInitSchema() map[string]interface{} {
	return t.ToolInitSchema
}

func (t *BaseTool) GetRequiredParams() []string {
	return t.RequiredParams
}

func (t *BaseTool) SetInitData(data interface{}) {
	t.ToolInit = data
}
