package nativecoder

import (
	"os"
	"strings"
)

// This file is the wiring of the auto-consolidation default (ci#653): the
// `auto_consolidation` key is part of a native coder agent's effective shared
// config unless it is switched off, and the deployment-wide kill switch rolls
// the default back fleet-wide.

// coderAgentConfigKeys are the top-level shared-config keys that declare a
// native coder agent. They mirror the tool-side role/sandbox/provider keys, so
// the default is only ever merged into a native coder agent's config and never
// into an opencode or plain assistant bot.
var coderAgentConfigKeys = []string{
	"coder_interaction",
	"coder_mode",
	"coder_workspace",
	"coder_workspace_uuid",
	"coder_sandbox_backend",
	"coder_sandbox_template",
	"coder_sandbox_image",
	"coder_sandbox_workspace_size",
	"coder_sandbox_mode",
	"coder_sandbox_max_shared_workspaces",
	"coder_sandbox_max_agents_per_pool",
	"coder_provider_max_concurrency",
	"coder_provider_queue_timeout_ms",
	"coder_provider_fallback_on_saturation",
	"coder_provider_fallback_backend",
	"coder_provider_fallbacks",
	// Declaring the policy explicitly also declares the agent as a native
	// coder agent, so `auto_consolidation: {enabled: false}` always applies.
	AutoConsolidationConfigKey,
}

// coderToolPrefix is the tool-name prefix that marks a config as a native coder
// agent (eg `coder_bash`, `coder_edit`).
const coderToolPrefix = "coder_"

// coderToolInitSelectWorkspace is the tool_init entry every native coder agent
// carries (the chat-init workspace/sandbox selector).
const coderToolInitSelectWorkspace = "coder_select_workspace"

// IsCoderAgentConfig reports whether a shared config declares a native coder
// agent: a `coder_*` role/sandbox/provider key, a `coder_*` tool, or a
// `coder_select_workspace` tool_init entry.
func IsCoderAgentConfig(config map[string]interface{}) bool {
	if config == nil {
		return false
	}
	for _, key := range coderAgentConfigKeys {
		if _, ok := config[key]; ok {
			return true
		}
	}
	for _, raw := range stringSliceFromConfig(config["tools"]) {
		if raw == coderToolInitSelectWorkspace || strings.HasPrefix(raw, coderToolPrefix) {
			return true
		}
	}
	if toolInit, ok := config["tool_init"].(map[string]interface{}); ok {
		if _, ok := toolInit[coderToolInitSelectWorkspace]; ok {
			return true
		}
	}
	return false
}

// stringSliceFromConfig reads a tool-name list that may be decoded as
// []interface{}, []string or a single string.
func stringSliceFromConfig(raw interface{}) []string {
	switch typed := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				out = append(out, strings.TrimSpace(name))
			}
		}
		return out
	case []string:
		return typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}

// ApplyAutoConsolidationDefault merges the default-ON auto-consolidation policy
// into a native coder agent's shared config, exactly like an integration
// SharedConfigDefaults contribution: an already configured policy (the map
// form, a boolean or a boolean-like string) always wins, and a non-coder config
// is never touched.
//
// envValue is the raw value of AutoConsolidationEnvKey; an empty value keeps
// the default ON, a false-like value (0/false/off/no/disabled) rolls the
// default back for the whole deployment.
func ApplyAutoConsolidationDefault(config map[string]interface{}, envValue string) map[string]interface{} {
	if config == nil {
		return nil
	}
	if _, exists := config[AutoConsolidationConfigKey]; exists {
		return config
	}
	if !IsCoderAgentConfig(config) {
		return config
	}
	if AutoConsolidationEnvDisabled(envValue) {
		return config
	}
	config[AutoConsolidationConfigKey] = AutoConsolidationPolicyMap()
	return config
}

// AutoConsolidationEnvValue returns the deployment-wide kill-switch value.
func AutoConsolidationEnvValue() string {
	return os.Getenv(AutoConsolidationEnvKey)
}

// ResolvePolicyFromConfig resolves the auto-consolidation policy of a shared
// config, applying the default-on rule when the key is absent.
func ResolvePolicyFromConfig(config map[string]interface{}) AutoConsolidationPolicy {
	if config == nil {
		return ResolveAutoConsolidationPolicy(nil)
	}
	raw, exists := config[AutoConsolidationConfigKey]
	if !exists {
		if AutoConsolidationEnvDisabled(AutoConsolidationEnvValue()) {
			policy := DefaultAutoConsolidationPolicy()
			policy.Enabled = false
			return policy
		}
		return ResolveAutoConsolidationPolicy(nil)
	}
	return ResolveAutoConsolidationPolicy(raw)
}

// AutoConsolidationEnabledForConfig reports whether the feature is on for a
// shared config. It is the one call the turn-completion choke point needs.
func AutoConsolidationEnabledForConfig(config map[string]interface{}) bool {
	if config == nil {
		return false
	}
	if !IsCoderAgentConfig(config) {
		return false
	}
	return ResolvePolicyFromConfig(config).Enabled
}
