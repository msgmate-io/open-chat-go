package msgmate

import (
	"encoding/json"
	"math/rand"
	"strconv"
	"time"
)

// ---- Weather tool ----------

type WeatherTool struct {
	BaseTool
}

type WeatherToolInput struct {
	Location string `json:"location"`
	Unit     string `json:"unit"`
}

func (t *WeatherTool) RunTool(input interface{}) (string, error) {
	// time.Sleep(2 * time.Second)
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

func NewWeatherTool() Tool {
	weatherTool := &WeatherTool{}
	weatherTool.BaseTool = BaseTool{
		ToolName:        "get_weather",
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

// ----- Current Time tool ----------

type CurrentTimeTool struct {
	BaseTool
}

func (t *CurrentTimeTool) RunTool(input interface{}) (string, error) {
	return time.Now().Format(time.RFC3339), nil
}

func NewCurrentTimeTool() Tool {
	currentTimeTool := &CurrentTimeTool{}
	currentTimeTool.BaseTool = BaseTool{
		ToolName:        "get_current_time",
		ToolType:        "function",
		ToolInput:       interface{}(nil),
		ToolDescription: "Return the current time",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return currentTimeTool
}
