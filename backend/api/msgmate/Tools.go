package msgmate

import (
	"encoding/json"
	"math/rand"
	"strconv"
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
	ConstructTool() interface{}
}

type BaseTool struct {
	ToolName        string
	ToolType        string
	ToolDescription string
	ToolInput       interface{}
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

type WeatherTool struct {
	BaseTool
}

type WeatherToolInput struct {
	Location string `json:"location"`
	Unit     string `json:"unit"`
}

func (t *WeatherTool) RunTool(input interface{}) (string, error) {
	var weatherToolInput WeatherToolInput = input.(WeatherToolInput)
	return "The temperature in " + weatherToolInput.Location + " is " + strconv.Itoa(rand.Intn(100)) + " " + weatherToolInput.Unit, nil
}

func (t *WeatherTool) ParseArguments(input string) (interface{}, error) {
	var weatherInput WeatherToolInput
	err := json.Unmarshal([]byte(input), &weatherInput)
	if err != nil {
		return nil, err
	}
	return weatherInput, nil
}

type ToolManager struct {
	Tools []Tool
}

func NewWeatherTool() Tool {
	weatherTool := &WeatherTool{}
	weatherTool.BaseTool = BaseTool{
		ToolName:        "WeatherTool",
		ToolType:        "function",
		ToolInput:       WeatherToolInput{},
		ToolDescription: "Return the temperature of the specified region specified by the user",
		RequiredParams:  []string{"location", "unit"},
		Parameters: map[string]interface{}{
			"location": map[string]interface{}{
				"type":        "string",
				"description": "The location to get weather for",
			},
			"unit": map[string]interface{}{
				"type":        "string",
				"description": "The unit of temperature (C or F)",
			},
		},
	}
	return weatherTool
}
