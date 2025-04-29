package database

import "encoding/json"

// DynamicRESTTool stores runtime-configurable REST tool definitions that are
// loaded without recompiling the backend.
type DynamicRESTTool struct {
	Model
	OwnerUserId                    uint            `json:"owner_user_id" gorm:"index;uniqueIndex:idx_dynamic_rest_tool_owner_name"`
	OwnerUser                      User            `json:"-" gorm:"foreignKey:OwnerUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	Name                           string          `json:"name" gorm:"size:160;uniqueIndex:idx_dynamic_rest_tool_owner_name"`
	FunctionName                   string          `json:"function_name" gorm:"size:160"`
	Description                    string          `json:"description" gorm:"type:text"`
	AdminOnly                      bool            `json:"admin_only" gorm:"default:false"`
	RequiresConfirmation           bool            `json:"requires_confirmation" gorm:"default:false"`
	StopOnFirstConfirmableToolCall bool           `json:"stop_on_first_confirmable_tool_call" gorm:"default:false"`
	ConfirmationBlockMessage       string          `json:"confirmation_block_message" gorm:"type:text"`
	Enabled                        bool            `json:"enabled" gorm:"default:true;index"`
	OpenAPISourceType              string          `json:"openapi_source_type" gorm:"size:16"`
	OpenAPISource                  string          `json:"openapi_source" gorm:"type:text"`
	OperationID                    string          `json:"operation_id" gorm:"size:255"`
	HTTPMethod                     string          `json:"http_method" gorm:"size:16"`
	Path                           string          `json:"path" gorm:"size:1024"`
	BaseURLSource                  string          `json:"base_url_source" gorm:"size:16"`
	BaseURLInputName               string          `json:"base_url_input_name" gorm:"size:128"`
	ParamBindings                  json.RawMessage `json:"param_bindings" gorm:"type:jsonb"`
	SafetyPolicy                   json.RawMessage `json:"safety_policy" gorm:"type:jsonb"`
}
