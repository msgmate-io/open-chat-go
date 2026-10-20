package nativecoder

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// dispatchroute.go is the machine-readable form of ci#757: the GitHub-Actions
// workflow route for starting a coding agent must stop being the route the
// development lead relies on. The native route is the in-server dispatch
// surface of backend/api/bots/AgentDispatch.go, reachable from a lead chat as
// the hive_mind_* family together with the openchat interaction-history tools.
//
// The file carries four things without ever carrying a secret:
//
//	(a) the deprecation descriptor: the workflow that is deprecated, the native
//	    replacement, and the verification phases with their exit criteria;
//	(b) the verification gate (DispatchRouteReady) so "the native route may
//	    replace the workflow" is a call, not a belief;
//	(c) the selective enablement of the hive-mind / interaction-recall families
//	    for development-lead chats (ApplyDevLeadDispatchTools);
//	(d) the measurement of how either route is actually used in past runs
//	    (ComputeHiveMindUsage, fed by an offline transcript export).
const (
	// LegacyDispatchWorkflow is the deprecated trigger route.
	LegacyDispatchWorkflow = "trigger-open-chat-coding-interaction.yaml"
	// LegacyDispatchRepo is the private repository hosting that workflow.
	LegacyDispatchRepo = "msgmate-io/open-chat-go-ci"
	// NativeDispatchTool is the native replacement for the workflow trigger.
	NativeDispatchTool = "hive_mind_agent_dispatch"
	// HiveMindToolsConfigKey is the per-config opt-out/opt-in key.
	HiveMindToolsConfigKey = "hive_mind_tools"
	// HiveMindToolsEnvKey is the deployment-wide kill switch. It rolls the
	// selective enablement back fleet-wide (the tools then have to be listed
	// in a chat's own `tools` array).
	HiveMindToolsEnvKey = "OCI_DISABLE_HIVE_MIND_TOOLS"
	// DevelopmentLeadRole is the native coder role the dev lead runs as.
	DevelopmentLeadRole = "lead"
)

// NativeDispatchTools is the lifecycle of the native trigger route: start,
// list/get, continue/stop and observe a peer interaction.
var NativeDispatchTools = []string{
	"hive_mind_agent_dispatch",
	"hive_mind_agent_list",
	"hive_mind_agent_get",
	"hive_mind_agent_continue",
	"hive_mind_agent_stop",
	"hive_mind_observe_list",
	"hive_mind_observe_get",
}

// InteractionRecallTools is the old-chat-context search surface. It is what
// answers "what did we already try on this issue?" from inside a dev-agent
// chat instead of by scrolling the server UI.
var InteractionRecallTools = []string{
	"mcp:open-chat-opencode-mcp:openchat_list_interactions",
	"mcp:open-chat-opencode-mcp:openchat_get_interaction",
	"mcp:open-chat-opencode-mcp:openchat_search_interactions",
	"mcp:open-chat-opencode-mcp:openchat_search_interaction_messages",
}

// devLeadRoles are the role values that declare a development lead.
var devLeadRoles = []string{"lead", "dev-lead", "development-lead"}

// devLeadConfigKeys are the top-level shared-config keys that declare a
// development-lead chat (mirroring the coder_* role keys).
var devLeadConfigKeys = []string{"coder_role", "development_lead", "dev_lead"}

// DispatchRoutePhase is one phase of the deprecation.
type DispatchRoutePhase struct {
	ID           string   `json:"id"`
	Order        int      `json:"order"`
	Title        string   `json:"title"`
	Goal         string   `json:"goal"`
	ExitCriteria []string `json:"exit_criteria"`
	Tracks       []int    `json:"tracks"`
}

// DispatchRoutePlan is the whole deprecation descriptor.
type DispatchRoutePlan struct {
	Version            int                  `json:"version"`
	DeprecatedWorkflow string               `json:"deprecated_workflow"`
	DeprecatedRepo     string               `json:"deprecated_repo"`
	NativeTool         string               `json:"native_tool"`
	NativeTools        []string             `json:"native_tools"`
	RecallTools        []string             `json:"recall_tools"`
	VerifiedHost       string               `json:"verified_host"`
	Phases             []DispatchRoutePhase `json:"phases"`
}

