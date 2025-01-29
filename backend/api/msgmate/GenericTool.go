package msgmate

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// ToolDefinition represents a complete tool definition in a single JSON-friendly structure
type ToolDefinition struct {
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	RequiresInit   bool                   `json:"requires_init"`
	InputType      interface{}            `json:"input_type"`
	RequiredParams []string               `json:"required_params"`
	Parameters     map[string]interface{} `json:"parameters"`
	RunFunction    func(input interface{}, init map[string]interface{}) (string, error)
}

// GenericTool is a simplified tool implementation that uses the ToolDefinition
type GenericTool struct {
	BaseTool
	Definition ToolDefinition
}

func (t *GenericTool) RunTool(input interface{}) (string, error) {
	return t.Definition.RunFunction(input, t.ToolInit.(map[string]interface{}))
}

func (t *GenericTool) ParseArguments(input string) (interface{}, error) {
	// Get the type of the input
	inputType := reflect.TypeOf(t.Definition.InputType)

	// Create a new instance based on whether it's a struct or pointer
	var inputTypeValue interface{}
	if inputType.Kind() == reflect.Struct {
		// For struct types, create a new instance directly
		newValue := reflect.New(inputType).Elem()
		inputTypeValue = newValue.Addr().Interface()
	} else if inputType.Kind() == reflect.Ptr {
		// For pointer types, create a new instance of the pointed-to type
		newValue := reflect.New(inputType.Elem())
		inputTypeValue = newValue.Interface()
	} else {
		return nil, fmt.Errorf("unsupported input type: %v", inputType.Kind())
	}

	// Unmarshal the JSON into the new instance
	err := json.Unmarshal([]byte(input), &inputTypeValue)
	if err != nil {
		return nil, err
	}

	// If it's a struct type, we need to dereference the pointer
	if inputType.Kind() == reflect.Struct {
		return reflect.ValueOf(inputTypeValue).Elem().Interface(), nil
	}

	// For pointer types, return the dereferenced value
	return reflect.ValueOf(inputTypeValue).Elem().Interface(), nil
}

// NewToolFromDefinition creates a new tool from a ToolDefinition
func NewToolFromDefinition(def ToolDefinition) Tool {
	tool := &GenericTool{
		Definition: def,
	}
	tool.BaseTool = BaseTool{
		RequiresInit:    def.RequiresInit,
		ToolName:        def.Name,
		ToolType:        "function",
		ToolDescription: def.Description,
		ToolInput:       def.InputType,
		ToolInit:        interface{}(nil),
		RequiredParams:  def.RequiredParams,
		Parameters:      def.Parameters,
	}
	return tool
}
