package nativecoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ci#757: the `trigger-open-chat-coding-interaction.yaml` workflow of the
// private `msgmate-io/open-chat-go-ci` repository is the *legacy* route a
// coordinating agent used to start another coding agent (it shells out through
// `coder_bash` with `gh workflow run` and lets GitHub deliver the trigger).
// Because it was the only documented route, agents kept using it instead of the
// native in-server surface (`POST /agents/dispatch`, exposed as the
// `hive_mind_agent_*` tool family), which is exactly the reported symptom.
//
// This file makes the migration a fact instead of a promise:
//
//   - `LegacyDispatchInputParity` maps every workflow input onto the native
//     equivalent, and `LegacyDispatchParityGaps` fails while an input has no
//     native counterpart - so "the native route is complete" is checked, not
//     asserted (this is the "investigate why the workflow is still used" half:
//     a gap is a reason to keep the workflow);
//   - `DevAgentToolProfile` resolves the tool list for a development-agent chat
//     by role, so the dispatch family and the past-chat-context search family
//     are enabled selectively (lead/dev chats get both, a leaf worker gets
//     context recall only);
//   - `NativeDispatchBlockers` / `NativeDispatchEvidence` turn "may the
//     workflow-only route be retired?" into data over the observed chats, so the
//     retirement cannot be claimed while a lead chat still routes through
//     GitHub.
//
// Nothing here carries a secret, a token or a host credential: the gate works
// on chat metadata (tool lists, observed round-trips, prompt references).

// The legacy route, addressed by repository and file name so a reference to it
// is greppable and can be checked for absence from a lead chat's config.
const (
	// LegacyDispatchWorkflowRepo is the private repository that hosts it.
	LegacyDispatchWorkflowRepo = "msgmate-io/open-chat-go-ci"
	// LegacyDispatchWorkflowName is the workflow file to deprecate.
	LegacyDispatchWorkflowName = "trigger-open-chat-coding-interaction.yaml"
	// LegacyDispatchInvocation is the shell fragment that identifies a config
	// or prompt still dispatching through GitHub.
	LegacyDispatchInvocation = "gh workflow run"
)

// The native surface that replaces the workflow.
const (
	// NativeDispatchRoute is the in-server route the native dispatch uses.
	NativeDispatchRoute = "POST /agents/dispatch"
	// NativeDispatchTool is the tool that starts a peer agent natively.
	NativeDispatchTool = "hive_mind_agent_dispatch"
	// NativeDispatchConfigKey is the shared-config block the dispatch records
	// its options in (consumed by autonomous.go).
	NativeDispatchConfigKey = AgentDispatchConfigKey
)

// NativeDispatchToolFamily is the full dispatch + observation tool family of
// the hive-mind integration. Order is the migration order: start a peer, watch
// it, read what it produced, steer or stop it.
var NativeDispatchToolFamily = []string{
	"hive_mind_agent_dispatch",
	"hive_mind_agent_list",
	"hive_mind_agent_get",
	"hive_mind_observe_list",
	"hive_mind_observe_get",
	"hive_mind_agent_continue",
	"hive_mind_agent_stop",
}

// ChatContextRecallToolFamily is the interaction-history family that answers
// "what did we already try on this issue?" from inside a dev-agent chat. The
// MCP prefix is the one documented in DEVELOPMENT.md.
var ChatContextRecallToolFamily = []string{
	"mcp:open-chat-opencode-mcp:openchat_list_interactions",
	"mcp:open-chat-opencode-mcp:openchat_get_interaction",
	"mcp:open-chat-opencode-mcp:openchat_search_interactions",
	"mcp:open-chat-opencode-mcp:openchat_search_interaction_messages",
}

// DevAgentRoleLead is the coordinating agent role that dispatches peers.
const DevAgentRoleLead = "lead"

// DevAgentToolProfile resolves the tool list a development-agent chat should
// carry, per role. It is deliberately narrow:
//
//   - the coordinating role gets the dispatch family plus context recall;
//   - a leaf worker (any other known role) gets context recall only, so a
//     worker that implements one issue cannot spawn peers;
//   - an unknown role gets an error instead of a silent widening.
//
// The returned slice is a fresh copy, so a caller cannot mutate the families.
func DevAgentToolProfile(role string) ([]string, error) {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		normalized = DevAgentRoleLead
	}
	known := false
	for _, candidate := range knownRoles {
		if candidate == normalized {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("unknown development-agent role %q (known: %s)", role, strings.Join(knownRoles, ", "))
	}
	tools := append([]string(nil), ChatContextRecallToolFamily...)
	if normalized == DevAgentRoleLead {
		tools = append(tools, NativeDispatchToolFamily...)
	}
	return tools, nil
}

