package nativecoder

import (
	"strings"
	"testing"

	"backend/runtimecfg"
)

// TestNativeBotStagesFlipOrderIsLeadLast guards the ci#584 rollout order: the
// ticket's families flip plan -> repl/coding -> admin -> lead and the
// coordinating lead is always last.
func TestNativeBotStagesFlipOrderIsLeadLast(t *testing.T) {
	stages := NativeBotStages()
	if len(stages) < 5 {
		t.Fatalf("expected at least 5 families, got %d", len(stages))
	}
	for i, stage := range stages {
		if stage.Order != i+1 {
			t.Errorf("stage %q has order %d, want %d", stage.Stage, stage.Order, i+1)
		}
		if !IsNativeBotRuntime(stage.NativeRuntime) {
			t.Errorf("stage %q targets %q, which is not a native runtime", stage.Stage, stage.NativeRuntime)
		}
		if len(stage.Monitors) == 0 {
			t.Errorf("stage %q has no monitoring signal", stage.Stage)
		}
	}
	if got := stages[len(stages)-1].NativeRuntime; got != NativeBotLeadRuntime {
		t.Fatalf("expected the lead runtime to flip last, got %q", got)
	}
	if got := stages[0].NativeRuntime; got != NativeBotPlanRuntime {
		t.Fatalf("expected the plan runtime to flip first, got %q", got)
	}
	last := stages[len(stages)-1]
	seenOrder := map[int]bool{}
	for _, stage := range stages {
		if seenOrder[stage.Order] {
			t.Errorf("two families share the flip position %d", stage.Order)
		}
		seenOrder[stage.Order] = true
		if stage.NativeRuntime != NativeBotLeadRuntime {
			continue
		}
		if stage.Order > last.Order {
			t.Errorf("family %q shares the lead runtime but flips after the lead", stage.Stage)
		}
	}
}

// TestNativeBotStagesShareTheCoordinatingRuntime guards the deliberately shared
// admin/lead runtime: admin and the coordinating lead run the same native
// runtime, so the admin family has to flip before the lead. A family sharing the
// lead runtime moves with the lead runtime, which also means it can never carry
// the rollback proof (see RollbackProofEligible).
func TestNativeBotStagesShareTheCoordinatingRuntime(t *testing.T) {
	stages := NativeBotStages()
	last := stages[len(stages)-1]
	if !last.Critical {
		t.Error("expected the lead family to be marked critical")
	}
	if NativeBotAdminRuntime != NativeBotLeadRuntime {
		t.Fatalf("expected admin to share the lead runtime, got %q", NativeBotAdminRuntime)
	}
	for _, stage := range stages {
		if stage.NativeRuntime != NativeBotLeadRuntime || stage.Stage == last.Stage {
			continue
		}
		if stage.Order > last.Order {
			t.Errorf("family %q uses the lead runtime but flips after the lead", stage.Stage)
		}
		if RollbackProofEligible(stage.Stage) {
			t.Errorf("family %q shares the coordinating runtime and must not carry the rollback proof", stage.Stage)
		}
	}
	if !RollbackProofEligible(stagePlan) {
		t.Error("expected the plan family to be rollback-proof eligible")
	}
}

// TestNativeBotLegacyAliasesTargetOwnStageRuntime guards the alias map used by
// the runtime selector: every opencode-era name resolves to the native runtime
// of its own family, and the flip is declared exactly once.
func TestNativeBotLegacyAliasesTargetOwnStageRuntime(t *testing.T) {
	aliases := LegacyRuntimeBotAliases()
	for _, legacy := range []string{"issue-plan-bot", "issue-repl-bot", "coding-bot", "issue-review-bot", "review-bot", "open-chat-development-lead", "open-chat-go-ci-admin", "opencode"} {
		native, ok := aliases[legacy]
		if !ok {
			t.Errorf("legacy bot %q has no native alias", legacy)
			continue
		}
		stage, ok := NativeBotStageForLegacyBot(legacy)
		if !ok || stage.NativeRuntime != native {
			t.Errorf("alias %q -> %q does not match its family runtime", legacy, native)
		}
	}
	if got := aliases["opencode"]; got != NativeBotReplRuntime {
		t.Fatalf("expected opencode to route to %q, got %q", NativeBotReplRuntime, got)
	}
}

