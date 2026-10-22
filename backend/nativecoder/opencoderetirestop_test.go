package nativecoder

import (
	"strings"
	"testing"
)

func retireStopReadyState() OpencodeRetirementState {
	return OpencodeRetirementState{
		ConfiguredRuntimes: fullyNativeRuntimes(),
		RolledOutThrough:   stageLead,
		RollbackProvenOn:   stageRepl,
		NativeStableDays:   2 * NativeBotStabilityWindowDays,
		ParitySignedOff:    true,
		RetirementDeltaOK:  true,
		ABValidationDone:   true,
	}
}

func retireStopFullRecord() OpencodeRetireStopRecord {
	return OpencodeRetireStopRecord{
		AppliedOn:           "2026-10-22",
		AppliedBy:           "cur1ousdude",
		Mode:                OpencodeRetireStopModeDisabled,
		OpenCodeBotsStopped: opencodeRetireStopLegacyBots(),
	}
}

func TestOpencodeRetireStopDeltaCoversTheFleet(t *testing.T) {
	entries := OpencodeRetireStopDelta()
	if err := ValidateOpencodeRetireStopDelta(entries); err != nil {
		t.Fatalf("stop delta must be valid: %v", err)
	}
	seen := map[string]bool{}
	backendDisabled := false
	for _, entry := range entries {
		key := entry.Kind + "|" + strings.ToLower(entry.Target)
		if seen[key] {
			t.Fatalf("duplicate stop entry %s", key)
		}
		seen[key] = true
		if entry.Kind == OpencodeRetireStopKindDisableBackend {
			backendDisabled = true
		}
		if IsNativeBotRuntime(entry.Target) {
			t.Fatalf("stop delta targets the native runtime %q", entry.Target)
		}
	}
	if !backendDisabled {
		t.Fatal("stop delta must disable the opencode chat backend")
	}
	if len(opencodeRetireStopLegacyBots()) == 0 {
		t.Fatal("flip registry exposes no opencode-era bot")
	}
	for _, legacy := range opencodeRetireStopLegacyBots() {
		if !seen[OpencodeRetireStopKindRemoveBot+"|"+strings.ToLower(legacy)] {
			t.Fatalf("legacy bot %q is not removed by the stop delta", legacy)
		}
	}
	raw, err := OpencodeRetireStopDeltaJSON()
	if err != nil {
		t.Fatalf("stop delta json: %v", err)
	}
	if !strings.Contains(raw, OpenCodeLegacyRuntime) {
		t.Fatalf("stop delta json must mention %q, got %s", OpenCodeLegacyRuntime, raw)
	}
}

func TestValidateOpencodeRetireStopDeltaRejectsDrift(t *testing.T) {
	if err := ValidateOpencodeRetireStopDelta(nil); err == nil {
		t.Fatal("an empty stop delta must be rejected")
	}
	if err := ValidateOpencodeRetireStopDelta([]OpencodeRetireStopEntry{{Kind: "nope", Target: "x", Reason: "y"}}); err == nil {
		t.Fatal("an unknown stop kind must be rejected")
	}
	bad := []OpencodeRetireStopEntry{
		{Kind: OpencodeRetireStopKindDisableBackend, Target: OpenCodeLegacyRuntime, Reason: "r"},
		{Kind: OpencodeRetireStopKindRemoveBot, Target: NativeBotStages()[0].NativeRuntime, Reason: "r"},
	}
	if err := ValidateOpencodeRetireStopDelta(bad); err == nil {
		t.Fatal("a stop entry targeting a native runtime must be rejected")
	}
	if err := ValidateOpencodeRetireStopDelta(OpencodeRetireStopDelta()[:1]); err == nil {
		t.Fatal("a delta that stops no bot must be rejected")
	}
}

func TestOpencodeRetireStopGate(t *testing.T) {
	state := retireStopReadyState()
	record := retireStopFullRecord()
	if blockers := OpencodeRetireStopBlockers(state, record); len(blockers) != 0 {
		t.Fatalf("a ready gate with a full stop record must have no blockers, got %v", blockers)
	}
	if !OpencodeRetireStopComplete(state, record) {
		t.Fatal("a ready gate with a full stop record must be complete")
	}
	if md := RenderOpencodeRetireStopMarkdown(state, record); !strings.Contains(md, "applied on") {
		t.Fatalf("completed stop evidence must record the application, got %s", md)
	}
	if blockers := OpencodeRetireStopBlockers(state, OpencodeRetireStopRecord{}); len(blockers) == 0 {
		t.Fatal("an unrecorded stop must be blocked")
	}
	partial := record
	partial.OpenCodeBotsStopped = record.OpenCodeBotsStopped[:1]
	if blockers := OpencodeRetireStopBlockers(state, partial); len(blockers) == 0 {
		t.Fatal("a partial stop must be blocked")
	}
	if blockers := OpencodeRetireStopBlockers(OpencodeRetirementState{}, record); len(blockers) == 0 {
		t.Fatal("a blocked retirement gate must block the stop")
	}
	if md := RenderOpencodeRetireStopMarkdown(OpencodeRetirementState{}, OpencodeRetireStopRecord{}); !strings.Contains(md, "pending:") {
		t.Fatalf("blocked stop evidence must list the pending reasons, got %s", md)
	}
}