// LegacyDispatchInput is one input of the legacy workflow plus the native
// equivalent that carries it. Equivalent is a tool name or a documented
// dispatch/loop option; it is free text on purpose, because the point is that a
// human can read the mapping, while `Gap` is what the checker uses.
type LegacyDispatchInput struct {
	// Name is the workflow input name.
	Name string
	// Purpose is why the workflow had it.
	Purpose string
	// Equivalent is the native counterpart.
	Equivalent string
	// Gap is non-empty while the native route cannot express the input; the
	// workflow must then be kept as the fallback for that case.
	Gap string
}

// LegacyDispatchInputParity is the input-by-input parity registry. It is the
// answer to "why do agents still use the workflow?": every entry with a Gap is
// a capability the native surface does not yet express, and the gate below
// refuses to retire the route while one is open.
func LegacyDispatchInputParity() []LegacyDispatchInput {
	return []LegacyDispatchInput{
		{Name: "message", Purpose: "the task handed to the coding agent",
			Equivalent: NativeDispatchTool + " task argument"},
		{Name: "bot", Purpose: "which bot the interaction starts",
			Equivalent: "hive_mind_agent_dispatch bot argument"},
		{Name: "tags", Purpose: "repo/issue tags on the interaction for per-issue dedupe",
			Equivalent: "hive_mind_agent_dispatch tags argument"},
		{Name: "host", Purpose: "the Open-Chat server to talk to",
			Equivalent: "in-server dispatch (" + NativeDispatchRoute + "), no remote host needed"},
		{Name: "wait", Purpose: "wait for the turn and print the reply",
			Equivalent: "hive_mind_observe_list / hive_mind_observe_get"},
		{Name: "wait_timeout", Purpose: "rolling wait window for the reply",
			Equivalent: "auto-continue wait policy on the dispatched chat"},
		{Name: "max_wall_seconds", Purpose: "absolute bound on the wait",
			Equivalent: "auto-continue wall bound (max_continuations)"},
		{Name: "max_wall_rearms", Purpose: "cap on re-arming the wall",
			Equivalent: "auto-continue re-arm policy"},
		{Name: "resume_horizon_seconds", Purpose: "how long a parked turn counts as progress",
			Equivalent: "auto-continue resumable window"},
		{Name: "resume_chat_uuid", Purpose: "re-attach to a live-but-orphaned interaction",
			Equivalent: "hive_mind_agent_continue on that chat_uuid"},
		{Name: "approve_confirmations", Purpose: "auto-approve confirmation-gated calls",
			Equivalent: "hive_mind_agent_dispatch approve_confirmations option"},
		{Name: "max_confirmations", Purpose: "cap on auto-approved confirmations",
			Equivalent: "hive_mind_agent_dispatch max_confirmations option"},
		{Name: "autonomous", Purpose: "trust=allow once the workspace is bound",
			Equivalent: "hive_mind_agent_dispatch autonomous option"},
		{Name: "auto_continue", Purpose: "keep the agent working across turns",
			Equivalent: "hive_mind_agent_dispatch auto_continue option"},
		{Name: "max_continuations", Purpose: "cap on auto-continuations",
			Equivalent: "hive_mind_agent_dispatch max_continuations option"},
		{Name: "stall_timeout", Purpose: "when a missing turn counts as a stall",
			Equivalent: "native auto-continue stall detection"},
		{Name: "stall_max_retries", Purpose: "stall retries before marking blocked",
			Equivalent: "native auto-continue stall retry bound"},
		{Name: "sandbox_error_max_attempts", Purpose: "tolerated provisioning failures",
			Equivalent: "native sandbox-provisioning retry bound"},
		{Name: "sandbox_error_base_delay", Purpose: "provisioning back-off base",
			Equivalent: "native sandbox-provisioning back-off"},
		{Name: "sandbox_error_max_delay", Purpose: "provisioning back-off ceiling",
			Equivalent: "native sandbox-provisioning back-off ceiling"},
		{Name: "provider_error_base_delay", Purpose: "provider back-off base",
			Equivalent: "native provider-error back-off"},
		{Name: "provider_error_max_delay", Purpose: "provider back-off ceiling",
			Equivalent: "native provider-error back-off ceiling"},
	}
}

// LegacyDispatchParityGaps returns the workflow inputs with no native
// equivalent, sorted, so the reason to keep the workflow is explicit.
func LegacyDispatchParityGaps() []string {
	gaps := []string{}
	for _, input := range LegacyDispatchInputParity() {
		if strings.TrimSpace(input.Gap) != "" {
			gaps = append(gaps, input.Name+": "+input.Gap)
		}
	}
	sort.Strings(gaps)
	return gaps
}

