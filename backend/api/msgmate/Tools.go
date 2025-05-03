package msgmate

import (
	_ "backend/api/msgmate/externaltools"
	tooldefs "backend/api/msgmate/tools"
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
	GetStopOnFirstConfirmableToolCall() bool
	GetConfirmationBlockMessage() string
	ConstructTool() interface{}
	SetInitData(data interface{})
}

type ToolConstructor func() Tool

var (
	AllTools         []Tool
	toolConstructors = map[string]ToolConstructor{}
	toolAliases      = map[string]string{}
	toolNames        []string
	registryMu       sync.RWMutex
)

func init() {
	registerBuiltinTools()
	registerExternalTools()
	refreshAllTools()
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

func registerExternalTools() {
	for _, externalDef := range extiface.List() {
		def := tooldefs.ToolDefinition{
			Name:                           externalDef.Name,
			FunctionName:                   externalDef.FunctionName,
			Description:                    externalDef.Description,
			AdminOnly:                      externalDef.AdminOnly,
			RequiresInit:                   externalDef.RequiresInit,
			RequiresConfirmation:           externalDef.RequiresConfirmation,
			StopOnFirstConfirmableToolCall: externalDef.StopOnFirstConfirmableToolCall,
			ConfirmationBlockMessage:       externalDef.ConfirmationBlockMessage,
			InputType:                      externalDef.InputType,
			RequiredParams:                 externalDef.RequiredParams,
			Parameters:                     externalDef.Parameters,
			RunFunction:                    externalDef.Run,
		}

		registerToolConstructor(def.Name, nil, func() Tool {
			return NewToolFromDefinition(def)
		})
	}
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
	if t.ToolInputSchema != nil {
		return t.ToolInputSchema
	}

	return map[string]interface{}{
		"type":        "object",
		"properties":  t.GetToolParameters(),
		"required":    t.RequiredParams,
		"description": "The parameters for the tool",
	}
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
