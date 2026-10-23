package nativecoder

import (
	"os"
	"strings"
)

// This file is the native-coder half of ci#809 ("agent should concisely finish
// + report completion").
//
// The core of ci#809 already shipped on the msgmate surface:
//
//   - `all_tasks_completed` is registered in backend/api/msgmate/Tools.go,
//   - it is implemented in backend/api/msgmate/tools/all_tasks_completed_tool.go,
//   - backend/api/msgmate/MsgmateBotAIHandler.go promotes a successful call into
//     `task_completed` message metadata,
//   - backend/api/chats/AutoContinue.go consumes that flag.
//
// The remaining gap is that a native coder agent never gets the tool: the
// compiled-in coder defaults only contribute the `coder_*` family, so unless an
// operator hand-adds `all_tasks_completed` the native route cannot emit the
// machine-readable completion signal and the auto-continue loop never sees it.
//
// This mirrors ApplyDevAgentHiveMindConfig (devagentconfig.go) exactly, so the
// two auto-injections behave identically.

// CoderTaskCompletionConfigKey is the optional per-agent shared-config override:
//
//	coder_task_completion: true|false
//
// Absent means "default ON" for a native coder agent. An explicit false-like
// value opts a single agent out.
const CoderTaskCompletionConfigKey = "coder_task_completion"

// AllTasksCompletedToolName is the msgmate tool that turns a successful call
// into `task_completed` metadata. Kept as a constant so the tool registry, the
// native coder surface and the tests share exactly one string.
const AllTasksCompletedToolName = "all_tasks_completed"

// CoderTaskCompletionEnvKey is the deployment-wide kill switch for the native
// coder completion tool. A false-like value disables the auto-injection for
// every agent on the deployment.
const CoderTaskCompletionEnvKey = "OPENCHAT_CODER_TASK_COMPLETION_TOOLS"

// CoderTaskCompletionEnvValue reads the deployment kill switch from the
// environment.
func CoderTaskCompletionEnvValue() string {
	return strings.TrimSpace(os.Getenv(CoderTaskCompletionEnvKey))
}

// CoderTaskCompletionEnvDisabled reports whether the deployment kill switch is
// set to a false-like value.
func CoderTaskCompletionEnvDisabled(envValue string) bool {
	return isFalseLikeValue(strings.TrimSpace(envValue))
}

// CoderTaskCompletionToolNames is the injected tool set. A slice (not a single
// string) so it stays symmetric with DevAgentHiveMindToolNames and so the set
// can grow without changing call sites.
func CoderTaskCompletionToolNames() []string {
	return []string{AllTasksCompletedToolName}
}

// CoderTaskCompletionConfigEnabled resolves the per-agent opt-out. A missing key
// keeps the tool on; only an explicit false-like value turns it off.
func CoderTaskCompletionConfigEnabled(config map[string]interface{}) bool {
	if config == nil {
		return true
	}
	raw, ok := config[CoderTaskCompletionConfigKey]
	if !ok {
		return true
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		return !isFalseLikeValue(typed)
	default:
		return true
	}
}

// HasAllCoderTaskCompletionTools reports whether every completion tool name is
// already present in tools.
func HasAllCoderTaskCompletionTools(tools []string) bool {
	return hasAllTools(tools, CoderTaskCompletionToolNames())
}

// HasCoderTaskCompletionTool reports whether the resolved tools list already
// carries the completion tool. This is the predicate the prompt/instruction
// layer should use: only advertise the tool when the agent can actually call it.
func HasCoderTaskCompletionTool(tools []string) bool {
	for _, name := range tools {
		if name == AllTasksCompletedToolName {
			return true
		}
	}
	return false
}

// ApplyCoderTaskCompletionConfig merges the completion tool into a native coder
// agent's shared config, exactly like an integration SharedConfigDefaults
// contribution:
//
//   - a non-coder config is never touched,
//   - an explicitly configured non-empty `tools` list always wins,
//   - both the deployment kill switch (CoderTaskCompletionEnvKey) and the
//     per-agent `coder_task_completion: false` opt-out disable the merge,
//   - the merge is idempotent: an already widened list is left unchanged.
//
// Like ApplyDevAgentHiveMindConfig this is meant to run BEFORE the compiled-in
// integration defaults fill `tools`; a non-empty list is treated as an
// operator decision and is never widened.
//
// The input map is modified in place and returned for convenient chaining; the
// bool reports whether the tool was merged.
func ApplyCoderTaskCompletionConfig(config map[string]interface{}, envValue string) (map[string]interface{}, bool) {
	if config == nil {
		return nil, false
	}
	if !IsCoderAgentConfig(config) {
		return config, false
	}
	if CoderTaskCompletionEnvDisabled(envValue) || !CoderTaskCompletionConfigEnabled(config) {
		return config, false
	}
	if HasExplicitTools(config) {
		return config, false
	}
	existing := stringSliceFromConfig(config[ToolsConfigKey])
	if HasAllCoderTaskCompletionTools(existing) {
		return config, false
	}
	config[ToolsConfigKey] = mergeToolNames(existing, CoderTaskCompletionToolNames())
	return config, true
}