// LegacyDispatchReferences reports whether a config/prompt text still routes
// through the GitHub workflow. It matches the workflow file name and the
// `gh workflow run` invocation, so a chat that only *mentions* the native route
// does not trip it.
func LegacyDispatchReferences(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, LegacyDispatchWorkflowName) ||
		strings.Contains(lower, LegacyDispatchInvocation)
}

// NativeDispatchChat is the observed dispatch state of ONE development-agent
// chat. It is metadata only (no transcript, no secret).
type NativeDispatchChat struct {
	// Name identifies the chat/bot in the fleet (eg "lead").
	Name string `json:"name"`
	// Repo is the repository the chat works on.
	Repo string `json:"repo,omitempty"`
	// Role is the chat's development-agent role.
	Role string `json:"role,omitempty"`
	// Tools is the enabled tool list of the chat.
	Tools []string `json:"tools,omitempty"`
	// DispatchRoundTrip is true when a live dispatch -> observe ->
	// continue/stop round-trip was observed for this chat.
	DispatchRoundTrip bool `json:"dispatch_round_trip,omitempty"`
	// ContextRecallRoundTrip is true when a past-run question was answered from
	// the interaction-history tool output.
	ContextRecallRoundTrip bool `json:"context_recall_round_trip,omitempty"`
	// LegacyWorkflowReferences is true while the chat's prompt/config still
	// dispatches through the GitHub workflow.
	LegacyWorkflowReferences bool `json:"legacy_workflow_references,omitempty"`
}

// NativeDispatchState is the observed fleet the gate runs over.
type NativeDispatchState struct {
	Chats []NativeDispatchChat `json:"chats"`
}

