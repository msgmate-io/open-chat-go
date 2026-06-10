package tools

import (
	"math/rand"
	"strconv"
)

type WeatherToolInput struct {
	Location string `json:"location"`
	Unit     string `json:"unit"`
}

var WeatherToolDef = ToolDefinition{
	Name:           "get_weather",
	Description:    "Return the temperature for a user-specified location.",
	RequiresInit:   false,
	InputType:      WeatherToolInput{},
	RequiredParams: []string{"location", "unit"},
	Parameters: map[string]interface{}{
		"location": map[string]interface{}{"type": "string", "description": "The location to get weather for"},
		"unit":     map[string]interface{}{"type": "string", "description": "The unit of temperature (C or F)"},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		toolInput := input.(WeatherToolInput)
		return "The temperature in " + toolInput.Location + " is " + strconv.Itoa(rand.Intn(100)) + " " + toolInput.Unit, nil
	},
}
