package nativecoder

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestAuditFleetInteractionThrash covers the ci#733 Part A/B thresholds: an
// edit-less interaction that burned the tool budget, an interaction repeating
// exact calls, and an interaction ending in a streak of non-productive turns.
func TestAuditFleetInteractionThrash(t *testing.T) {
	noEdits := AuditFleetInteraction(FleetAuditInput{ToolCalls: FleetAuditCallThreshold, Edits: 0})
	if !noEdits.Thrashing || noEdits.Action != FleetAuditActionStopAndNarrow {
		t.Fatalf("expected stop_and_narrow for calls-without-edits, got %+v", noEdits)
	}
	if noEdits.RecommendedPrompt == "" {
		t.Fatal("thrash verdict must carry the narrowing prompt")
	}

	// One edit before the threshold keeps the interaction healthy.
	if healthy := AuditFleetInteraction(FleetAuditInput{ToolCalls: FleetAuditCallThreshold, Edits: 3}); healthy.Thrashing {
		t.Fatalf("an interaction that edits must not be thrashing: %+v", healthy)
	}

	dupes := AuditFleetInteraction(FleetAuditInput{ToolCalls: 8, Edits: 4, ExactDuplicateCalls: FleetAuditDuplicateThreshold})
	if !dupes.Thrashing {
		t.Fatalf("^%d exact duplicate calls must be thrashing: %+v", FleetAuditDuplicateThreshold, dupes)
	}

	streak := AuditFleetInteraction(FleetAuditInput{
		ToolCalls:       5,
		Edits:           2,
		TerminalReasons: []string{"turn_completed", "no_usable_tool_call", "turn_timeout"},
	})
	if !streak.Thrashing {
		t.Fatalf("two non-productive terminal reasons in a row must be thrashing: %+v", streak)
	}

	// A single non-productive turn after a productive one is not a streak.
	noStreak := AuditFleetInteraction(FleetAuditInput{
		ToolCalls:       5,
		Edits:           2,
		TerminalReasons: []string{"turn_timeout", "turn_completed", "turn_timeout"},
	})
	if noStreak.Thrashing {
		t.Fatalf("non-consecutive terminal reasons must not be thrashing: %+v", noStreak)
	}
}

// TestAuditFleetInteractionModeration covers the moderation actions the lead
// takes on stalled, summary-less and PR-less interactions (ci#733 Part E).
func TestAuditFleetInteractionModeration(t *testing.T) {
	stalled := AuditFleetInteraction(FleetAuditInput{ToolCalls: 2, Edits: 1, StallReason: "stall_no_first_turn"})
	if !stalled.Stalled || stalled.Action != FleetAuditActionContinueOnce {
		t.Fatalf("a stalled interaction needs exactly one continue: %+v", stalled)
	}
	if stalled.RecommendedPrompt == "" {
		t.Fatal("stalled verdict must carry a resume prompt")
	}

	aged := AuditFleetInteraction(FleetAuditInput{ToolCalls: 2, Edits: 1, StallAgeSeconds: FleetAuditStallAgeSeconds})
	if !aged.Stalled {
		t.Fatalf("an interaction without a turn past the stall age is stalled: %+v", aged)
	}

	noSummary := AuditFleetInteraction(FleetAuditInput{ToolCalls: 4, Edits: 2, NoSummary: true})
	if noSummary.Action != FleetAuditActionContinueOnce {
		t.Fatalf("a summary-less stop needs one continue: %+v", noSummary)
	}

	noPR := AuditFleetInteraction(FleetAuditInput{ToolCalls: 4, Edits: 2, FinishedWithoutPR: true})
	if noPR.Action != FleetAuditActionContinueOnce {
		t.Fatalf("finished-without-PR needs one continue: %+v", noPR)
	}

	ready := AuditFleetInteraction(FleetAuditInput{ToolCalls: 4, Edits: 2, ReadyPR: true})
	if ready.Action != FleetAuditActionReadyToMerge {
		t.Fatalf("a ready PR is the lead's to merge: %+v", ready)
	}

	if ok := AuditFleetInteraction(FleetAuditInput{ToolCalls: 4, Edits: 2}); ok.Action != FleetAuditActionOK {
		t.Fatalf("an ordinary healthy interaction is ok: %+v", ok)
	}
}

