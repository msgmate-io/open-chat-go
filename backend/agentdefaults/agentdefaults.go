package agentdefaults

import "github.com/msgmate-io/go-integration-interface/integrationinterface"

// Agent-default hooks (ci#394): the coder integration owns the config defaults
// that used to be hardcoded in the core, exposed through the integration
// Definition. The core consults the first Definition that declares an
// agent-default hook; a build without such an integration (eg the public
// core-only profile) applies none of them.

// agentDefinition returns the first registered Definition that declares any
// agent-default hook.
func agentDefinition() (integrationinterface.Definition, bool) {
	for _, def := range integrationinterface.List() {
		if def.AgentConfig != nil ||
			def.AgentDefaults != nil ||
			def.AgentInteractionDefaults != nil ||
			def.AgentChatSetup != nil ||
			def.AgentInteractionTags != nil {
			return def, true
		}
	}
	return integrationinterface.Definition{}, false
}

// AgentConfig reports whether config declares an integration agent, and whether
// an integration declares the predicate at all.
func AgentConfig(config map[string]interface{}) (bool, bool) {
	if def, ok := agentDefinition(); ok && def.AgentConfig != nil {
		return def.AgentConfig(config), true
	}
	return false, false
}

// ApplyAgentDefaults merges the integration's bot-level defaults.
func ApplyAgentDefaults(config map[string]interface{}) (map[string]interface{}, bool) {
	if def, ok := agentDefinition(); ok && def.AgentDefaults != nil {
		return def.AgentDefaults(config), true
	}
	return config, false
}

// ApplyAgentInteractionDefaults merges the integration's interaction-time
// defaults.
func ApplyAgentInteractionDefaults(config map[string]interface{}, tools, tags []string, toolInit, overrides map[string]interface{}) (map[string]interface{}, bool) {
	if def, ok := agentDefinition(); ok && def.AgentInteractionDefaults != nil {
		return def.AgentInteractionDefaults(config, tools, tags, toolInit, overrides), true
	}
	return config, false
}

// ApplyAgentChatSetup applies the integration's chat-create-time defaults.
func ApplyAgentChatSetup(config map[string]interface{}, tags []string, toolInit map[string]interface{}) (map[string]interface{}, bool) {
	if def, ok := agentDefinition(); ok && def.AgentChatSetup != nil {
		return def.AgentChatSetup(config, tags, toolInit), true
	}
	return config, false
}

// ApplyAgentInteractionTags returns the tags the integration adds to an
// interaction.
func ApplyAgentInteractionTags(config map[string]interface{}, botName string, explicit []string) ([]string, bool) {
	if def, ok := agentDefinition(); ok && def.AgentInteractionTags != nil {
		return def.AgentInteractionTags(config, botName, explicit), true
	}
	return explicit, false
}

// ToolsConfigKey is the shared-config key that lists the tools an agent may
// call.
const ToolsConfigKey = "tools"

// DropEmptyTools returns the config with an empty `tools` list removed, so the
// compiled-in integration defaults fill the full tool set instead of being
// suppressed by an editor-written `tools: []` (ci#617). The input is never
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
