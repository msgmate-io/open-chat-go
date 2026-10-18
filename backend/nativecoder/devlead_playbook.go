package nativecoder

import (
	"encoding/json"
	"strings"
)

// This file owns the core-side half of ci#733 ("Development lead: fleet
// moderation toolkit + operating playbook").
//
// The native development lead runs as a normal native coder agent, so the two
// things it needs from the *core* are:
//
//   - `devlead_playbook`: a shared-config default that ships the moderation
//     policy (thresholds + the instruction block) to the lead's prompt, the
//     same way `auto_consolidation` and `coder_interaction` are shipped
//     defaults. An already configured value always wins, and a non-coder
//     config is never touched.
//   - `devlead_fleet_audit_thresholds`: the tunable half of that policy, so a
//     deployment can tighten or relax the thrash thresholds without a rebuild.
//
// The deterministic decision function itself lives in fleet_audit.go
// (AuditFleetInteraction), so the policy is unit-testable and identical between
// the audit tool, the rescue tool and any operator script.

const (
	// DevLeadPlaybookConfigKey is the shared-config key carrying the
	// development-lead operating playbook.
	DevLeadPlaybookConfigKey = "devlead_playbook"
	// DevLeadFleetThresholdsConfigKey is the shared-config key carrying the
	// fleet-audit thresholds.
	DevLeadFleetThresholdsConfigKey = "devlead_fleet_audit_thresholds"

	// DevLeadInstructionsKey is the playbook sub-key holding the prompt block.
	DevLeadInstructionsKey = "instructions"
	// DevLeadThresholdsKey is the playbook sub-key holding the thresholds.
	DevLeadThresholdsKey = "thresholds"
	// DevLeadEnabledKey is the playbook sub-key that switches the block off.
	DevLeadEnabledKey = "enabled"

	// DevLeadFleetToolName is the read-only fleet-audit tool.
	DevLeadFleetToolName = "coder_fleet_audit"
	// DevLeadRescueToolName is the stop-and-narrow rescue tool.
	DevLeadRescueToolName = "coder_agent_rescue"
)

// FleetAuditThresholds is the tunable half of the fleet-moderation policy
// (ci#733 Parts A/B). The zero value resolves to the defaults.
type FleetAuditThresholds struct {
	// MaxCalls is the tool-call count after which an edit-less interaction
	// counts as thrashing.
	MaxCalls int `json:"max_calls"`
	// MaxExactRepeats is the exact-duplicate tool-call count that counts as
	// thrashing.
	MaxExactRepeats int `json:"max_exact_repeats"`
	// TerminalStreak is the number of consecutive non-productive terminal
	// reasons that counts as thrashing.
	TerminalStreak int `json:"terminal_streak"`
	// StallSeconds is how long an interaction may go without a turn before it
	// counts as stalled.
	StallSeconds int `json:"stall_seconds"`
	// NarrowCallBudget is the tool-call budget granted on a rescue.
	NarrowCallBudget int `json:"narrow_call_budget"`
}

// DefaultFleetAuditThresholds returns the shipped thresholds.
func DefaultFleetAuditThresholds() FleetAuditThresholds {
	return FleetAuditThresholds{
		MaxCalls:         FleetAuditCallThreshold,
		MaxExactRepeats:  FleetAuditDuplicateThreshold,
		TerminalStreak:   FleetAuditTerminalStreakThreshold,
		StallSeconds:     FleetAuditStallAgeSeconds,
		NarrowCallBudget: FleetAuditNarrowCallBudget,
	}
}

// withDefaults fills every unset field with its default, so a partial config
// (an operator setting only `max_calls`) keeps the rest of the policy intact.
func (t FleetAuditThresholds) withDefaults() FleetAuditThresholds {
	def := DefaultFleetAuditThresholds()
	if t.MaxCalls <= 0 {
		t.MaxCalls = def.MaxCalls
	}
	if t.MaxExactRepeats <= 0 {
		t.MaxExactRepeats = def.MaxExactRepeats
	}
	if t.TerminalStreak <= 0 {
		t.TerminalStreak = def.TerminalStreak
	}
	if t.StallSeconds <= 0 {
		t.StallSeconds = def.StallSeconds
	}
	if t.NarrowCallBudget <= 0 {
		t.NarrowCallBudget = def.NarrowCallBudget
	}
	return t
}

// Map renders the thresholds as the JSON-ish map the shared config and the
// audit tool answer with.
func (t FleetAuditThresholds) Map() map[string]interface{} {
	t = t.withDefaults()
	return map[string]interface{}{
		"max_calls":          t.MaxCalls,
		"max_exact_repeats":  t.MaxExactRepeats,
		"terminal_streak":    t.TerminalStreak,
		"stall_seconds":      t.StallSeconds,
		"narrow_call_budget": t.NarrowCallBudget,
	}
}

// ResolveFleetAuditThresholds reads thresholds from a decoded shared-config
// value (map, JSON string or the playbook block) and fills the defaults in.
func ResolveFleetAuditThresholds(raw interface{}) FleetAuditThresholds {
	out := FleetAuditThresholds{}
	block := sharedConfigBlock(raw)
	if block == nil {
		return out.withDefaults()
	}
	out.MaxCalls = configInt(block, "max_calls")
	out.MaxExactRepeats = configInt(block, "max_exact_repeats")
	out.TerminalStreak = configInt(block, "terminal_streak")
	out.StallSeconds = configInt(block, "stall_seconds")
	out.NarrowCallBudget = configInt(block, "narrow_call_budget")
	return out.withDefaults()
}

