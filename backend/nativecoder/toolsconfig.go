package nativecoder

// ToolsConfigKey is the shared-config key that lists the tools an agent may
// call. The compiled-in integrations contribute their own default list through
// ApplySharedConfigDefaults, which only fills keys that are ABSENT.
const ToolsConfigKey = "tools"

// DropEmptyTools returns the config with an empty `tools` list removed.
//
// A bot whose stored config carries `tools: []` (what an editor that always
// writes the array produces) is indistinguishable, at the merge layer, from an
// intentional "no tools": ApplySharedConfigDefaults never overwrites an
// existing key, so the empty list suppressed every compiled-in integration
// default and the agent was left with only the tool the runtime always adds -
// the ci#617 report of little-world / dual-repo agents that could call
// coder_select_workspace but none of coder_bash / coder_git / coder_read.
//
// An empty list therefore means "not configured": drop the key so the defaults
// fill the full coder set. A non-empty list is preserved verbatim (an
// intentional narrowing still wins) and a non-list value is left untouched so
// the existing config validation keeps reporting it. The input map is never
// mutated.
func DropEmptyTools(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	raw, exists := config[ToolsConfigKey]
	if !exists || !isEmptyToolList(raw) {
		return config
	}
	out := make(map[string]interface{}, len(config))
	for key, value := range config {
		if key == ToolsConfigKey {
			continue
		}
		out[key] = value
	}
	return out
}

// isEmptyToolList reports whether a `tools` value means "no tools configured".
func isEmptyToolList(raw interface{}) bool {
	switch typed := raw.(type) {
	case nil:
		return true
	case []interface{}:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	default:
		return false
	}
}