// DispatchRoutePlanSnapshot returns the plan ci#757 is tracked against.
func DispatchRoutePlanSnapshot() DispatchRoutePlan {
	return DispatchRoutePlan{
		Version:            1,
		DeprecatedWorkflow: LegacyDispatchWorkflow,
		DeprecatedRepo:     LegacyDispatchRepo,
		NativeTool:         NativeDispatchTool,
		NativeTools:        append([]string{}, NativeDispatchTools...),
		RecallTools:        append([]string{}, InteractionRecallTools...),
		VerifiedHost:       "ci.msgmate.io",
		Phases: []DispatchRoutePhase{
			{
				ID:    "measure_route_usage",
				Order: 0,
				Title: "Measure which route past runs actually used",
				Goal: "Bucket historical tool calls into legacy workflow dispatches (a coder_bash `gh workflow run`), " +
					"native hive-mind dispatches and observation/recall calls, so the migration is driven by evidence.",
				ExitCriteria: []string{
					"the legacy share and the native share are reported for a transcript export",
					"the chats still on the legacy route are named",
					"the report is reproducible offline (ComputeHiveMindUsage over a transcript export)",
				},
				Tracks: []int{757},
			},
			{
				ID:    "enable_native_route_for_leads",
				Order: 1,
				Title: "Enable the native route for dev-lead chats",
				Goal: "Every development-lead chat on ci.msgmate.io holds the hive_mind_agent_* family plus the " +
					"openchat interaction-recall tools, with an explicit opt-out.",
				ExitCriteria: []string{
					"a lead chat's effective tools include NativeDispatchTools and InteractionRecallTools",
					"a non-lead chat and an opted-out lead chat stay untouched",
					"the deployment kill switch rolls the enablement back fleet-wide",
				},
				Tracks: []int{757},
			},
			{
				ID:    "verify_round_trip_on_ci",
				Order: 2,
				Title: "Verify dispatch -> observe -> continue/stop on ci.msgmate.io",
				Goal: "Prove on the live deployment that a lead can start a peer agent, read its transcript, steer and " +
					"stop it without a GitHub round-trip, and that old chat context is searchable.",
				ExitCriteria: []string{
					"a dispatch returned a chat_uuid that observe could read",
					"continue and stop reached the dispatched interaction",
					"an interaction-history search returned a past development chat",
					"no shell `gh workflow run` was needed for the round trip",
				},
				Tracks: []int{757},
			},
			{
				ID:    "retire_workflow_from_prompt",
				Order: 3,
				Title: "Retire the workflow from the lead's prompt/config",
				Goal: "The lead no longer shells out to the workflow; the workflow stays in the private repo as the " +
					"reproducible fallback for a human operator.",
				ExitCriteria: []string{
					"the lead prompt/config names the native tool family as the trigger route",
					"the flipping version is recorded so the change is revertible",
					"the legacy path is documented as fallback-only, not removed",
				},
				Tracks: []int{757},
			},
		},
	}
}

// DispatchRouteEvidence is what a live deployment has to show before the
// workflow stops being the lead's route. It carries host names only.
type DispatchRouteEvidence struct {
	Host                 string   `json:"host"`
	NativeToolsEnabled   bool     `json:"native_tools_enabled"`
	RoundTripVerified    bool     `json:"round_trip_verified"`
	RecallSearchVerified bool     `json:"recall_search_verified"`
	StoppedInteractions  []string `json:"stopped_interactions,omitempty"`
	LegacyCallsAfter     int      `json:"legacy_calls_after,omitempty"`
}

// DispatchRouteBlockers lists why the workflow may not be retired yet. An empty
// result is the go-ahead; the ordering is stable for run evidence.
func DispatchRouteBlockers(evidence DispatchRouteEvidence) []string {
	blockers := []string{}
	if strings.TrimSpace(evidence.Host) == "" {
		blockers = append(blockers, "no host: evidence must name the deployment it was collected on")
	} else if evidence.Host != "ci.msgmate.io" {
		blockers = append(blockers, fmt.Sprintf("evidence was collected on %q, not on ci.msgmate.io", evidence.Host))
	}
	if !evidence.NativeToolsEnabled {
		blockers = append(blockers, "the hive_mind_agent_* family is not enabled for the dev-lead chat")
	}
	if !evidence.RoundTripVerified {
		blockers = append(blockers, "dispatch -> observe -> continue/stop was never verified on the deployment")
	}
	if !evidence.RecallSearchVerified {
		blockers = append(blockers, "searching old chat context from a dev-agent chat was never verified")
	}
	if len(evidence.StoppedInteractions) == 0 {
		blockers = append(blockers, "no dispatched interaction was stopped through the native route")
	}
	if evidence.LegacyCallsAfter > 0 {
		blockers = append(blockers, fmt.Sprintf("the legacy workflow was still used %d time(s) after verification", evidence.LegacyCallsAfter))
	}
	return blockers
}

// DispatchRouteReady reports whether the native route may replace the workflow.
func DispatchRouteReady(evidence DispatchRouteEvidence) bool {
	return len(DispatchRouteBlockers(evidence)) == 0
}

