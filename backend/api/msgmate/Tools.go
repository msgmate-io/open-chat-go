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
	NewLittleWorldRetrieveMatchOverviewTool(),
	NewLittleWorldResolveMatchTool(),
	NewRWTHAachenSeminarTimsAutoPaperIncludeExcludeAgent(),
	NewSignalSendMessageTool(),
	NewSignalReadPastMessagesTool(),
	NewSignalRemoveFromWhitelistTool(),
	NewSignalAddToWhitelistTool(),
	NewSignalGetWhitelistTool(),
	NewSignalShowTypingIndicatorTool(),
	NewRunCallbackFunctionTool(),
}

// NewToolByName maps tool names to their constructor functions
func NewToolByName(name string) (Tool, bool) {
	switch name {
	case "get_weather":
		return NewWeatherTool(), true
	case "get_current_time":
		return NewCurrentTimeTool(), true
	case "get_random_number":
		return NewRandomNumberTool(), true
	case "little_world__chat_reply":
		return NewLittleWorldChatReplyTool(), true
	case "little_world__get_user_state":
		return NewLittleWorldGetUserStateTool(), true
	case "little_world__set_user_searching_state":
		return NewLittleWorldSetUserSearchingStateTool(), true
	case "little_world__get_past_messages":
		return NewLittleWorldGetPastMessagesWithUserTool(), true
	case "little_world__retrieve_match_overview":
		return NewLittleWorldRetrieveMatchOverviewTool(), true
	case "little_world__resolve_match":
		return NewLittleWorldResolveMatchTool(), true
	case "rwth_aachen_seminar_tims_auto_paper_include_exclude_agent":
		return NewRWTHAachenSeminarTimsAutoPaperIncludeExcludeAgent(), true
	case "signal_send_message":
		return NewSignalSendMessageTool(), true
	case "signal_read_past_messages":
		return NewSignalReadPastMessagesTool(), true
	case "signal_remove_from_whitelist":
		return NewSignalRemoveFromWhitelistTool(), true
	case "signal_add_to_whitelist":
		return NewSignalAddToWhitelistTool(), true
	case "signal_get_whitelist":
		return NewSignalGetWhitelistTool(), true
	case "signal_show_typing_indicator":
		return NewSignalShowTypingIndicatorTool(), true
	case "run_callback_function":
		return NewRunCallbackFunctionTool(), true
	default:
		return nil, false
	}
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
