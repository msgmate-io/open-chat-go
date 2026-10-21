package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestABValidationAxesAreStableAndUnique(t *testing.T) {
	axes := ABValidationAxes()
	if len(axes) == 0 {
		t.Fatal("expected comparison axes")
	}
	seen := map[string]bool{}
	for _, axis := range axes {
		if axis.ID == "" || axis.Description == "" {
			t.Fatalf("axis %+v is missing an id or description", axis)
		}
		if seen[axis.ID] {
			t.Fatalf("duplicate axis %q", axis.ID)
		}
		seen[axis.ID] = true
	}
	for _, want := range []string{"task_completed", "pr_opened", "sandbox_used", "tool_loop_parity", "auto_continue", "no_parked_turn"} {
		if !seen[want] {
			t.Errorf("expected axis %q to be part of the comparison", want)
		}
	}
}

func TestABValidationCasesStartEmpty(t *testing.T) {
	cases := ABValidationCases()
	if len(cases) != 0 {
		t.Fatalf("got %d cases, want none recorded before ci#583 runs", len(cases))
	}
	if err := ValidateABValidationPlan(cases); err == nil {
		t.Fatal("no recorded evidence must not validate as a plan")
	}
	if ABValidationFlipAllowed(NativeBotStages()[0].Stage, cases) {
		t.Fatal("no family may be flippable before any paired run is recorded")
	}
}

func TestABValidationTemplateCoversEveryFamily(t *testing.T) {
	cases := ABValidationTemplate()
	if err := ValidateABValidationPlan(cases); err != nil {
		t.Fatalf("blank template should be structurally valid: %v", err)
	}
	families := NativeBotStages()
	if want := len(families) * ABValidationMinCasesPerFamily; len(cases) != want {
		t.Fatalf("got %d cases, want %d", len(cases), want)
	}
	for _, stage := range families {
		count := 0
		for _, c := range cases {
			if c.Stage == stage.Stage {
				count++
			}
			if c.Stage == stage.Stage && c.NativeRuntime != stage.NativeRuntime {
				t.Errorf("family %q paired against runtime %q, want %q", c.Stage, c.NativeRuntime, stage.NativeRuntime)
			}
		}
		if count != ABValidationMinCasesPerFamily {
			t.Errorf("family %q has %d cases, want %d", stage.Stage, count, ABValidationMinCasesPerFamily)
		}
	}
	// The template carries no outcomes, so nothing is flippable from it.
	for _, stage := range families {
		if ABValidationFlipAllowed(stage.Stage, cases) {
			t.Errorf("family %q is flippable from the empty template", stage.Stage)
		}
	}
}

func TestABValidationPlanRejectsDrift(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if err := ValidateABValidationPlan(nil); err == nil {
			t.Fatal("expected an error for an empty plan")
		}
	})
	t.Run("unknown family", func(t *testing.T) {
		cases := ABValidationTemplate()
		cases[0].Stage = "not-a-family"
		if err := ValidateABValidationPlan(cases); err == nil || !strings.Contains(err.Error(), "unknown family") {
			t.Fatalf("got %v, want an unknown-family error", err)
		}
	})
	t.Run("wrong runtime", func(t *testing.T) {
		cases := ABValidationTemplate()
		cases[0].NativeRuntime = "some_other_runtime"
		if err := ValidateABValidationPlan(cases); err == nil || !strings.Contains(err.Error(), "paired against runtime") {
			t.Fatalf("got %v, want a runtime mismatch error", err)
		}
	})
	t.Run("foreign legacy bot", func(t *testing.T) {
		cases := ABValidationTemplate()
		cases[0].LegacyBot = "open-chat-development-lead"
		if err := ValidateABValidationPlan(cases); err == nil || !strings.Contains(err.Error(), "does not belong") {
			t.Fatalf("got %v, want a legacy-bot mismatch error", err)
		}
	})
	t.Run("unknown axis", func(t *testing.T) {
		cases := ABValidationTemplate()
		cases[0].Native = map[string]bool{"vibes": true}
		if err := ValidateABValidationPlan(cases); err == nil || !strings.Contains(err.Error(), "unknown comparison axis") {
			t.Fatalf("got %v, want an unknown-axis error", err)
		}
	})
	t.Run("missing family", func(t *testing.T) {
		cases := ABValidationTemplate()
		trimmed := cases[:0:0]
		for _, c := range cases {
			if c.Stage != NativeBotStages()[0].Stage {
				trimmed = append(trimmed, c)
			}
		}
		if err := ValidateABValidationPlan(trimmed); err == nil || !strings.Contains(err.Error(), "no paired run") {
			t.Fatalf("got %v, want a missing-family error", err)
		}
	})
}