// IsDevLeadConfig reports whether a shared config declares a development lead
// chat: a dev-lead role key, a dev-lead dispatch role, or the recovery/admin
// role deliberately NOT (a restricted recovery bot must not gain sub-agents).
func IsDevLeadConfig(config map[string]interface{}) bool {
	if config == nil {
		return false
	}
	want := func(raw interface{}) bool {
		value, _ := raw.(string)
		value = strings.ToLower(strings.TrimSpace(value))
		for _, role := range devLeadRoles {
			if value == role {
				return true
			}
		}
		return false
	}
	flag := func(raw interface{}) bool {
		switch value := raw.(type) {
		case bool:
			return value
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1", "yes", "on":
				return true
			}
		}
		return false
	}
	for _, key := range devLeadConfigKeys {
		if want(config[key]) {
			return true
		}
		if key != "coder_role" && flag(config[key]) {
			return true
		}
	}
	if meta := dispatchMetaFromConfig(config); meta != nil {
		if want(meta["role"]) {
			return true
		}
	}
	return false
}

// HiveMindToolsDisabled reports whether the deployment-wide kill switch is on.
func HiveMindToolsDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "yes", "disabled":
		return true
	}
	return false
}

// HiveMindToolsEnvValue returns the raw kill-switch value.
func HiveMindToolsEnvValue() string { return os.Getenv(HiveMindToolsEnvKey) }

// ApplyDevLeadDispatchTools enables the native trigger route and the old-chat
// recall family for a development-lead chat, exactly like an integration
// SharedConfigDefaults contribution.
//
// It is selective and non-destructive: a config that does not declare a dev
// lead is never touched, a config whose `tools` array is absent/empty is left
// alone (an empty `tools: []` means "not configured", ci#617, so the
// compiled-in defaults apply), an already listed tool is never duplicated, and
// `hive_mind_tools: false` or the deployment kill switch opts the chat out.
func ApplyDevLeadDispatchTools(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	if !IsDevLeadConfig(config) {
		return config
	}
	raw, exists := config["tools"]
	tools := stringSliceFromConfig(raw)
	if !exists || len(tools) == 0 {
		return config
	}
	if boolishValue(config[HiveMindToolsConfigKey], true) == false {
		return config
	}
	if HiveMindToolsDisabled(HiveMindToolsEnvValue()) {
		return config
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool] = true
	}
	for _, tool := range append(append([]string{}, NativeDispatchTools...), InteractionRecallTools...) {
		if !seen[tool] {
			tools = append(tools, tool)
			seen[tool] = true
		}
	}
	config["tools"] = tools
	return config
}

