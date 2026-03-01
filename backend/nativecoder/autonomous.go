package nativecoder

import "strings"

// This file owns the core-side half of the autonomous-confirmation contract
// (ci#506): a native coder interaction that runs headless must never park on a
// per-action confirmation, so the *chat's* shared config has to state the mode
// the coder integration reads.
//
// The integration resolves the interaction mode from the chat's
// `coder_interaction` key (msgmate-coder#62): `autonomous` lifts the per-action
// gate on the mutating tools (`coder_bash`/`coder_write`/`coder_edit`/
// `coder_patch`/`coder_git`) and auto-answers `coder_ask`. What was missing on
// the core side is the wiring of the trusted agent-dispatch surface: a dispatch
// with `autonomous: true` (the workflow-equivalent option, and the ci#532
// default for the native dev agents) only recorded the flag under
// `agent_dispatch` and never made the interaction autonomous, so a dispatched
// dev agent still parked on "Waiting for your confirmation to run `coder_bash`"
// until a human (or the auto-approve harness) approved each call.
//
// The policy here is deliberately narrow and explicit:
//
//   - an already configured `coder_interaction` value always wins;
//   - an explicit `agent_dispatch.autonomous` request is promoted into
//     `coder_interaction: autonomous`, so the contract the integration reads is
//     present on the chat itself;
//   - `agent_dispatch.approve_confirmations` is reported as a no-park
//     interaction (the dispatcher stands in as the confirming user) but is
//     *not* promoted: it keeps the narrower semantics the integration already
//     implements for that flag, where a question (`coder_ask`) is still asked
//     and answered by the confirming dispatcher instead of the agent answering
//     itself.

const (
	// CoderInteractionConfigKey is the shared-config key a native coder
	// interaction states its mode in. It is the top-level equivalent of the
	// coder session's interaction field.
	CoderInteractionConfigKey = "coder_interaction"
	// CoderInteractionAutonomous is the mode value that makes an interaction
	// run without pausing for the user (no per-action confirmations, questions
	// auto-answered).
	CoderInteractionAutonomous = "autonomous"
	// CoderInteractionInteractive is the default mode: per-action confirmation
	// for the mutating tools.
	CoderInteractionInteractive = "interactive"

	// AgentDispatchConfigKey is the shared-config block the trusted
	// agent-dispatch surface records its workflow-equivalent options in.
	AgentDispatchConfigKey = "agent_dispatch"
	// AgentDispatchAutonomousKey is the block's `autonomous` flag.
	AgentDispatchAutonomousKey = "autonomous"
	// AgentDispatchApproveConfirmationsKey is the block's
	// `approve_confirmations` flag.
	AgentDispatchApproveConfirmationsKey = "approve_confirmations"
)

// Autonomous policy sources, reported by ResolveAutonomousPolicy so a caller
// (the agent-facing interaction view, a test) can tell *why* an interaction
// runs without pausing for the user.
const (
	// AutonomousSourceCoderInteraction is the explicit chat mode.
	AutonomousSourceCoderInteraction = "coder_interaction"
	// AutonomousSourceDispatchAutonomous is a dispatch that asked for
	// `autonomous: true`.
	AutonomousSourceDispatchAutonomous = "agent_dispatch.autonomous"
	// AutonomousSourceDispatchApproveConfirmations is a dispatch whose
	// confirming user stands in for the agent (`approve_confirmations`).
	AutonomousSourceDispatchApproveConfirmations = "agent_dispatch.approve_confirmations"
)

// AutonomousPolicy is the resolved no-confirm policy of a chat shared config.
type AutonomousPolicy struct {
	// Enabled reports whether the interaction runs without parking on a
	// per-action confirmation.
	Enabled bool
	// Source names the config key the decision came from (empty when the
	// interaction keeps its per-action gate).
	Source string
}

// NormalizeCoderInteraction coerces a raw interaction mode to one of the two
// supported values. Anything that is not `autonomous` (including an empty
// value) normalizes to `interactive`, which keeps the per-action gate.
func NormalizeCoderInteraction(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), CoderInteractionAutonomous) {
		return CoderInteractionAutonomous
	}
	return CoderInteractionInteractive
}