// ResolveFleetAuditThresholdsFromConfig reads the thresholds off a whole shared
// config, preferring the dedicated key and falling back to the playbook block.
func ResolveFleetAuditThresholdsFromConfig(config map[string]interface{}) FleetAuditThresholds {
	if config == nil {
		return DefaultFleetAuditThresholds()
	}
	if raw, ok := config[DevLeadFleetThresholdsConfigKey]; ok {
		return ResolveFleetAuditThresholds(raw)
	}
	if raw, ok := config[DevLeadPlaybookConfigKey]; ok {
		if block := sharedConfigBlock(raw); block != nil {
			if thresholds, ok := block[DevLeadThresholdsKey]; ok {
				return ResolveFleetAuditThresholds(thresholds)
			}
		}
	}
	return DefaultFleetAuditThresholds()
}

// sharedConfigBlock accepts the shapes a shared-config sub-value is decoded
// into (map, JSON string, and the playbook's own nesting).
func sharedConfigBlock(raw interface{}) map[string]interface{} {
	switch typed := raw.(type) {
	case map[string]interface{}:
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" || trimmed[0] != '{' {
			return nil
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil
		}
		return decoded
	default:
		return nil
	}
}

// configInt reads an integer-ish value from a decoded config block.
func configInt(block map[string]interface{}, key string) int {
	if block == nil {
		return 0
	}
	switch typed := block[key].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if v, err := typed.Int64(); err == nil {
			return int(v)
		}
	}
	return 0
}

// DevLeadPlaybookText is the instruction block the lead's prompt carries. It is
// deliberately policy-only: the mechanical thresholds live in the config so the
// lead never has to guess them.
func DevLeadPlaybookText() string {
	return strings.Join([]string{
		"## Development-lead playbook (ci#733)",
		"",
		"You own the consolidation train and the agent fleet. Every mechanical decision below is backed by the fleet audit, so audit before you act.",
		"",
		"1. Audit first. Run `coder_fleet_audit` (or the interaction view's audit fields) before touching a managed interaction. It reports tool calls, edits, exact duplicate calls, the last terminal reason and a recommended action.",
		"2. Thrash => stop and narrow. When an interaction hits the thresholds (>=30 tool calls with 0 edits, >=3 exact duplicates, or 2 consecutive `no_usable_tool_call`/`turn_timeout` turns), stop it and resume once with a single objective and a 10-15 tool-call budget: read only the named files, make the smallest edit, run the focused test, report the diff. Never repeat a call that was already made.",
		"3. Stalled or without a summary => continue once. Resume with a request for a one-paragraph status (done / in progress / blocked) and the next concrete step.",
		"4. Finished without a PR => continue once, asking explicitly for the pull request URL.",
		"5. One consolidation PR at a time. Fold finished feature PRs into the open consolidation PR; never repoint or reopen a second one.",
		"6. Required-green only. Merge only when the required check (`Run PR CI Tests and Deployments`) is green; never bypass branch protection.",
		"7. Feature PRs carry only the `test` and `build` labels (optionally `test-core-only`). The full suite (`ai-test-real`, `ai-test-mock`, `sandbox-test`, `e2e`, `deploy`) runs only on the consolidation PR.",
		"8. Capacity guard. Check provider-admission saturation before dispatching or nudging; when the provider is saturated, queue the work instead of piling on.",
		"9. Never wait or poll CI, never push to `main`, and cancel only superseded runs.",
		"10. Report the audit verdict, the action taken and the resulting PR/CI state in every hand-off.",
	}, "\n")
}

// DevLeadPlaybookConfig renders the shipped playbook block.
func DevLeadPlaybookConfig() map[string]interface{} {
	return map[string]interface{}{
		DevLeadEnabledKey:      true,
		DevLeadInstructionsKey: DevLeadPlaybookText(),
		DevLeadThresholdsKey:   DefaultFleetAuditThresholds().Map(),
	}
}

// IsDevLeadAgentConfig reports whether a shared config declares a development
// lead: either the playbook key is present or the agent carries the fleet-audit
// tool.
func IsDevLeadAgentConfig(config map[string]interface{}) bool {
	if config == nil {
		return false
	}
	if _, ok := config[DevLeadPlaybookConfigKey]; ok {
		return true
	}
	if _, ok := config[DevLeadFleetThresholdsConfigKey]; ok {
		return true
	}
	for _, raw := range stringSliceFromConfig(config["tools"]) {
		if raw == DevLeadFleetToolName || raw == DevLeadRescueToolName {
			return true
		}
	}
	return false
}

// ApplyDevLeadPlaybookDefault merges the shipped playbook into a development
// lead's shared config, exactly like an integration SharedConfigDefaults
// contribution:
//
//   - an already configured playbook always wins (including a boolean or a
//     JSON string form);
//   - only a coder config that declares itself as a lead is touched, so an
//     ordinary coder agent never grows the block;
//   - a nil config is returned untouched.
func ApplyDevLeadPlaybookDefault(config map[string]interface{}) map[string]interface{} {
	if config == nil {
		return nil
	}
	if _, exists := config[DevLeadPlaybookConfigKey]; exists {
		return config
	}
	if !IsDevLeadAgentConfig(config) {
		return config
	}
	config[DevLeadPlaybookConfigKey] = DevLeadPlaybookConfig()
	return config
}