// HiveMindCallSample is one historical tool call. Outcome is optional: a failed
// hive-mind call is evidence that the route exists but does not work yet.
type HiveMindCallSample struct {
	Tool      string `json:"tool"`
	Arguments string `json:"arguments,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Chat      string `json:"chat,omitempty"`
	Date      string `json:"date,omitempty"`
	Failed    bool   `json:"failed,omitempty"`
}

// HiveMindUsageReport is the adoption metric of both routes.
type HiveMindUsageReport struct {
	Version             int      `json:"version"`
	Samples             int      `json:"samples"`
	NativeCalls         int      `json:"native_calls"`
	LegacyCalls         int      `json:"legacy_calls"`
	ObserveCalls        int      `json:"observe_calls"`
	RecallCalls         int      `json:"recall_calls"`
	FailedNativeCalls   int      `json:"failed_native_calls"`
	DistinctChats       int      `json:"distinct_chats"`
	DistinctNativeChats int      `json:"distinct_native_chats"`
	LegacyShare         float64  `json:"legacy_share"`
	NativeShare         float64  `json:"native_share"`
	LegacyChats         []string `json:"legacy_chats"`
	NativeChats         []string `json:"native_chats"`
	RecallChats         []string `json:"recall_chats"`
	Verdict             string   `json:"verdict"`
}

// ClassifyDispatchRouteCall buckets one sample: "legacy" (a shelled-out
// `gh workflow run` of the deprecated workflow), "native" (a hive-mind
// dispatch/lifecycle call), "observe", "recall" (old-chat search) or "other".
func ClassifyDispatchRouteCall(sample HiveMindCallSample) string {
	tool := strings.TrimSpace(sample.Tool)
	args := sample.Arguments
	switch {
	case tool == "coder_bash" || tool == "coder_git":
		if strings.Contains(args, "gh workflow run") && strings.Contains(args, LegacyDispatchWorkflow) {
			return "legacy"
		}
		return "other"
	case strings.HasPrefix(tool, "hive_mind_agent_"):
		return "native"
	case strings.HasPrefix(tool, "hive_mind_observe_"):
		return "observe"
	case strings.HasPrefix(tool, "hive_mind_state_"), tool == "hive_mind_influence":
		return "native"
	case strings.Contains(tool, "openchat_search"), strings.Contains(tool, "openchat_list_interactions"), strings.Contains(tool, "openchat_get_interaction"):
		return "recall"
	}
	return "other"
}

// ComputeHiveMindUsage measures how the two routes are used in a transcript
// export, so "are the hive-mind tools actually used yet?" is answered by
// evidence instead of by impression.
func ComputeHiveMindUsage(samples []HiveMindCallSample) HiveMindUsageReport {
	report := HiveMindUsageReport{Version: 1, Samples: len(samples)}
	chats, legacyChats, nativeChats, recallChats := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, sample := range samples {
		chat := strings.TrimSpace(sample.Chat)
		if chat != "" {
			chats[chat] = true
		}
		switch ClassifyDispatchRouteCall(sample) {
		case "legacy":
			report.LegacyCalls++
			if chat != "" {
				legacyChats[chat] = true
			}
		case "native":
			report.NativeCalls++
			if sample.Failed {
				report.FailedNativeCalls++
			}
			if chat != "" {
				nativeChats[chat] = true
			}
		case "observe":
			report.ObserveCalls++
			if chat != "" {
				nativeChats[chat] = true
			}
		case "recall":
			report.RecallCalls++
			if chat != "" {
				recallChats[chat] = true
			}
		}
	}
	triggers := report.NativeCalls + report.LegacyCalls
	if triggers > 0 {
		report.LegacyShare = float64(report.LegacyCalls) / float64(triggers)
		report.NativeShare = float64(report.NativeCalls) / float64(triggers)
	}
	report.DistinctChats = len(chats)
	report.DistinctNativeChats = len(nativeChats)
	report.LegacyChats = sortedKeys(legacyChats)
	report.NativeChats = sortedKeys(nativeChats)
	report.RecallChats = sortedKeys(recallChats)
	switch {
	case triggers == 0:
		report.Verdict = "no trigger calls in the export: set the tools up and re-export before retiring the workflow"
	case report.NativeCalls == 0:
		report.Verdict = "the native route is unused: the workflow is still the only trigger route"
	case report.LegacyCalls > 0:
		report.Verdict = "both routes are in use: keep the workflow until the legacy share reaches zero"
	case report.FailedNativeCalls > 0:
		report.Verdict = "the native route is used but failing: fix it before retiring the workflow"
	case report.RecallCalls == 0:
		report.Verdict = "native dispatch works, old-chat recall is unused: enable the interaction-history tools too"
	default:
		report.Verdict = "the native route carries all dispatches and old-chat recall is in use: the workflow may be retired"
	}
	return report
}

// sortedKeys renders a set deterministically for run evidence.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// RenderDispatchRouteMarkdown renders the plan, the gate state and the usage
// metric for a ticket comment or runbook. It omits the usage metric when no
// report was measured.
func RenderDispatchRouteMarkdown(evidence DispatchRouteEvidence, usage *HiveMindUsageReport) string {
	plan := DispatchRoutePlanSnapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "# Native agent dispatch / retiring the workflow-only route (ci#757)\n\n")
	fmt.Fprintf(&b, "plan v%d: %s (%s) -> %s\n\n", plan.Version, plan.DeprecatedWorkflow, plan.DeprecatedRepo, plan.NativeTool)
	fmt.Fprintf(&b, "verified host: %s\n\n", plan.VerifiedHost)
	for _, phase := range plan.Phases {
		fmt.Fprintf(&b, "## %d. %s (%s)\n%s\n\n", phase.Order, phase.Title, phase.ID, phase.Goal)
		for _, criteria := range phase.ExitCriteria {
			fmt.Fprintf(&b, "- %s\n", criteria)
		}
		b.WriteString("\n")
	}
	blockers := DispatchRouteBlockers(evidence)
	if len(blockers) == 0 {
		b.WriteString("gate: ready - the workflow may be retired from the lead's route\n")
	} else {
		b.WriteString("gate: blocked\n")
		for _, blocker := range blockers {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
	}
	if usage != nil {
		fmt.Fprintf(&b, "\nusage (plan v%d): %d native / %d legacy dispatch calls, native share %.1f%%, %d chats on the native route, %d recall calls\n",
			usage.Version, usage.NativeCalls, usage.LegacyCalls, usage.NativeShare*100, usage.DistinctNativeChats, usage.RecallCalls)
		fmt.Fprintf(&b, "verdict: %s\n", usage.Verdict)
	}
	return b.String()
}

// DispatchRouteJSON renders the gate + usage as JSON for run evidence.
func DispatchRouteJSON(evidence DispatchRouteEvidence, usage *HiveMindUsageReport) string {
	payload := map[string]interface{}{
		"plan":     DispatchRoutePlanSnapshot(),
		"evidence": evidence,
		"blockers": DispatchRouteBlockers(evidence),
		"ready":    DispatchRouteReady(evidence),
	}
	if usage != nil {
		payload["usage"] = usage
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
