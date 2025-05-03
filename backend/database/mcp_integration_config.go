package database

import "encoding/json"

// MCPIntegrationConfig stores owner-scoped external MCP server configuration.
//
// Model migration/registration is provided by the compiled MCP integration module.
type MCPIntegrationConfig struct {
	Model
	OwnerUserId uint            `json:"owner_user_id" gorm:"index;uniqueIndex:idx_mcp_integration_owner_name"`
	OwnerUser   User            `json:"-" gorm:"foreignKey:OwnerUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Name        string          `json:"name" gorm:"size:160;uniqueIndex:idx_mcp_integration_owner_name"`
	Config      json.RawMessage `json:"config" gorm:"type:jsonb"`
	AuthData    json.RawMessage `json:"auth_data" gorm:"type:jsonb"`
	Enabled     bool            `json:"enabled" gorm:"default:true;index"`
}
