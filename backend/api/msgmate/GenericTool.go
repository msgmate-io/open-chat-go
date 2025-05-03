package msgmate

import (
	tooldefs "backend/api/msgmate/tools"
	"encoding/json"
	"fmt"
	"reflect"
)

type ToolDefinition = tooldefs.ToolDefinition

// GenericTool is a simplified tool implementation that uses the ToolDefinition
type GenericTool struct {
	BaseTool
	Definition ToolDefinition
}

func (t *GenericTool) RunTool(input interface{}) (string, error) {
	initData, ok := t.ToolInit.(map[string]interface{})
	if !ok || initData == nil {
		initData = map[string]interface{}{}
	}
	return t.Definition.RunFunction(input, initData)
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
	} else if inputType.Kind() == reflect.Map {
		newValue := reflect.MakeMap(inputType)
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
	if inputType.Kind() == reflect.Map {
		return inputTypeValue, nil
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
		AdminOnly:                      def.AdminOnly,
		RequiresInit:                   def.RequiresInit,
		RequiresConfirmation:           def.RequiresConfirmation,
		StopOnFirstConfirmableToolCall: def.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:       def.ConfirmationBlockMessage,
		ToolName:                       def.Name,
		ToolFunctionName:               def.FunctionName,
		ToolType:                       "function",
		ToolDescription:                def.Description,
		ToolInput:                      def.InputType,
		ToolInputSchema:                def.InputSchema,
		ToolInitSchema:                 def.InitSchema,
		ToolInit:                       interface{}(nil),
		RequiredParams:                 def.RequiredParams,
		Parameters:                     def.Parameters,
	}
	return tool
}
