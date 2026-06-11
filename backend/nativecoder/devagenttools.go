package nativecoder

import (
	"os"
	"sort"
	"strings"
)

// DevAgentHiveMindEnvKey is the deployment-wide kill switch for the native
// hive-mind tool family on development-agent chats. Unlike the
// auto-consolidation default (ci#653) this family is default-ON, so native
// dispatch "just works" on a fresh deploy of ci.msgmate.io (ci#757). Only an
// explicit false-like value (0/false/no/off/disabled) turns it off.
const DevAgentHiveMindEnvKey = "OPENCHAT_DEV_AGENT_HIVEMIND_TOOLS"

// DevAgentHiveMindEnvValue reads the raw kill-switch value. Kept separate so
// callers can thread a cached/prefetched value instead.
func DevAgentHiveMindEnvValue() string {
	return strings.TrimSpace(os.Getenv(DevAgentHiveMindEnvKey))
}

// DevAgentHiveMindEnvDisabled reports whether a raw kill-switch value disables
// the family. An empty value keeps it enabled.
func DevAgentHiveMindEnvDisabled(raw string) bool {
	return isFalseLikeValue(raw)
}

// DevAgentHiveMindToolNames is the native dispatch/observation family plus the
// interaction-history tools a development agent needs to recall old chat
// context through the hive mind instead of the server UI (ci#757).
//
// The dispatch/observe names are required for the native route; the
// `mcp:open-chat-opencode-mcp:openchat_*` names are the optional recall
// helpers that make old-chat search efficient.
func DevAgentHiveMindToolNames() []string {
	names := make([]string, 0, len(devAgentHiveMindDispatchTools)+len(devAgentHiveMindRecallTools))
	names = append(names, devAgentHiveMindDispatchTools...)
	names = append(names, devAgentHiveMindRecallTools...)
	return names
}

var (
	// devAgentHiveMindDispatchTools are the names a development agent must have
	// to start/steer other development agents natively.
	devAgentHiveMindDispatchTools = []string{
		"hive_mind_agent_dispatch",
		"hive_mind_agent_list",
		"hive_mind_agent_get",
		"hive_mind_agent_continue",
		"hive_mind_agent_stop",
		"hive_mind_observe_list",
		"hive_mind_observe_get",
		"hive_mind_state_get",
		"hive_mind_state_set",
		"hive_mind_influence",
	}

	// devAgentHiveMindRecallTools let a development agent search old chat
	// context (interactions, messages) directly through the hive-mind MCP
	// surface.
	devAgentHiveMindRecallTools = []string{
		"mcp:open-chat-opencode-mcp:openchat_list_interactions",
		"mcp:open-chat-opencode-mcp:openchat_get_interaction",
		"mcp:open-chat-opencode-mcp:openchat_search_interactions",
		"mcp:open-chat-opencode-mcp:openchat_search_interaction_messages",
	}
)

// DevAgentHiveMindRequiredTools returns the dispatch family. These are the
// names whose absence means the native route cannot work.
func DevAgentHiveMindRequiredTools() []string {
	return append([]string(nil), devAgentHiveMindDispatchTools...)
}

// DevAgentHiveMindRecallToolNames returns the optional old-chat-context tools.
func DevAgentHiveMindRecallToolNames() []string {
	return append([]string(nil), devAgentHiveMindRecallTools...)
}

// DevAgentHiveMindToolInitKeys are the tool_init entries a development agent
// needs so the family is initialized on chat start; the dispatch and observe
// list tools are enough to drive and discover work.
func DevAgentHiveMindToolInitKeys() []string {
	return []string{
		"hive_mind_agent_dispatch",
		"hive_mind_agent_list",
		"hive_mind_observe_list",
	}
}

// devAgentRoleAliases are the role/bot markers that identify a chat as a
// development agent (lead or worker) rather than an end-user chat.
var devAgentRoleAliases = []string{
	"lead",
	"dev-lead",
	"development-lead",
	"dev-agent",
	"development-agent",
	"coder",
	"coder-agent",
	"native-coder",
}

