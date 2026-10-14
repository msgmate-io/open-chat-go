package tools

type ToolDefinition struct {
	Name                           string
	FunctionName                   string
	Description                    string
	AdminOnly                      bool
	RequiresInit                   bool
	InitSchema                     map[string]interface{}
	RequiresConfirmation           bool
	StopOnFirstConfirmableToolCall bool
	ConfirmationBlockMessage       string
	InputType                      interface{}
	InputSchema                    map[string]interface{}
	RequiredParams                 []string
	Parameters                     map[string]interface{}
	RunFunction                    func(input interface{}, init map[string]interface{}) (string, error)
	// ResolveConfirmation optionally overrides RequiresConfirmation at call
	// time from code (never user config). See
	// toolinterface.ConfirmationResolver.
	ResolveConfirmation func(input interface{}, init map[string]interface{}) (requiresConfirmation bool, resolvedResult string)
}

var RunCallbackExecutor func(initData map[string]interface{}, input map[string]interface{}) error
