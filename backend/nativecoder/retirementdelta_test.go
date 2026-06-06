package nativecoder

import (
	"strings"
	"testing"
)

// TestOpencodeRetirementFlipDeltaFollowsTheRegistry guards ci#582: the delta is
// derived from the flip registry, so it covers every opencode-era bot exactly
// once and in flip order (the coordinating family last).
func TestOpencodeRetirementFlipDeltaFollowsTheRegistry(t *testing.T) {
	if err := ValidateOpencodeRetirementDeltaPlan(); err != nil {
		t.Fatal(err)
	}
	flip := OpencodeRetirementFlipDelta()
	want := 0
	for _, stage := range NativeBotStages() {
		want += len(stage.LegacyBots)
	}
	if len(flip) != want {
		t.Fatalf("flip delta has %d entries, want one per opencode-era bot (%d)", len(flip), want)
	}
	last := flip[len(flip)-1]
	stage, ok := NativeBotStageForLegacyBot(last.Bot)
	if !ok || !stage.Critical {
		t.Fatalf("expected the critical family to flip last, got %+v", last)
	}
	for _, entry := range flip {
		if entry.From != OpenCodeLegacyRuntime {
			t.Errorf("entry %q leaves %q instead of %q", entry.Bot, entry.From, OpenCodeLegacyRuntime)
		}
	}
}

// TestOpencodeRetirementRollbackIsTheInverse checks that the rollback delta
// unwinds every flip, in reverse order.
func TestOpencodeRetirementRollbackIsTheInverse(t *testing.T) {
	flip := OpencodeRetirementFlipDelta()
	rollback := OpencodeRetirementRollbackDelta()
	if len(flip) != len(rollback) {
		t.Fatalf("expected %d rollback entries, got %d", len(flip), len(rollback))
	}
	first := rollback[0]
	if first.Bot != flip[len(flip)-1].Bot {
		t.Fatalf("expected the rollback to start where the flip ended, got %q", first.Bot)
	}
	if first.To != OpenCodeLegacyRuntime || first.Action != OpencodeRetirementDeltaRollback {
		t.Fatalf("expected a rollback back onto %q, got %+v", OpenCodeLegacyRuntime, first)
	}
}

// TestValidateOpencodeRetirementDeltasRejectsDrift pins the guard rails: a bot
// missing from the rollback, a duplicated bot and a non-inverse direction must
// all fail.
func TestValidateOpencodeRetirementDeltasRejectsDrift(t *testing.T) {
	flip := OpencodeRetirementFlipDelta()
	rollback := OpencodeRetirementRollbackDelta()

	if err := ValidateOpencodeRetirementDeltas(flip, rollback[:len(rollback)-1]); err == nil {
		t.Error("expected a short rollback delta to be rejected")
	}

	duplicated := append(append([]OpencodeRetirementDeltaEntry{}, flip...), flip[0])
	if err := ValidateOpencodeRetirementDeltas(duplicated, append(append([]OpencodeRetirementDeltaEntry{}, rollback...), rollback[0])); err == nil {
		t.Error("expected a duplicated bot to be rejected")
	}

	drifted := append([]OpencodeRetirementDeltaEntry{}, rollback...)
	drifted[0] = flip[len(flip)-1]
	if err := ValidateOpencodeRetirementDeltas(flip, drifted); err == nil {
		t.Error("expected a rollback entry that is not the inverse of its flip to be rejected")
	}

	nonNative := append([]OpencodeRetirementDeltaEntry{}, flip...)
	nonNative[0].To = "some-other-runtime"
	if err := ValidateOpencodeRetirementDeltas(nonNative, rollback); err == nil {
		t.Error("expected a flip onto a non-native runtime to be rejected")
	}
}

// TestPendingFlipDeltaFollowsTheFleet checks the delta against the observed
// configuration, using the same per-family decision as the retirement gate.
func TestPendingFlipDeltaFollowsTheFleet(t *testing.T) {
	all := PendingOpencodeRetirementFlipDelta(OpencodeRetirementState{})
	if len(all) != len(OpencodeRetirementFlipDelta()) {
		t.Fatalf("expected every entry to be outstanding for an unflipped fleet, got %d", len(all))
	}

	planStage, _ := NativeBotStageByName(stagePlan)
	flipped := OpencodeRetirementState{ConfiguredRuntimes: fullyNativeRuntimes()}
	if pending := PendingOpencodeRetirementFlipDelta(flipped); len(pending) != 0 {
		t.Fatalf("expected no outstanding entries for a fully native fleet, got %d", len(pending))
	}

	partial := OpencodeRetirementState{ConfiguredRuntimes: map[string]string{
		planStage.NativeRuntime: planStage.NativeRuntime,
	}}
	pending := PendingOpencodeRetirementFlipDelta(partial)
	if len(pending) != len(all)-len(planStage.LegacyBots) {
		t.Fatalf("expected only the unflipped families outstanding, got %d of %d", len(pending), len(all))
	}
	for _, entry := range pending {
		if entry.Stage == planStage.Stage {
			t.Fatalf("flipped family %q is still reported outstanding", entry.Stage)
		}
	}
}

// TestRenderOpencodeRetirementDeltaMarkdown guards the rendered delta and the
// vault-sync apply procedure (no secrets, names only).
func TestRenderOpencodeRetirementDeltaMarkdown(t *testing.T) {
	md := RenderOpencodeRetirementDeltaMarkdown(OpencodeRetirementFlipDelta())
	for _, want := range []string{"| family | bot |", "publish-open-chat-config.yaml", "DRY_RUN=true", "DRY_RUN=false"} {
		if !strings.Contains(md, want) {
			t.Errorf("delta markdown is missing %q:\n%s", want, md)
		}
	}
	if !strings.Contains(md, OpenCodeLegacyRuntime) {
		t.Error("delta markdown must name the runtime that is being retired")
	}

	encoded, err := OpencodeRetirementDeltaJSON(OpencodeRetirementRollbackDelta())
	if err != nil {
		t.Fatalf("render rollback delta: %v", err)
	}
	if !strings.Contains(encoded, `"action":"rollback"`) {
		t.Errorf("rollback delta JSON is incomplete: %s", encoded)
	}
}