// IsDevAgentRole reports whether role identifies a development-agent chat. The
// match is case-insensitive, tolerates spaces/underscores for dashes and
// accepts numeric suffixes such as `dev-agent-2`.
func IsDevAgentRole(role string) bool {
	norm := strings.ToLower(strings.TrimSpace(role))
	norm = strings.ReplaceAll(norm, "_", "-")
	norm = strings.ReplaceAll(norm, " ", "-")
	norm = strings.Trim(norm, "-")
	if norm == "" {
		return false
	}
	for _, alias := range devAgentRoleAliases {
		if norm == alias || strings.HasPrefix(norm, alias+"-") {
			return true
		}
	}
	return false
}

// ApplyDevAgentHiveMindTools augments a chat's tool list with the hive-mind
// family when the chat belongs to a development agent.
//
// envValue is the raw DevAgentHiveMindEnvKey value: empty keeps the family
// enabled, false-like disables it deployment-wide (ci#757).
//
// explicitlyConfigured mirrors the shared-config `tools` semantics: an empty
// list means "not configured" (ci#617 defaults apply), a non-empty configured
// list is an operator decision and therefore wins. Callers should pass
// explicitlyConfigured=true only for a non-empty configured array.
//
// The returned bool reports whether the family was injected.
func ApplyDevAgentHiveMindTools(role string, tools []string, explicitlyConfigured bool, envValue string) ([]string, bool) {
	if DevAgentHiveMindEnvDisabled(envValue) || !IsDevAgentRole(role) {
		return tools, false
	}
	if explicitlyConfigured && len(tools) > 0 {
		return tools, false
	}
	if hasAllTools(tools, devAgentHiveMindDispatchTools) {
		return tools, false
	}
	return mergeToolNames(tools, DevAgentHiveMindToolNames()), true
}

// hasAllTools reports whether every name in want is present in have.
func hasAllTools(have []string, want []string) bool {
	present := make(map[string]struct{}, len(have))
	for _, name := range have {
		present[strings.TrimSpace(name)] = struct{}{}
	}
	for _, name := range want {
		if _, ok := present[name]; !ok {
			return false
		}
	}
	return true
}

// mergeToolNames returns a sorted, de-duplicated union of base and extra. The
// input slices are never modified.
func mergeToolNames(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, name := range list {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// HiveMindUsageSample is one observed development-agent chat and the tools it
// actually invoked, used to evaluate how well the hive-mind family is used on
// ci.msgmate.io (ci#757).
type HiveMindUsageSample struct {
	ChatUUID string
	Bot      string
	Tools    []string
}

// HiveMindUsageReport summarises hive-mind adoption across observed chats.
type HiveMindUsageReport struct {
	Chats               int
	ChatsUsingHiveMind  int
	ToolCalls           map[string]int
	UnusedHiveMindTools []string
	Coverage            float64
}

// EvaluateHiveMindUsage computes hive-mind adoption from observed chats. The
// report is deterministic: unused tools are sorted and coverage is the share of
// observed chats that used at least one hive-mind tool.
func EvaluateHiveMindUsage(samples []HiveMindUsageSample) HiveMindUsageReport {
	family := DevAgentHiveMindToolNames()
	known := make(map[string]struct{}, len(family))
	for _, name := range family {
		known[name] = struct{}{}
	}

	report := HiveMindUsageReport{
		Chats:     len(samples),
		ToolCalls: make(map[string]int, len(family)),
	}
	for _, sample := range samples {
		used := false
		for _, tool := range sample.Tools {
			tool = strings.TrimSpace(tool)
			if _, ok := known[tool]; !ok {
				continue
			}
			report.ToolCalls[tool]++
			used = true
		}
		if used {
			report.ChatsUsingHiveMind++
		}
	}
	for _, name := range family {
		if report.ToolCalls[name] == 0 {
			report.UnusedHiveMindTools = append(report.UnusedHiveMindTools, name)
		}
	}
	if report.Chats > 0 {
		report.Coverage = float64(report.ChatsUsingHiveMind) / float64(report.Chats)
	}
	return report
}
