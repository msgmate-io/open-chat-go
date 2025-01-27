package msgmate

import (
	"encoding/json"
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
	GetRequiresInit() bool
	ConstructTool() interface{}
	SetInitData(data interface{})
}

var AllTools = []Tool{
	NewWeatherTool(),
	NewCurrentTimeTool(),
	NewRandomNumberTool(),
	NewLittleWorldChatReplyTool(),
	NewLittleWorldGetUserStateTool(),
	NewLittleWorldSetUserSearchingStateTool(),
	NewLittleWorldGetPastMessagesWithUserTool(),
}

type BaseTool struct {
	RequiresInit    bool
	ToolName        string
	ToolType        string
	ToolDescription string
	ToolInput       interface{}
	ToolInit        interface{}
	RequiredParams  []string
	Parameters      map[string]interface{}
}

func (t *BaseTool) ConstructTool() interface{} {
	return map[string]interface{}{
		"type": t.ToolType,
		"function": map[string]interface{}{
			"name":        t.GetToolFunctionName(),
			"description": t.GetToolDescription(),
			"parameters": map[string]interface{}{
				"type":        "object",
				"properties":  t.GetToolParameters(),
				"required":    t.RequiredParams,
				"description": "The parameters for the tool",
			},
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
	return t.ToolName
}

func (t *BaseTool) GetToolDescription() string {
	return t.ToolDescription
}

func (t *BaseTool) GetToolParameters() map[string]interface{} {
	return t.Parameters
}

func (t *BaseTool) GetRequiredParams() []string {
	return t.RequiredParams
}

func (t *BaseTool) SetInitData(data interface{}) {
	t.ToolInit = data
}
