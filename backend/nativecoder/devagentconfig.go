package nativecoder

import "strings"

// This file is the shared-config wiring for ci#757: the legacy
// "trigger coding interaction" GitHub Actions workflow stops being the primary
// dispatch route, and a native development agent gets the hive-mind tool family
// (dispatch + observe + interaction-history recall) in its effective shared
// config so the native route just works after a deploy.

// DevAgentHiveMindConfigKey is the optional per-agent shared-config override:
//
//	dev_agent_hivemind: true|false
//
// Absent means "default ON" for a native coder agent (ci#757). An explicit
// false-like value opts a single agent out, e.g. when an operator still wants
// the deprecated workflow route only.
const DevAgentHiveMindConfigKey = "dev_agent_hivemind"

// DevAgentHiveMindDeprecationNotice is the shared wording for the retirement of
// the workflow-only dispatch route. Kept as a constant so docs, preflight logs
// and tests share exactly one string.
const DevAgentHiveMindDeprecationNotice = "deprecated: the trigger-coding-interaction GitHub Actions workflow is superseded by native hive_mind_agent_* dispatch (ci#757)"

// HasExplicitTools reports whether a shared config carries an explicitly
// configured, non-empty `tools` list. This mirrors the ci#617 semantics: an
// empty list means "not configured" (compiled-in defaults apply) while a
// non-empty list is an operator decision that must win over auto-injection.
func HasExplicitTools(config map[string]interface{}) bool {
	if config == nil {
		return false
	}
	return len(stringSliceFromConfig(config["tools"])) > 0
}

// DevAgentHiveMindConfigEnabled resolves the per-agent opt-out. A missing key
// keeps the family on; only an explicit false-like value turns it off.
func DevAgentHiveMindConfigEnabled(config map[string]interface{}) bool {
	if config == nil {
		return true
	}
	raw, ok := config[DevAgentHiveMindConfigKey]
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

func isFalseLikeValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}

// ApplyDevAgentHiveMindConfig merges the hive-mind tool family into a native
// coder agent's shared config, exactly like an integration
// SharedConfigDefaults contribution:
//
//   - a non-coder config is never touched,
//   - an explicitly configured non-empty `tools` list always wins,
//   - both the deployment kill switch (DevAgentHiveMindEnvKey) and the
//     per-agent `dev_agent_hivemind: false` opt-out disable the merge,
//   - the merge is idempotent: an already widened list is left unchanged.
//
// The input map is modified in place and returned for convenient chaining; the
// bool reports whether the family was merged.
func ApplyDevAgentHiveMindConfig(config map[string]interface{}, envValue string) (map[string]interface{}, bool) {
	if config == nil {
		return nil, false
	}
	if !IsCoderAgentConfig(config) {
		return config, false
	}
	if DevAgentHiveMindEnvDisabled(envValue) || !DevAgentHiveMindConfigEnabled(config) {
		return config, false
	}
	if HasExplicitTools(config) {
		return config, false
	}
	existing := stringSliceFromConfig(config["tools"])
	if HasAllDevAgentHiveMindTools(existing) {
		return config, false
	}
	config["tools"] = mergeToolNames(existing, DevAgentHiveMindToolNames())
	return config, true
}

// HasAllDevAgentHiveMindTools reports whether every hive-mind tool name is
// already present in tools.
func HasAllDevAgentHiveMindTools(tools []string) bool {
	return hasAllTools(tools, DevAgentHiveMindToolNames())
}

// DevAgentDispatchReadiness is the deployment preflight answer to "will native
// dispatch just work for a development agent here?".
type DevAgentDispatchReadiness struct {
	// FamilyEnabled is false only when the deployment kill switch is off.
	FamilyEnabled bool
	// ToolsPresent is true when every required dispatch tool is configured, so
	// a chat driven by this agent can start other agents natively.
	ToolsPresent bool
	// ExplicitToolsOverrode is true when an operator narrowed `tools` so the
	// family was not merged and the agent falls back to the legacy route.
	ExplicitToolsOverrode bool
	// MissingRequiredTools lists required dispatch tools absent from the config.
	MissingRequiredTools []string
	// MissingRecallTools lists optional old-chat-context tools absent from the
	// config. Their absence only costs recall efficiency, not dispatch.
	MissingRecallTools []string
}

// DispatchReady reports whether native dispatch is available for this agent.
func (r DevAgentDispatchReadiness) DispatchReady() bool {
	return r.FamilyEnabled && r.ToolsPresent
}

// LegacyRouteRequired reports whether the deprecated GitHub Actions workflow is
// still the only usable dispatch route for this agent.
func (r DevAgentDispatchReadiness) LegacyRouteRequired() bool {
	return !r.ToolsPresent
}

// EvaluateDevAgentDispatchReadiness inspects an effective shared config. It is
// side-effect free and cheap, so callers can log it at bot bootstrap or expose
// it on a preflight/debug surface.
func EvaluateDevAgentDispatchReadiness(config map[string]interface{}) DevAgentDispatchReadiness {
	readiness := DevAgentDispatchReadiness{FamilyEnabled: !DevAgentHiveMindEnvDisabled(DevAgentHiveMindEnvValue())}
	if config == nil {
		readiness.MissingRequiredTools = DevAgentHiveMindRequiredTools()
		readiness.MissingRecallTools = DevAgentHiveMindRecallToolNames()
		return readiness
	}
	present := make(map[string]struct{})
	for _, name := range stringSliceFromConfig(config["tools"]) {
		present[name] = struct{}{}
	}
	for _, name := range DevAgentHiveMindRequiredTools() {
		if _, ok := present[name]; !ok {
			readiness.MissingRequiredTools = append(readiness.MissingRequiredTools, name)
		}
	}
	for _, name := range DevAgentHiveMindRecallToolNames() {
		if _, ok := present[name]; !ok {
			readiness.MissingRecallTools = append(readiness.MissingRecallTools, name)
		}
	}
	readiness.ToolsPresent = len(readiness.MissingRequiredTools) == 0
	readiness.ExplicitToolsOverrode = readiness.FamilyEnabled && !readiness.ToolsPresent
	return readiness
}
