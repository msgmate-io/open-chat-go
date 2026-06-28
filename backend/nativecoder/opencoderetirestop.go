package nativecoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ci#585 umbrella, final step: "opencode bots and integration stopped on ci".
//
// The flip delta of ci#582 points the legacy agent names at the native runtimes
// and keeps opencode as the immediate rollback. This file is the delta that
// removes the opencode integration itself once the per-bot rollout is done,
// plus the record that proves the removal happened. The delta is derived from
// the same flip registry as the retirement gate (NativeBotStages), so a bot
// cannot be stopped without its family being native.
const (
	// OpencodeRetireStopModeDisabled is the cutover mode a completed stop
	// record must have been executed in.
	OpencodeRetireStopModeDisabled = "retired"

	// OpencodeRetireStopKindDisableBackend removes the opencode chat backend.
	OpencodeRetireStopKindDisableBackend = "disable_chat_backend"
	// OpencodeRetireStopKindRemoveBot removes one opencode-era dev bot.
	OpencodeRetireStopKindRemoveBot = "remove_bot"
	// OpencodeRetireStopKindRemoveIntegration removes the opencode integration.
	OpencodeRetireStopKindRemoveIntegration = "remove_integration"
)

// OpencodeRetireStopEntry is one config change of the retirement delta.
type OpencodeRetireStopEntry struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// OpencodeRetireStopKinds returns the accepted entry kinds.
func OpencodeRetireStopKinds() []string {
	return []string{
		OpencodeRetireStopKindDisableBackend,
		OpencodeRetireStopKindRemoveBot,
		OpencodeRetireStopKindRemoveIntegration,
	}
}

// opencodeRetireStopLegacyBots lists every opencode-era bot of the flip
// registry, deduped and sorted, so the delta and the stop record agree on one
// set of bots.
func opencodeRetireStopLegacyBots() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, stage := range NativeBotStages() {
		for _, legacy := range stage.LegacyBots {
			if legacy == stage.NativeRuntime {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(legacy))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, legacy)
		}
	}
	sort.Strings(out)
	return out
}

// OpencodeRetireStopDelta returns the ordered config changes that stop the
// opencode integration: disable the backend, remove its bots, remove the
// integration. It is applied through publish-open-chat-config.yaml
// (vault-sync), DRY_RUN=true first.
func OpencodeRetireStopDelta() []OpencodeRetireStopEntry {
	entries := []OpencodeRetireStopEntry{{
		Kind:   OpencodeRetireStopKindDisableBackend,
		Target: OpenCodeLegacyRuntime,
		Reason: "the native msgmate-coder suite replaces the opencode chat backend",
	}}
	for _, legacy := range opencodeRetireStopLegacyBots() {
		entries = append(entries, OpencodeRetireStopEntry{
			Kind:   OpencodeRetireStopKindRemoveBot,
			Target: legacy,
			Reason: "flipped to its native msgmate-coder runtime (ci#584)",
		})
	}
	entries = append(entries, OpencodeRetireStopEntry{
		Kind:   OpencodeRetireStopKindRemoveIntegration,
		Target: OpenCodeLegacyRuntime,
		Reason: "the opencode integration is no longer needed once every dev bot is native",
	})
	return entries
}

// ValidateOpencodeRetireStopDelta rejects a delta that drifted: unknown kinds,
// empty targets or reasons, duplicates, a native runtime named as a stop
// target, or a fleet bot the delta forgets to stop.
func ValidateOpencodeRetireStopDelta(entries []OpencodeRetireStopEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("retirement stop delta is empty")
	}
	kinds := map[string]bool{}
	for _, kind := range OpencodeRetireStopKinds() {
		kinds[kind] = true
	}
	seen := map[string]bool{}
	backendDisabled := false
	for _, entry := range entries {
		if !kinds[entry.Kind] {
			return fmt.Errorf("unknown retirement stop kind %q", entry.Kind)
		}
		if strings.TrimSpace(entry.Target) == "" {
			return fmt.Errorf("retirement stop entry %q has no target", entry.Kind)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return fmt.Errorf("retirement stop entry %q (%s) has no reason", entry.Kind, entry.Target)
		}
		if IsNativeBotRuntime(entry.Target) {
			return fmt.Errorf("retirement stop entry targets native runtime %q", entry.Target)
		}
		key := entry.Kind + "|" + strings.ToLower(strings.TrimSpace(entry.Target))
		if seen[key] {
			return fmt.Errorf("duplicate retirement stop entry %s", key)
		}
		seen[key] = true
		if entry.Kind == OpencodeRetireStopKindDisableBackend && strings.EqualFold(entry.Target, OpenCodeLegacyRuntime) {
			backendDisabled = true
		}
	}
	if !backendDisabled {
		return fmt.Errorf("retirement stop delta must disable the %s chat backend", OpenCodeLegacyRuntime)
	}
	for _, legacy := range opencodeRetireStopLegacyBots() {
		if !seen[OpencodeRetireStopKindRemoveBot+"|"+strings.ToLower(legacy)] {
			return fmt.Errorf("legacy bot %q is not removed by the retirement stop delta", legacy)
		}
	}
	return nil
}

