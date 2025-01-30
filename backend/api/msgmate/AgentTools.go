package msgmate

import (
	"encoding/json"
	"fmt"
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
		RequiresInit:    false,
		ToolName:        "get_weather",
		ToolType:        "function",
		ToolInput:       WeatherToolInput{},
		ToolInit:        interface{}(nil),
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
		RequiresInit:    false,
		ToolName:        "get_current_time",
		ToolType:        "function",
		ToolInput:       interface{}(nil),
		ToolInit:        interface{}(nil),
		ToolDescription: "Return the current time",
		RequiredParams:  []string{},
		Parameters:      map[string]interface{}{},
	}
	return currentTimeTool
}

// ----- Random Number tool ----------

type RandomNumberTool struct {
	BaseTool
}

type RandomNumberToolInput struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (t *RandomNumberTool) RunTool(input interface{}) (string, error) {
	var randomInput RandomNumberToolInput = input.(RandomNumberToolInput)

	// Ensure min is less than max
	if randomInput.Min >= randomInput.Max {
		return "", fmt.Errorf("min must be less than max")
	}

	// Generate random number in range [min, max]
	randomNum := rand.Intn(randomInput.Max-randomInput.Min+1) + randomInput.Min

	return strconv.Itoa(randomNum), nil
}

func (t *RandomNumberTool) ParseArguments(input string) (interface{}, error) {
	var randomInput RandomNumberToolInput
	err := json.Unmarshal([]byte(input), &randomInput)
	if err != nil {
		return nil, err
	}
	return randomInput, nil
}

func NewRandomNumberTool() Tool {
	randomNumberTool := &RandomNumberTool{}
	randomNumberTool.BaseTool = BaseTool{
		RequiresInit:    false,
		ToolName:        "get_random_number",
		ToolType:        "function",
		ToolInput:       RandomNumberToolInput{},
		ToolInit:        interface{}(nil),
		ToolDescription: "Generate a random number within a specified range",
		RequiredParams:  []string{"min", "max"},
		Parameters: map[string]interface{}{
			"min": map[string]interface{}{
				"type":        "integer",
				"description": "The minimum value (inclusive)",
			},
			"max": map[string]interface{}{
				"type":        "integer",
				"description": "The maximum value (inclusive)",
			},
		},
	}
	return randomNumberTool
}
