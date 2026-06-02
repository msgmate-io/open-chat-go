package tasks

const (
	QueueDefault      = "default"
	TypeToolExecution = "tools:execute"
)

type ToolExecutionPayload struct {
	ChatUUID        string                 `json:"chat_uuid"`
	ToolName        string                 `json:"tool_name"`
	UserID          uint                   `json:"user_id"`
	InputParameters map[string]interface{} `json:"input_parameters,omitempty"`
}

type ToolExecutionResult struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}
