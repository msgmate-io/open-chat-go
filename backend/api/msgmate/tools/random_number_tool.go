package tools

import (
	"fmt"
	"math/rand"
	"strconv"
)

type RandomNumberToolInput struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

var RandomNumberToolDef = ToolDefinition{
	Name:           "get_random_number",
	Description:    "Generate a random number within a specified range.",
	RequiresInit:   false,
	InputType:      RandomNumberToolInput{},
	RequiredParams: []string{"min", "max"},
	Parameters: map[string]interface{}{
		"min": map[string]interface{}{"type": "integer", "description": "The minimum value (inclusive)"},
		"max": map[string]interface{}{"type": "integer", "description": "The maximum value (inclusive)"},
	},
	RunFunction: func(input interface{}, _ map[string]interface{}) (string, error) {
		toolInput := input.(RandomNumberToolInput)
		if toolInput.Min >= toolInput.Max {
			return "", fmt.Errorf("min must be less than max")
		}
		return strconv.Itoa(rand.Intn(toolInput.Max-toolInput.Min+1) + toolInput.Min), nil
	},
}

var RandomNumberSeededToolDef = ToolDefinition{
	Name:         "get_random_number_seeded",
	Description:  "Generate a deterministic random number within a specified range using a seed.",
	RequiresInit: true,
	InitSchema: map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"seed"},
		"properties": map[string]interface{}{
			"seed": map[string]interface{}{"type": "integer", "description": "Integer seed used to initialize deterministic random generation"},
		},
	},
	InputType:      RandomNumberToolInput{},
	RequiredParams: []string{"min", "max"},
	Parameters: map[string]interface{}{
		"min": map[string]interface{}{"type": "integer", "description": "The minimum value (inclusive)"},
		"max": map[string]interface{}{"type": "integer", "description": "The maximum value (inclusive)"},
	},
	RunFunction: func(input interface{}, init map[string]interface{}) (string, error) {
		toolInput := input.(RandomNumberToolInput)
		if toolInput.Min >= toolInput.Max {
			return "", fmt.Errorf("min must be less than max")
		}

		rawSeed, exists := init["seed"]
		if !exists {
			return "", fmt.Errorf("missing required tool_init key: seed")
		}

		seedFloat, ok := rawSeed.(float64)
		if !ok {
			return "", fmt.Errorf("tool_init key seed must be an integer")
		}
		if float64(int64(seedFloat)) != seedFloat {
			return "", fmt.Errorf("tool_init key seed must be an integer")
		}

		rng := rand.New(rand.NewSource(int64(seedFloat)))
		return strconv.Itoa(rng.Intn(toolInput.Max-toolInput.Min+1) + toolInput.Min), nil
	},
}