// CoderInteractionFromConfig resolves the interaction mode a chat shared config
// declares. An absent or invalid value resolves to `interactive`.
func CoderInteractionFromConfig(config map[string]interface{}) string {
	if config == nil {
		return CoderInteractionInteractive
	}
	return NormalizeCoderInteraction(stringValue(config[CoderInteractionConfigKey]))
}

// CoderInteractionAutonomousForConfig reports whether a chat shared config
// declares the interaction `autonomous`.
func CoderInteractionAutonomousForConfig(config map[string]interface{}) bool {
	return CoderInteractionFromConfig(config) == CoderInteractionAutonomous
}

// dispatchMetaFromConfig returns the `agent_dispatch` block of a chat shared
// config, or nil when the chat was not created through the dispatch surface.
func dispatchMetaFromConfig(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	raw, ok := config[AgentDispatchConfigKey].(map[string]interface{})
	if !ok {
		return nil
	}
	return raw
}

// dispatchAskedAutonomous reports whether the recorded dispatch options asked
// for an autonomous run (`autonomous: true`).
func dispatchAskedAutonomous(config map[string]interface{}) bool {
	dispatch := dispatchMetaFromConfig(config)
	if dispatch == nil {
		return false
	}
	return boolishValue(dispatch[AgentDispatchAutonomousKey], false)
}

// dispatchApprovesConfirmations reports whether the recorded dispatch options
// asked for `approve_confirmations: true` — a dispatcher that acts as the
// confirming user, so the interaction never waits on a human, but keeps the
// narrower semantics of that flag (a question is still asked, the dispatcher
// answers it).
func dispatchApprovesConfirmations(config map[string]interface{}) bool {
	dispatch := dispatchMetaFromConfig(config)
	if dispatch == nil {
		return false
	}
	return boolishValue(dispatch[AgentDispatchApproveConfirmationsKey], false)
}

// ResolveAutonomousPolicy resolves whether a chat shared config makes the
// interaction run without pausing for the user, and which key decided it.
//
// The explicit chat mode wins; the dispatch options are honoured as the
// workflow-equivalent fallback, because the trusted dispatch surface is the
// in-product counterpart of the dispatcher workflow that used to upgrade the
// chat to a trusted session.
func ResolveAutonomousPolicy(config map[string]interface{}) AutonomousPolicy {
	if CoderInteractionAutonomousForConfig(config) {
		return AutonomousPolicy{Enabled: true, Source: AutonomousSourceCoderInteraction}
	}
	if dispatchAskedAutonomous(config) {
		return AutonomousPolicy{Enabled: true, Source: AutonomousSourceDispatchAutonomous}
	}
	if dispatchApprovesConfirmations(config) {
		return AutonomousPolicy{Enabled: true, Source: AutonomousSourceDispatchApproveConfirmations}
	}
	return AutonomousPolicy{}
}

// AutonomousEnabledForConfig reports whether a chat shared config makes the
// interaction run without pausing for a per-action confirmation. It is the one
// call the agent-facing interaction view needs.
func AutonomousEnabledForConfig(config map[string]interface{}) bool {
	return ResolveAutonomousPolicy(config).Enabled
}

// ApplyCoderAutonomousConfig promotes an explicit `agent_dispatch.autonomous`
// request into the `coder_interaction: autonomous` mode on the chat's effective
// shared config, so the contract the coder integration reads is stated on the
// chat itself instead of only inside the dispatch bookkeeping (ci#506).
//
// An already configured `coder_interaction` value always wins, a dispatch
// without an explicit autonomous request is left untouched (the interaction
// keeps its per-action gate), and `approve_confirmations` is deliberately not
// promoted — see the file comment. The config is modified in place and returned
// for chaining, mirroring ApplyAutoConsolidationDefault.
func ApplyCoderAutonomousConfig(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	if _, exists := config[CoderInteractionConfigKey]; exists {
		return config
	}
	if !dispatchAskedAutonomous(config) {
		return config
	}
	config[CoderInteractionConfigKey] = CoderInteractionAutonomous
	return config
}