func TestABValidationFamilyBlockers(t *testing.T) {
	stage := NativeBotStages()[0].Stage

	t.Run("passing fixture is clear", func(t *testing.T) {
		if blockers := ABValidationFamilyBlockers(stage, ABValidationPassingCases()); len(blockers) != 0 {
			t.Fatalf("expected no blockers, got %v", blockers)
		}
	})

	t.Run("too few runs", func(t *testing.T) {
		cases := ABValidationPassingCases()
		trimmed := cases[:0:0]
		kept := false
		for _, c := range cases {
			if c.Stage == stage && kept {
				continue
			}
			if c.Stage == stage {
				kept = true
			}
			trimmed = append(trimmed, c)
		}
		blockers := ABValidationFamilyBlockers(stage, trimmed)
		if len(blockers) == 0 || !strings.Contains(strings.Join(blockers, "\n"), "want at least") {
			t.Fatalf("expected a minimum-cases blocker, got %v", blockers)
		}
	})

	t.Run("regression", func(t *testing.T) {
		cases := ABValidationPassingCases()
		for i := range cases {
			if cases[i].Stage == stage {
				cases[i].Native["pr_opened"] = false
			}
		}
		blockers := ABValidationFamilyBlockers(stage, cases)
		if len(blockers) == 0 || !strings.Contains(strings.Join(blockers, "\n"), "native regressed on axis \"pr_opened\"") {
			t.Fatalf("expected a regression blocker, got %v", blockers)
		}
		if ABValidationFlipAllowed(stage, cases) {
			t.Fatal("a regressed family must not be flippable")
		}
	})

	t.Run("unrecorded outcome", func(t *testing.T) {
		cases := ABValidationPassingCases()
		for i := range cases {
			if cases[i].Stage == stage {
				delete(cases[i].Native, "auto_continue")
			}
		}
		blockers := ABValidationFamilyBlockers(stage, cases)
		if len(blockers) == 0 || !strings.Contains(strings.Join(blockers, "\n"), "no recorded outcome") {
			t.Fatalf("expected a missing-outcome blocker, got %v", blockers)
		}
	})

	t.Run("unknown family", func(t *testing.T) {
		blockers := ABValidationFamilyBlockers("nope", ABValidationPassingCases())
		if len(blockers) != 1 || !strings.Contains(blockers[0], "unknown family") {
			t.Fatalf("got %v, want a single unknown-family blocker", blockers)
		}
	})
}

func TestABValidationBlockersIncludeParityGaps(t *testing.T) {
	blockers := ABValidationBlockers(ABValidationPassingCases())
	gaps := RetirementParityBlockers()
	if len(gaps) == 0 {
		if len(blockers) != 0 {
			t.Fatalf("expected a fully green gate, got %v", blockers)
		}
		return
	}
	joined := strings.Join(blockers, "\n")
	if !strings.Contains(joined, "parity gap") {
		t.Fatalf("expected the ci#581 parity gaps to gate the fleet, got %v", blockers)
	}
	if !ABValidationFlipAllowed(NativeBotStages()[0].Stage, ABValidationPassingCases()) {
		t.Fatal("a family with complete paired runs must stay flippable even while a fleet-wide gap is open")
	}
}

