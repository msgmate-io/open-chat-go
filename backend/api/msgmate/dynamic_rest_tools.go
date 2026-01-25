package msgmate

import (
	tooldefs "backend/api/msgmate/tools"
	"backend/database"

	"github.com/msgmate-io/go-tool-interface/toolinterface"
	restapitoolintegration "github.com/msgmate-io/rest-api-tool-integration"
	"gorm.io/gorm"
)

func ResolveUserDynamicRESTToolByName(db *gorm.DB, ownerUserID uint, toolName string) (*database.DynamicRESTTool, error) {
	row, err := restapitoolintegration.ResolveUserDynamicRESTToolByName(db, ownerUserID, toolName)
	if err != nil {
		return nil, err
	}
	converted := fromIntegrationDynamicRESTTool(*row)
	return &converted, nil
}

func BuildDynamicRESTToolSnapshot(row database.DynamicRESTTool) map[string]interface{} {
	return restapitoolintegration.BuildDynamicRESTToolSnapshot(toIntegrationDynamicRESTTool(row))
}

func BuildDynamicRESTToolDefinition(row database.DynamicRESTTool) (tooldefs.ToolDefinition, error) {
	def, err := restapitoolintegration.BuildDynamicRESTToolDefinition(toIntegrationDynamicRESTTool(row))
	if err != nil {
		return tooldefs.ToolDefinition{}, err
	}
	return toolDefinitionFromIntegration(def), nil
}

func NewDynamicRESTToolFromSnapshot(toolName string, dynamicToolsRaw interface{}) (Tool, bool, error) {
	def, found, err := restapitoolintegration.NewDynamicRESTToolDefinitionFromSnapshot(toolName, dynamicToolsRaw)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return NewToolFromDefinition(toolDefinitionFromIntegration(def)), true, nil
}

func GetNewToolInstanceByNameOrSnapshot(toolName string, initData map[string]interface{}, dynamicToolsRaw interface{}, mcpToolsRaw interface{}) (Tool, error) {
	if tool, found := NewToolByName(toolName); found && tool != nil {
		if tool.GetRequiresInit() {
			tool.SetInitData(initData)
		}
		return tool, nil
	}

	dynamicTool, found, err := NewDynamicRESTToolFromSnapshot(toolName, dynamicToolsRaw)
	if err != nil {
		return nil, err
	}
	if !found || dynamicTool == nil {
		mcpTool, mcpFound, mcpErr := NewMCPToolFromSnapshot(toolName, mcpToolsRaw)
		if mcpErr != nil {
			return nil, mcpErr
		}
		if !mcpFound || mcpTool == nil {
			return nil, nil
		}
		if mcpTool.GetRequiresInit() {
			mcpTool.SetInitData(initData)
		}
		return mcpTool, nil
	}
	if dynamicTool.GetRequiresInit() {
		dynamicTool.SetInitData(initData)
	}
	return dynamicTool, nil
}

// toolDefinitionFromIntegration maps the integration-owned, core-independent
// toolinterface.Definition back onto the core tool definition type. The two
// structs intentionally mirror each other; keeping this conversion at the core
// boundary lets an integration build callable tools without importing core
// packages.
func toolDefinitionFromIntegration(def toolinterface.Definition) tooldefs.ToolDefinition {
	return tooldefs.ToolDefinition{
		Name:                           def.Name,
		FunctionName:                   def.FunctionName,
		Description:                    def.Description,
		AdminOnly:                      def.AdminOnly,
		RequiresInit:                   def.RequiresInit,
		InitSchema:                     def.InitSchema,
		RequiresConfirmation:           def.RequiresConfirmation,
		StopOnFirstConfirmableToolCall: def.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:       def.ConfirmationBlockMessage,
		InputType:                      def.InputType,
		InputSchema:                    def.InputSchema,
		RequiredParams:                 def.RequiredParams,
		Parameters:                     def.Parameters,
		RunFunction:                    def.Run,
		ResolveConfirmation:            def.ResolveConfirmation,
	}
}

// toIntegrationDynamicRESTTool converts the core's own DynamicRESTTool copy
// into the integration-owned model (the two intentionally share a schema).
func toIntegrationDynamicRESTTool(row database.DynamicRESTTool) restapitoolintegration.DynamicRESTTool {
	return restapitoolintegration.DynamicRESTTool{
		Model: restapitoolintegration.Model{
			UUID:      row.UUID,
			ID:        row.ID,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
			DeletedAt: row.DeletedAt,
		},
		OwnerUserId:                    row.OwnerUserId,
		Name:                           row.Name,
		FunctionName:                   row.FunctionName,
		Description:                    row.Description,
		AdminOnly:                      row.AdminOnly,
		RequiresConfirmation:           row.RequiresConfirmation,
		StopOnFirstConfirmableToolCall: row.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:       row.ConfirmationBlockMessage,
		Enabled:                        row.Enabled,
		OpenAPISourceType:              row.OpenAPISourceType,
		OpenAPISource:                  row.OpenAPISource,
		OperationID:                    row.OperationID,
		HTTPMethod:                     row.HTTPMethod,
		Path:                           row.Path,
		BaseURLSource:                  row.BaseURLSource,
		BaseURLInputName:               row.BaseURLInputName,
		ParamBindings:                  row.ParamBindings,
		SafetyPolicy:                   row.SafetyPolicy,
	}
}

// fromIntegrationDynamicRESTTool is the inverse of toIntegrationDynamicRESTTool.
func fromIntegrationDynamicRESTTool(row restapitoolintegration.DynamicRESTTool) database.DynamicRESTTool {
	return database.DynamicRESTTool{
		Model: database.Model{
			UUID:      row.UUID,
			ID:        row.ID,
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
			DeletedAt: row.DeletedAt,
		},
		OwnerUserId:                    row.OwnerUserId,
		Name:                           row.Name,
		FunctionName:                   row.FunctionName,
		Description:                    row.Description,
		AdminOnly:                      row.AdminOnly,
		RequiresConfirmation:           row.RequiresConfirmation,
		StopOnFirstConfirmableToolCall: row.StopOnFirstConfirmableToolCall,
		ConfirmationBlockMessage:       row.ConfirmationBlockMessage,
		Enabled:                        row.Enabled,
		OpenAPISourceType:              row.OpenAPISourceType,
		OpenAPISource:                  row.OpenAPISource,
		OperationID:                    row.OperationID,
		HTTPMethod:                     row.HTTPMethod,
		Path:                           row.Path,
		BaseURLSource:                  row.BaseURLSource,
		BaseURLInputName:               row.BaseURLInputName,
		ParamBindings:                  row.ParamBindings,
		SafetyPolicy:                   row.SafetyPolicy,
	}
}
