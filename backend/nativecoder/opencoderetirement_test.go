package nativecoder

import (
	"strings"
	"testing"
)

// fullyNativeRuntimes records every family as flipped to its native runtime.
func fullyNativeRuntimes() map[string]string {
	runtimes := map[string]string{}
	for _, stage := range NativeBotStages() {
		runtimes[stage.NativeRuntime] = stage.NativeRuntime
	}
	return runtimes
}

func TestValidateOpencodeRetirementPlan(t *testing.T) {
	if err := ValidateOpencodeRetirementPlan(); err != nil {
		t.Fatal(err)
	}
	plan := OpencodeRetirementPlan()
	if len(plan) != 5 {
		t.Fatalf("expected the five umbrella steps, got %d", len(plan))
	}
	if last := plan[len(plan)-1]; last.ID != "retire_opencode" || last.Ticket != "ci#585" {
		t.Fatalf("retirement must be the last step, got %+v", last)
	}
}

func TestOpencodeLegacyBotsFollowsTheFlip(t *testing.T) {
	// Nothing flipped: every family is still opencode-era.
	if got := OpencodeLegacyBots(OpencodeRetirementState{}); len(got) == 0 {
		t.Fatal("expected every family to be reported as legacy when nothing is configured")
	}

	// The plan family flipped under its native runtime name, the repl family
	// flipped the legacy bot in place to the native runtime.
	partial := OpencodeRetirementState{ConfiguredRuntimes: map[string]string{
		NativeBotPlanRuntime: NativeBotPlanRuntime,
		"issue-repl-bot":     NativeBotReplRuntime,
	}}
	got := OpencodeLegacyBots(partial)
	if containsString(got, "issue-plan-bot") || containsString(got, "issue-repl-bot") {
		t.Fatalf("flipped families must not be reported: %v", got)
	}
	if !containsString(got, "open-chat-development-lead") {
		t.Fatalf("the lead flips last, so it must still be legacy: %v", got)
	}

	// Entire fleet flipped -> nothing legacy left.
	if legacy := OpencodeLegacyBots(OpencodeRetirementState{ConfiguredRuntimes: fullyNativeRuntimes()}); len(legacy) != 0 {
		t.Fatalf("expected no legacy bots, got %v", legacy)
	}
}

func TestOpencodeRetirementGate(t *testing.T) {
	blocked := OpencodeRetirementState{}
	if OpencodeRetirementReady(blocked) {
		t.Fatal("expected the gate to be blocked")
	}
	blockers := OpencodeRetirementBlockers(blocked)
	for _, want := range []string{"ci#581", "ci#582", "ci#583", "families still running " + OpenCodeLegacyRuntime} {
		if !containsSubstr(blockers, want) {
			t.Fatalf("expected a blocker mentioning %q, got %v", want, blockers)
		}
	}
	md := RenderOpencodeRetirementMarkdown(blocked)
	if !strings.Contains(md, "## Gate") || !strings.Contains(md, "blocked:") {
		t.Fatalf("markdown must render the blocked gate:\n%s", md)
	}
	if !strings.Contains(md, "- **opencode bots and integration stopped on ci** (ci#585)") {
		t.Fatalf("markdown must render the checklist:\n%s", md)
	}

	ready := OpencodeRetirementState{
		ConfiguredRuntimes: fullyNativeRuntimes(),
		RolledOutThrough:   stageLead,
		RollbackProvenOn:   stageRepl,
		NativeStableDays:   2 * NativeBotStabilityWindowDays,
		ParitySignedOff:    true,
		RetirementDeltaOK:  true,
		ABValidationDone:   true,
	}
	if !OpencodeRetirementReady(ready) {
		t.Fatalf("expected ready, blockers: %v", OpencodeRetirementBlockers(ready))
	}
	if !strings.Contains(RenderOpencodeRetirementMarkdown(ready), "ready:") {
		t.Fatal("markdown must report the ready gate")
	}
}

func TestOpencodeRetirementRefusesRollbackOnTheLead(t *testing.T) {
	// The lead orchestrates the fleet, so a rollback proof taken from it must
	// not unlock the retirement gate.
	state := OpencodeRetirementState{
		ConfiguredRuntimes: fullyNativeRuntimes(),
		RolledOutThrough:   stageLead,
		RollbackProvenOn:   stageLead,
		NativeStableDays:   2 * NativeBotStabilityWindowDays,
		ParitySignedOff:    true,
		RetirementDeltaOK:  true,
		ABValidationDone:   true,
	}
	if OpencodeRetirementReady(state) {
		t.Fatal("a rollback proven on the coordinating lead must not retire opencode")
	}
	if !containsSubstr(OpencodeRetirementBlockers(state), "rollback was never proven on a non-critical") {
		t.Fatalf("expected the rollback-proof blocker, got %v", OpencodeRetirementBlockers(state))
	}
}

func containsSubstr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