func TestABValidationReportRenders(t *testing.T) {
	cases := ABValidationPassingCases()
	report := ABValidationReportFor(cases)
	if report.Umbrella != "ci#585" || report.Ticket != "ci#583" {
		t.Fatalf("unexpected report identity %+v", report)
	}
	if report.EvidenceEnvKey != ABValidationEvidenceEnvKey {
		t.Fatalf("got env key %q, want %q", report.EvidenceEnvKey, ABValidationEvidenceEnvKey)
	}
	if want := len(NativeBotStages()); len(report.FlipAllowed) != want {
		t.Fatalf("got %d flip entries, want %d", len(report.FlipAllowed), want)
	}
	if len(report.ValidatedFamilies) != len(NativeBotStages()) {
		t.Fatalf("got %v, want every family validated", report.ValidatedFamilies)
	}

	raw, err := ABValidationJSON(cases)
	if err != nil {
		t.Fatalf("render json: %v", err)
	}
	var decoded ABValidationReport
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(decoded.Cases) != len(cases) {
		t.Fatalf("round-trip lost cases: %d != %d", len(decoded.Cases), len(cases))
	}

	md := RenderABValidationMarkdown(cases)
	for _, stage := range NativeBotStages() {
		if !strings.Contains(md, stage.Stage) {
			t.Errorf("markdown table is missing family %q", stage.Stage)
		}
	}
	if !strings.Contains(md, "| family | legacy bot | native runtime | paired runs | flip allowed |") {
		t.Fatalf("unexpected markdown header:\n%s", md)
	}
}