// NativeDispatchStateJSON renders the observed fleet as the JSON document the
// `native_dispatch_report` job reads back, so the report and the gate always
// agree on the shape.
func NativeDispatchStateJSON(state NativeDispatchState) (string, error) {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// hasTool reports whether the chat enables the tool.
func (c NativeDispatchChat) hasTool(tool string) bool {
	for _, enabled := range c.Tools {
		if strings.EqualFold(strings.TrimSpace(enabled), tool) {
			return true
		}
	}
	return false
}

// hasTools reports how many of the family are enabled and names the missing
// ones, so a blocker can say exactly what to add to the config.
func (c NativeDispatchChat) missingTools(family []string) []string {
	missing := []string{}
	for _, tool := range family {
		if !c.hasTool(tool) {
			missing = append(missing, tool)
		}
	}
	return missing
}

// NativeDispatchChatBlockers returns the reasons a single chat is not on the
// native route. An empty result means the chat is migrated.
func NativeDispatchChatBlockers(chat NativeDispatchChat) []string {
	blockers := []string{}
	if missing := chat.missingTools(ChatContextRecallToolFamily); len(missing) > 0 {
		blockers = append(blockers, fmt.Sprintf("chat %q does not enable the past-chat-context search family (%s)", chat.Name, strings.Join(missing, ", ")))
	}
	role := strings.ToLower(strings.TrimSpace(chat.Role))
	if role == "" {
		role = DevAgentRoleLead
	}
	if role == DevAgentRoleLead {
		if missing := chat.missingTools(NativeDispatchToolFamily); len(missing) > 0 {
			blockers = append(blockers, fmt.Sprintf("coordinating chat %q does not enable the native dispatch family (%s)", chat.Name, strings.Join(missing, ", ")))
		}
		if !chat.DispatchRoundTrip {
			blockers = append(blockers, fmt.Sprintf("chat %q has no observed dispatch -> observe -> continue/stop round-trip on the native route", chat.Name))
		}
	}
	if !chat.ContextRecallRoundTrip {
		blockers = append(blockers, fmt.Sprintf("chat %q has no observed past-chat-context search round-trip", chat.Name))
	}
	if chat.LegacyWorkflowReferences {
		blockers = append(blockers, fmt.Sprintf("chat %q still references %s (%s) instead of %s", chat.Name, LegacyDispatchWorkflowName, LegacyDispatchInvocation, NativeDispatchTool))
	}
	return blockers
}

// NativeDispatchBlockers returns the fleet-wide blockers of retiring the
// workflow-only dispatch route. A parity gap is fleet-wide by nature; per-chat
// blockers are prefixed with the chat name so one list is actionable.
func NativeDispatchBlockers(state NativeDispatchState) []string {
	blockers := []string{}
	for _, gap := range LegacyDispatchParityGaps() {
		blockers = append(blockers, "legacy workflow input has no native equivalent - "+gap)
	}
	if len(state.Chats) == 0 {
		blockers = append(blockers, "no development-agent chat was observed, so no chat is verified on the native route")
	}
	for _, chat := range state.Chats {
		blockers = append(blockers, NativeDispatchChatBlockers(chat)...)
	}
	sort.Strings(blockers)
	return blockers
}

// NativeDispatchReady reports whether the workflow-only route may be retired:
// every input is expressible natively and every observed chat is migrated.
func NativeDispatchReady(state NativeDispatchState) bool {
	return len(NativeDispatchBlockers(state)) == 0
}

// NativeDispatchStepStatus values.
const (
	// NativeDispatchStepDone marks a migration step with no blocker left.
	NativeDispatchStepDone = "done"
	// NativeDispatchStepBlocked marks a step whose evidence is still missing.
	NativeDispatchStepBlocked = "blocked"
)

// NativeDispatchStep is one step of the ci#757 checklist plus its gate result.
type NativeDispatchStep struct {
	ID     string
	Title  string
	Status string
	Detail []string
}

// nativeDispatchStepPlan is the ordered migration checklist. The last step is
// the retirement itself, so the order is also the contract.
var nativeDispatchStepPlan = []struct {
	ID    string
	Title string
}{
	{"native_tools_enabled", "The coordinating chats carry the native dispatch family"},
	{"dispatch_roundtrip_verified", "A dispatch -> observe -> continue/stop round-trip was observed on ci.msgmate.io"},
	{"context_recall_enabled", "Development-agent chats carry the past-chat-context search family"},
	{"context_recall_verified", "An old-chat-context question was answered from search output"},
	{"native_parity_complete", "Every legacy workflow input has a native equivalent"},
	{"workflow_retired", "No chat references " + LegacyDispatchWorkflowName + " (kept only as documented fallback)"},
}

// nativeDispatchStepFragments maps a step onto the blocker fragments that
// belong to it, matched case-insensitively against the gate lines. The fleet-wide
// "nothing was observed" blocker is attached to every step: with an empty fleet
// there is no evidence for any step, so none may be reported as done.
func nativeDispatchStepFragments(id string) []string {
	fleetWide := []string{"no development-agent chat was observed"}
	switch id {
	case "native_tools_enabled":
		return append(fleetWide, "does not enable the native dispatch family")
	case "dispatch_roundtrip_verified":
		return append(fleetWide, "round-trip on the native route")
	case "context_recall_enabled":
		return append(fleetWide, "past-chat-context search family")
	case "context_recall_verified":
		return append(fleetWide, "context search round-trip")
	case "native_parity_complete":
		return append(fleetWide, "no native equivalent")
	case "workflow_retired":
		return append(fleetWide, "still references", LegacyDispatchWorkflowName)
	default:
		return nil
	}
}

// NativeDispatchEvidence returns one row per checklist step, in checklist order,
// derived from NativeDispatchBlockers so a step cannot be reported as done while
// its blocker is still listed.
func NativeDispatchEvidence(state NativeDispatchState) []NativeDispatchStep {
	blockers := NativeDispatchBlockers(state)
	out := make([]NativeDispatchStep, 0, len(nativeDispatchStepPlan))
	for _, step := range nativeDispatchStepPlan {
		detail := []string{}
		for _, blocker := range blockers {
			lower := strings.ToLower(blocker)
			for _, fragment := range nativeDispatchStepFragments(step.ID) {
				if strings.Contains(lower, strings.ToLower(fragment)) {
					detail = append(detail, blocker)
					break
				}
			}
		}
		status := NativeDispatchStepDone
		if len(detail) > 0 {
			status = NativeDispatchStepBlocked
		}
		out = append(out, NativeDispatchStep{ID: step.ID, Title: step.Title, Status: status, Detail: detail})
	}
	return out
}

// RenderNativeDispatchEvidenceMarkdown renders the checklist as a table plus the
// open blocker list, for a ticket comment. It never prints a credential: the
// evidence is built from chat metadata only.
func RenderNativeDispatchEvidenceMarkdown(state NativeDispatchState) string {
	var b strings.Builder
	b.WriteString("### ci#757 native agent dispatch - migration evidence\n\n")
	b.WriteString("Legacy route: `" + LegacyDispatchWorkflowRepo + "/" + LegacyDispatchWorkflowName + "`. Native route: `" + NativeDispatchRoute + "` via `" + NativeDispatchTool + "`.\n\n")
	b.WriteString("| step | status |\n| --- | --- |\n")
	for _, row := range NativeDispatchEvidence(state) {
		b.WriteString(fmt.Sprintf("| %s | %s |\n", row.Title, row.Status))
	}
	blockers := NativeDispatchBlockers(state)
	if len(blockers) == 0 {
		b.WriteString("\nNo blocker left: the workflow-only dispatch route may be retired.\n")
		return b.String()
	}
	b.WriteString("\nOpen blockers:\n")
	for _, blocker := range blockers {
		b.WriteString("- " + blocker + "\n")
	}
	return b.String()
}