// TestFleetAuditThresholdsResolution guards the shared-config half of the
// policy: partial configs keep the shipped defaults.
func TestFleetAuditThresholdsResolution(t *testing.T) {
	if got := DefaultFleetAuditThresholds(); got.MaxCalls != FleetAuditCallThreshold || got.NarrowCallBudget != FleetAuditNarrowCallBudget {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	partial := ResolveFleetAuditThresholds(map[string]interface{}{"max_calls": 12})
	if partial.MaxCalls != 12 {
		t.Fatalf("configured max_calls must win: %+v", partial)
	}
	if partial.MaxExactRepeats != FleetAuditDuplicateThreshold || partial.TerminalStreak != FleetAuditTerminalStreakThreshold {
		t.Fatalf("unset thresholds must fall back to defaults: %+v", partial)
	}

	fromString := ResolveFleetAuditThresholds(`{"max_calls": 7, "narrow_call_budget": 9}`)
	if fromString.MaxCalls != 7 || fromString.NarrowCallBudget != 9 {
		t.Fatalf("JSON-string thresholds must decode: %+v", fromString)
	}

	// The dedicated key wins over the playbook block; the playbook is the
	// fallback.
	config := map[string]interface{}{
		DevLeadFleetThresholdsConfigKey: map[string]interface{}{"max_calls": 11},
		DevLeadPlaybookConfigKey:        map[string]interface{}{DevLeadThresholdsKey: map[string]interface{}{"max_calls": 99}},
	}
	if got := ResolveFleetAuditThresholdsFromConfig(config); got.MaxCalls != 11 {
		t.Fatalf("dedicated thresholds key must win: %+v", got)
	}
	playbookOnly := map[string]interface{}{
		DevLeadPlaybookConfigKey: map[string]interface{}{DevLeadThresholdsKey: map[string]interface{}{"max_calls": 42}},
	}
	if got := ResolveFleetAuditThresholdsFromConfig(playbookOnly); got.MaxCalls != 42 {
		t.Fatalf("playbook thresholds must be honoured: %+v", got)
	}
	if got := ResolveFleetAuditThresholdsFromConfig(nil); got.MaxCalls != FleetAuditCallThreshold {
		t.Fatalf("nil config must resolve to defaults: %+v", got)
	}
}

// TestApplyDevLeadPlaybookDefault guards the ci#733 Part F default: the
// playbook reaches a lead's prompt without ever clobbering an operator value.
func TestApplyDevLeadPlaybookDefault(t *testing.T) {
	lead := map[string]interface{}{
		"tools": []string{"coder_bash", DevLeadFleetToolName},
	}
	out := ApplyDevLeadPlaybookDefault(lead)
	block, ok := out[DevLeadPlaybookConfigKey].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a playbook block, got %T", out[DevLeadPlaybookConfigKey])
	}
	if block[DevLeadEnabledKey] != true {
		t.Fatalf("playbook must be enabled by default: %+v", block)
	}
	instructions, _ := block[DevLeadInstructionsKey].(string)
	if instructions == "" {
		t.Fatal("playbook block must carry the instruction text")
	}
	if _, ok := block[DevLeadThresholdsKey].(map[string]interface{}); !ok {
		t.Fatalf("playbook block must carry the thresholds: %+v", block)
	}

	// Configured values always win.
	configured := map[string]interface{}{
		DevLeadPlaybookConfigKey: map[string]interface{}{DevLeadEnabledKey: false},
		"tools":                  []string{DevLeadRescueToolName},
	}
	if got := ApplyDevLeadPlaybookDefault(configured); got[DevLeadPlaybookConfigKey].(map[string]interface{})[DevLeadEnabledKey] != false {
		t.Fatal("an operator-configured playbook must be preserved verbatim")
	}

	// A plain coder agent never grows the block.
	plain := map[string]interface{}{"tools": []string{"coder_bash"}}
	if _, exists := ApplyDevLeadPlaybookDefault(plain)[DevLeadPlaybookConfigKey]; exists {
		t.Fatal("a non-lead coder config must not grow the playbook")
	}
}

// TestDevLeadPlaybookConfigIsJSONRoundTrippable keeps the shipped playbook
// storable as jsonb in the shared config.
func TestDevLeadPlaybookConfigIsJSONRoundTrippable(t *testing.T) {
	encoded, err := json.Marshal(DevLeadPlaybookConfig())
	if err != nil {
		t.Fatalf("playbook must marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("playbook must unmarshal: %v", err)
	}
	thresholds := ResolveFleetAuditThresholds(decoded[DevLeadThresholdsKey])
	if thresholds.MaxCalls != FleetAuditCallThreshold {
		t.Fatalf("thresholds must survive a JSON round trip: %+v", thresholds)
	}
}

// TestIsDevLeadAgentConfig covers lead detection from the tools and from the
// playbook/threshold keys.
func TestIsDevLeadAgentConfig(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]interface{}
		want   bool
	}{
		{"nil", nil, false},
		{"plain coder", map[string]interface{}{"tools": []string{"coder_bash"}}, false},
		{"audit tool", map[string]interface{}{"tools": []interface{}{DevLeadFleetToolName}}, true},
		{"rescue tool", map[string]interface{}{"tools": []interface{}{DevLeadRescueToolName}}, true},
		{"playbook key", map[string]interface{}{DevLeadPlaybookConfigKey: true}, true},
		{"thresholds key", map[string]interface{}{DevLeadFleetThresholdsConfigKey: map[string]interface{}{}}, true},
	}
	for _, tc := range cases {
		if got := IsDevLeadAgentConfig(tc.config); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// TestDevLeadNarrowPromptBudget ensures the rescue prompt names the granted
// budget, so the lead's instruction and the config cannot drift apart.
func TestDevLeadNarrowPromptBudget(t *testing.T) {
	audit := AuditFleetInteraction(FleetAuditInput{ToolCalls: 40, Edits: 0})
	if audit.Action != FleetAuditActionStopAndNarrow {
		t.Fatalf("expected stop_and_narrow, got %+v", audit)
	}
	if !strings.Contains(audit.RecommendedPrompt, strconv.Itoa(FleetAuditNarrowCallBudget)) {
		t.Fatalf("narrow prompt must name the call budget: %q", audit.RecommendedPrompt)
	}
}