// OpencodeRetireStopDeltaJSON renders the validated delta for the vault-sync
// job, so a drifted delta fails before it reaches the config publish.
func OpencodeRetireStopDeltaJSON() (string, error) {
	entries := OpencodeRetireStopDelta()
	if err := ValidateOpencodeRetireStopDelta(entries); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// OpencodeRetireStopRecord is the evidence that the stop delta was applied on
// ci: when, by whom, in which cutover mode, and which bots were stopped.
type OpencodeRetireStopRecord struct {
	AppliedOn           string   `json:"applied_on"`
	AppliedBy           string   `json:"applied_by"`
	Mode                string   `json:"mode"`
	OpenCodeBotsStopped []string `json:"opencode_bots_stopped"`
}

// OpencodeRetireStopBlockers returns the reasons the opencode integration
// cannot be recorded as stopped yet. An empty result closes the umbrella.
func OpencodeRetireStopBlockers(state OpencodeRetirementState, record OpencodeRetireStopRecord) []string {
	blockers := []string{}
	for _, blocker := range OpencodeRetirementBlockers(state) {
		blockers = append(blockers, "retirement gate is still blocked: "+blocker)
	}
	if strings.TrimSpace(record.AppliedOn) == "" {
		blockers = append(blockers, "the retirement stop delta has not been applied (no date recorded)")
	}
	if strings.TrimSpace(record.AppliedBy) == "" {
		blockers = append(blockers, "the retirement stop record names no operator")
	}
	if record.Mode != OpencodeRetireStopModeDisabled {
		blockers = append(blockers, fmt.Sprintf("the stop record must be executed in %q mode, got %q",
			OpencodeRetireStopModeDisabled, record.Mode))
	}
	stopped := map[string]bool{}
	for _, bot := range record.OpenCodeBotsStopped {
		stopped[strings.ToLower(strings.TrimSpace(bot))] = true
	}
	missing := []string{}
	for _, legacy := range opencodeRetireStopLegacyBots() {
		if !stopped[strings.ToLower(legacy)] {
			missing = append(missing, legacy)
		}
	}
	if len(missing) > 0 {
		blockers = append(blockers, "opencode bots not recorded as stopped: "+strings.Join(missing, ", "))
	}
	return blockers
}

// OpencodeRetireStopComplete reports whether the opencode integration is stopped
// with the evidence the umbrella requires.
func OpencodeRetireStopComplete(state OpencodeRetirementState, record OpencodeRetireStopRecord) bool {
	return len(OpencodeRetireStopBlockers(state, record)) == 0
}

// RenderOpencodeRetireStopMarkdown renders the stop delta plus its record for a
// ticket comment; it is the closing evidence of the ci#585 umbrella.
func RenderOpencodeRetireStopMarkdown(state OpencodeRetirementState, record OpencodeRetireStopRecord) string {
	var b strings.Builder
	b.WriteString("# opencode retirement stop delta (ci#585)\n\n")
	b.WriteString("| kind | target | reason |\n|------|--------|--------|\n")
	for _, entry := range OpencodeRetireStopDelta() {
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", entry.Kind, entry.Target, entry.Reason))
	}
	b.WriteString("\n## Stop record\n\n")
	if OpencodeRetireStopComplete(state, record) {
		b.WriteString(fmt.Sprintf("applied on %s by %s in %q mode; no configured dev bot resolves to the %s backend.\n",
			record.AppliedOn, record.AppliedBy, record.Mode, OpenCodeLegacyRuntime))
		return b.String()
	}
	b.WriteString("pending:\n")
	for _, blocker := range OpencodeRetireStopBlockers(state, record) {
		b.WriteString("- " + blocker + "\n")
	}
	return b.String()
}
