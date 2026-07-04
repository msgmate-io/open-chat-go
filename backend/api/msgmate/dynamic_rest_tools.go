package msgmate

import (
	tooldefs "backend/api/msgmate/tools"
	"backend/database"

	restapitoolintegration "github.com/msgmate-io/rest-api-tool-integration"
	"gorm.io/gorm"
)

func ResolveUserDynamicRESTToolByName(db *gorm.DB, ownerUserID uint, toolName string) (*database.DynamicRESTTool, error) {
	return restapitoolintegration.ResolveUserDynamicRESTToolByName(db, ownerUserID, toolName)
}

func BuildDynamicRESTToolSnapshot(row database.DynamicRESTTool) map[string]interface{} {
	return restapitoolintegration.BuildDynamicRESTToolSnapshot(row)
}

func BuildDynamicRESTToolDefinition(row database.DynamicRESTTool) (tooldefs.ToolDefinition, error) {
	return restapitoolintegration.BuildDynamicRESTToolDefinition(row)
}

func NewDynamicRESTToolFromSnapshot(toolName string, dynamicToolsRaw interface{}) (Tool, bool, error) {
	def, found, err := restapitoolintegration.NewDynamicRESTToolDefinitionFromSnapshot(toolName, dynamicToolsRaw)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return NewToolFromDefinition(def), true, nil
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