func TestABValidationReportIsSerialisableWhenEmpty(t *testing.T) {
	report := ABValidationReportFor(nil)
	if report.Cases == nil || report.Blockers == nil || report.ValidatedFamilies == nil {
		t.Fatalf("empty report must serialise empty slices, not null: %+v", report)
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

func TestMigrationUmbrellaStepsMirrorTheRetirementPlan(t *testing.T) {
	steps := MigrationUmbrellaSteps(ABValidationPassingCases())
	plan := OpencodeRetirementPlan()
	if len(steps) != len(plan) {
		t.Fatalf("got %d steps, want one per retirement step (%d)", len(steps), len(plan))
	}
	for i, step := range steps {
		if step.ID != plan[i].ID || step.Ticket != plan[i].Ticket {
			t.Errorf("step %d is %s/%s, want %s/%s", i, step.ID, step.Ticket, plan[i].ID, plan[i].Ticket)
		}
		if step.Title == "" || step.Detail == "" {
			t.Errorf("step %+v is missing a title or detail", step)
		}
		switch step.Status {
		case MigrationStepSignedOff, MigrationStepPending, MigrationStepBlocked:
		default:
			t.Errorf("step %+v has an unknown status", step)
		}
	}
}

func TestMigrationUmbrellaStepsReflectRepoState(t *testing.T) {
	steps := MigrationUmbrellaSteps(ABValidationPassingCases())
	byTicket := map[string]MigrationUmbrellaStep{}
	for _, step := range steps {
		byTicket[step.Ticket] = step
	}
	for _, ticket := range []string{"ci#581", "ci#582", "ci#583", "ci#584", "ci#585"} {
		if _, ok := byTicket[ticket]; !ok {
			t.Errorf("umbrella checklist is missing %s", ticket)
		}
	}
	if got := byTicket["ci#583"].Status; got != MigrationStepSignedOff {
		t.Errorf("A/B step with a green fixture is %q, want %q", got, MigrationStepSignedOff)
	}
	if got := byTicket["ci#582"].Status; got != MigrationStepPending {
		t.Errorf("config-delta step is %q, want %q (it is a separate ticket)", got, MigrationStepPending)
	}
	if got := byTicket["ci#584"].Status; got != MigrationStepPending {
		t.Errorf("rollout step is %q, want %q (it is a separate ticket)", got, MigrationStepPending)
	}
	if !strings.Contains(byTicket["ci#584"].Detail, OpenCodeLegacyRuntime) {
		t.Errorf("rollout step must name the %q rollback target: %q", OpenCodeLegacyRuntime, byTicket["ci#584"].Detail)
	}
	if !strings.Contains(byTicket["ci#585"].Detail, OpenCodeLegacyRuntime) {
		t.Errorf("retirement step must name the %q runtime: %q", OpenCodeLegacyRuntime, byTicket["ci#585"].Detail)
	}
	if MigrationUmbrellaReady(steps) {
		t.Fatal("the umbrella must not be ready while the config delta and rollout are still pending")
	}
}

func TestMigrationUmbrellaStepsBlockOnOpenEvidenceAndParityGap(t *testing.T) {
	steps := MigrationUmbrellaSteps(nil)
	byTicket := map[string]MigrationUmbrellaStep{}
	for _, step := range steps {
		byTicket[step.Ticket] = step
	}
	ab := byTicket["ci#583"]
	if len(ABValidationBlockers(nil)) == 0 {
		t.Fatalf("expected an empty case set to block the A/B step, got %+v", ab)
	}
	if ab.Status != MigrationStepBlocked {
		t.Fatalf("A/B step is %q, want %q", ab.Status, MigrationStepBlocked)
	}
	if !strings.Contains(ab.Detail, "ab validation:") {
		t.Fatalf("A/B step detail should carry the blocker, got %q", ab.Detail)
	}

	parity := byTicket["ci#581"]
	gaps := RetirementParityBlockers()
	if len(gaps) == 0 {
		if parity.Status != MigrationStepSignedOff {
			t.Fatalf("parity step is %q while the registry reports no gap", parity.Status)
		}
		return
	}
	if parity.Status != MigrationStepBlocked {
		t.Fatalf("parity step is %q while %d gap(s) are open", parity.Status, len(gaps))
	}
	if !strings.Contains(parity.Detail, gaps[0]) {
		t.Fatalf("parity detail %q does not name the open gap %q", parity.Detail, gaps[0])
	}
}

func TestABValidationSignOffDrivesRetirementGate(t *testing.T) {
	state := OpencodeRetirementState{ABValidationDone: false}
	if got := ABValidatedRetirementState(state, ABValidationPassingCases()); !got.ABValidationDone {
		t.Fatal("green A/B evidence must set ABValidationDone")
	}
	if got := ABValidatedRetirementState(state, ABValidationCases()); got.ABValidationDone {
		t.Fatal("unrecorded A/B evidence must not set ABValidationDone")
	}
	ok, blockers := ABValidationSignOff(ABValidationPassingCases())
	if !ok || len(blockers) != 0 {
		t.Fatalf("green fixture must sign off, got ok=%t blockers=%v", ok, blockers)
	}
	if ok, blockers := ABValidationSignOff(nil); ok || len(blockers) == 0 {
		t.Fatalf("empty evidence must not sign off, got ok=%t blockers=%v", ok, blockers)
	}
	// The sign-off ignores the fleet-wide parity gaps: those are ci#581's step.
	if ok, _ := ABValidationSignOff(ABValidationPassingCases()); !ok {
		t.Fatal("ci#583 sign-off must not be blocked by an open ci#581 parity gap")
	}
}

func TestMigrationUmbrellaRendersAndSignsOff(t *testing.T) {
	steps := MigrationUmbrellaSteps(ABValidationPassingCases())
	md := RenderMigrationUmbrellaMarkdown(steps)
	if !strings.Contains(md, "| ticket | step | status | detail |") {
		t.Fatalf("unexpected markdown header:\n%s", md)
	}
	for _, step := range steps {
		if !strings.Contains(md, step.Ticket) {
			t.Errorf("markdown is missing %s", step.Ticket)
		}
	}
	if MigrationUmbrellaReady(nil) {
		t.Fatal("an empty checklist is not ready")
	}
	signedOff := make([]MigrationUmbrellaStep, 0, len(steps))
	for _, step := range steps {
		step.Status = MigrationStepSignedOff
		signedOff = append(signedOff, step)
	}
	if !MigrationUmbrellaReady(signedOff) {
		t.Fatal("an all-signed-off checklist must be ready")
	}
}
