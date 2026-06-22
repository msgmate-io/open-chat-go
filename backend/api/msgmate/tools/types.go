package tools

type ToolDefinition struct {
	Name                           string
	FunctionName                   string
	Description                    string
	AdminOnly                      bool
	RequiresInit                   bool
	RequiresConfirmation           bool
	StopOnFirstConfirmableToolCall bool
	ConfirmationBlockMessage       string
	InputType                      interface{}
	InputSchema                    map[string]interface{}
	RequiredParams                 []string
	Parameters                     map[string]interface{}
	RunFunction                    func(input interface{}, init map[string]interface{}) (string, error)
}

var RunCallbackExecutor func(initData map[string]interface{}, input map[string]interface{}) error