// TestNativeBotRotationKeepsLegacyOutOfRotation checks that a flipped family is
// served natively while the legacy runtime is only the rollback target, and that
// the unflipped families keep running the legacy runtime.
func TestNativeBotRotationKeepsLegacyOutOfRotation(t *testing.T) {
	stages := NativeBotStages()
	plan := stages[0]
	lead := stages[len(stages)-1]
	if got := NativeBotRotation(plan, plan.Stage); got != "native" {
		t.Fatalf("expected flipped family to run native, got %q", got)
	}
	if got := NativeBotRotation(lead, plan.Stage); got != OpenCodeLegacyRuntime {
		t.Fatalf("expected an unflipped family to run %q, got %q", OpenCodeLegacyRuntime, got)
	}
	if got := NativeBotRollbackTarget(lead); got != OpenCodeLegacyRuntime {
		t.Fatalf("expected the legacy runtime to stay the rollback target, got %q", got)
	}
}

// TestNativeBotRollbackWindowParsesStageAndLegacyNames checks the runtime knob
// that pauses or rolls back a family without a rebuild.
func TestNativeBotRollbackWindowParsesStageAndLegacyNames(t *testing.T) {
	previous := runtimecfg.GetAll()
	defer runtimecfg.SetAll(previous)

	runtimecfg.SetValue(NativeBotRollbackWindowEnvKey, runtimecfg.Value{Value: "repl, issue-review-bot"})
	open := NativeBotRollbackStages()
	if !open[stageRepl] || !open[stageReview] {
		t.Fatalf("expected repl and review windows open, got %v", open)
	}
	if open[stageLead] {
		t.Fatalf("expected the lead window closed, got %v", open)
	}

	runtimecfg.SetValue(NativeBotRollbackWindowEnvKey, runtimecfg.Value{Value: "all"})
	open = NativeBotRollbackStages()
	for _, stage := range NativeBotStages() {
		if !open[stage.Stage] {
			t.Errorf("expected the %s window open for \"all\"", stage.Stage)
		}
	}

	runtimecfg.SetValue(NativeBotRollbackWindowEnvKey, runtimecfg.Value{Value: ""})
	if open := NativeBotRollbackStages(); len(open) != 0 {
		t.Fatalf("expected every window closed without a setting, got %v", open)
	}
}

// TestRetirementBlockersRequireFullFlipAndProvenRollback mirrors the ci#584
// acceptance criteria.
func TestRetirementBlockersRequireFullFlipAndProvenRollback(t *testing.T) {
	stages := NativeBotStages()
	last := stages[len(stages)-1]

	incomplete := RetirementBlockers(NativeBotMigrationState{RolledOutThrough: stages[0].Stage, NativeStableDays: NativeBotStabilityWindowDays})
	if len(incomplete) == 0 {
		t.Fatal("expected blockers for a partial rollout")
	}

	leadProof := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: last.Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
	})
	if len(leadProof) == 0 {
		t.Fatal("expected a blocker when the rollback was only proven on the critical lead")
	}

	ready := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
	})
	if len(ready) != 0 {
		t.Fatalf("expected no blockers for a fully flipped, rollback-proven fleet, got %v", ready)
	}

	retained := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
		RetainedParity:   map[string]string{"repl": ""},
	})
	if len(retained) == 0 {
		t.Fatal("expected a blocker for an unexplained parity exception")
	}
}

// TestNativeBotRolloutDocsAreRendered guards the runbook rendering and the
// per-family checklist (no secrets, stage names only).
func TestNativeBotRolloutDocsAreRendered(t *testing.T) {
	markdown := RenderNativeBotRolloutMarkdown()
	for _, stage := range NativeBotStages() {
		if !strings.Contains(markdown, stage.NativeRuntime) {
			t.Errorf("rollout table is missing runtime %q", stage.NativeRuntime)
		}
		checklist := NativeBotStageChecklist(stage)
		if len(checklist) < 5 {
			t.Errorf("stage %q checklist is incomplete: %v", stage.Stage, checklist)
		}
		for _, step := range checklist {
			if strings.Contains(step, "kubeconfig") || strings.Contains(step, "token") {
				t.Errorf("stage %q checklist leaks credential material: %q", stage.Stage, step)
			}
		}
	}
	if !strings.Contains(markdown, OpenCodeLegacyRuntime) {
		t.Fatal("rollout table must name the legacy runtime kept for rollback")
	}
}
