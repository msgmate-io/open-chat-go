package nativecoder

import "testing"

// reportFor builds a coverage report with one bucket for the GitHub category at
// the given share, so the gates can be exercised without a JSONL export.
func reportFor(share float64, classified int) BashCoverageReport {
	return BashCoverageReport{
		Version:         BashRetirementPlanVersion,
		DeprecatedTool:  BashRetirementDeprecatedTool,
		Threshold:       BashRetirementShareThreshold,
		TotalCalls:      classified,
		ClassifiedCalls: classified,
		Buckets: []BashCoverageBucket{{
			Category: BashCategoryGitHubCLI,
			Calls:    classified,
			Share:    share,
			Shipped:  true,
			Retired:  share < BashRetirementShareThreshold,
		}},
	}
}

func mustCategory(t *testing.T, id string) BashUsageCategory {
	t.Helper()
	category, ok := BashRetirementCategoryByID(id)
	if !ok {
		t.Fatalf("category %q must be in the plan registry", id)
	}
	return category
}

func phaseGateByID(t *testing.T, gates []BashRetirementPhaseGate, id string) BashRetirementPhaseGate {
	t.Helper()
	for _, gate := range gates {
		if gate.ID == id {
			return gate
		}
	}
	t.Fatalf("phase %q not found in gates", id)
	return BashRetirementPhaseGate{}
}

func categoryGateByID(t *testing.T, gates []BashRetirementCategoryGate, id string) BashRetirementCategoryGate {
	t.Helper()
	for _, gate := range gates {
		if gate.Category == id {
			return gate
		}
	}
	t.Fatalf("category %q not found in gates", id)
	return BashRetirementCategoryGate{}
}

// A shipped GitHub category below the threshold retires inside phase 1.
func TestEvaluateBashRetirementPhasesGatesGitHubPhaseReady(t *testing.T) {
	gates := EvaluateBashRetirementPhases(reportFor(0.01, 100))
	gate := phaseGateByID(t, gates, "phase_1_github")
	if !gate.Ready {
		t.Fatalf("phase_1_github should be ready, blockers: %v", gate.Blockers)
	}
	if !gate.Measured {
		t.Fatalf("phase_1_github should be measured against a non-empty sample")
	}
	entry := categoryGateByID(t, gate.Categories, BashCategoryGitHubCLI)
	if !entry.Retired || len(entry.Blockers) != 0 {
		t.Fatalf("github category should be retired, got %+v", entry)
	}
	if entry.Threshold != BashRetirementShareThreshold {
		t.Fatalf("gate threshold %v must mirror the report threshold %v",
			entry.Threshold, BashRetirementShareThreshold)
	}
	if len(entry.TargetTools) == 0 {
		t.Fatalf("a retired category must carry the typed tools that replace it")
	}
	if !entry.Shipped {
		t.Fatalf("the registry marks the GitHub surface as shipped")
	}
}

// A share at or above the threshold blocks the phase and names the reason.
func TestEvaluateBashRetirementPhasesBlocksOverThresholdShare(t *testing.T) {
	gates := EvaluateBashRetirementPhases(reportFor(BashRetirementShareThreshold, 100))
	gate := phaseGateByID(t, gates, "phase_1_github")
	if gate.Ready {
		t.Fatalf("phase_1_github must not be ready while the share is at the threshold")
	}
	entry := categoryGateByID(t, gate.Categories, BashCategoryGitHubCLI)
	if entry.Retired {
		t.Fatalf("a category at the threshold must not be retired")
	}
	if len(entry.Blockers) != 1 || len(gate.Blockers) != 1 {
		t.Fatalf("expected exactly one blocker, got entry %v / phase %v",
			entry.Blockers, gate.Blockers)
	}
}

// A category whose typed surface the plan does not yet ship blocks its phase
// even at zero observed share: the gate reads the registry, not the bucket.
func TestEvaluateBashRetirementPhasesBlocksUnshippedSurface(t *testing.T) {
	unshipped := mustCategory(t, BashCategoryBuildTestRun)
	if unshipped.Shipped {
		t.Fatalf("precondition: %q is expected to be unshipped in the plan", unshipped.ID)
	}
	gates := EvaluateBashRetirementPhases(reportFor(0.01, 100))
	gate := phaseGateByID(t, gates, "phase_2_build_test_run")
	if gate.Ready {
		t.Fatalf("phase_2_build_test_run must not be ready while its surface is unshipped")
	}
	entry := categoryGateByID(t, gate.Categories, unshipped.ID)
	if entry.Shipped || entry.Retired {
		t.Fatalf("an unshipped category must not be retired, got %+v", entry)
	}
	if len(entry.Blockers) != 1 {
		t.Fatalf("an unshipped, under-threshold category has exactly one blocker, got %v",
			entry.Blockers)
	}
}

// Guarded categories are deliberately shell-only: they are omitted from the
// gates and never block a phase, but their phase never reports ready either.
func TestEvaluateBashRetirementPhasesSkipsGuardedCategories(t *testing.T) {
	guarded := mustCategory(t, BashCategoryResidualShell)
	if !guarded.Guarded {
		t.Fatalf("precondition: %q must be the guarded residual category", guarded.ID)
	}
	report := reportFor(0.01, 100)
	report.Buckets = append(report.Buckets, BashCoverageBucket{
		Category: guarded.ID,
		Calls:    50,
		Share:    0.50,
	})
	gates := EvaluateBashRetirementPhases(report)
	for _, gate := range gates {
		for _, entry := range gate.Categories {
			if entry.Category == guarded.ID {
				t.Fatalf("guarded category %q must be omitted from phase %q", guarded.ID, gate.ID)
			}
		}
	}
	// The guarded-only phase has nothing to close and stays open regardless.
	residual := phaseGateByID(t, gates, "phase_5_guarded_residual")
	if residual.Ready {
		t.Fatalf("a guarded-only phase has nothing to close and must not report ready")
	}
	// But a guarded shell-heavy share must not block the GitHub phase.
	if !phaseGateByID(t, gates, "phase_1_github").Ready {
		t.Fatalf("a guarded shell-heavy category must not block phase_1_github")
	}
	if BlockersContain(gates, BashCategoryResidualShell) {
		t.Fatalf("guarded category must never appear in any blocker list")
	}
}

// BlockersContain reports whether any phase blocker mentions the given category.
func BlockersContain(gates []BashRetirementPhaseGate, category string) bool {
	for _, gate := range gates {
		for _, blocker := range gate.Blockers {
			if len(blocker) >= len(category) && blocker[:len(category)] == category {
				return true
			}
		}
	}
	return false
}

// The measure phase retires no category and must never report ready, and an
// empty sample is never reported as measured.
func TestEvaluateBashRetirementPhasesDefinitionPhaseAndEmptySample(t *testing.T) {
	gates := EvaluateBashRetirementPhases(reportFor(0.01, 0))
	measure := phaseGateByID(t, gates, "phase_0_measure")
	if measure.Ready {
		t.Fatalf("the measure phase retires nothing and must not report ready")
	}
	if measure.Measured {
		t.Fatalf("the measure phase evaluated against zero calls is not measured")
	}
	for _, gate := range gates {
		if gate.Measured {
			t.Fatalf("phase %q evaluated against zero classified calls is not measured", gate.ID)
		}
	}
}

// No gates at all is not evidence of readiness, and a retirement with an open
// phase cannot close; it closes only once every retiring phase is ready.
func TestBashRetirementReadyToCloseRequiresRetiringPhases(t *testing.T) {
	if BashRetirementReadyToClose(nil) {
		t.Fatalf("no gates is not evidence of readiness")
	}
	gates := EvaluateBashRetirementPhases(reportFor(0.01, 100))
	if BashRetirementReadyToClose(gates) {
		t.Fatalf("a retirement with unshipped phases must not be ready to close")
	}
	// Only definition/guarded phases: nothing to close is not "ready to close".
	definitions := []BashRetirementPhaseGate{
		{ID: "phase_0_measure", Measured: true},
		{ID: "phase_5_guarded_residual", Measured: true},
	}
	if BashRetirementReadyToClose(definitions) {
		t.Fatalf("a retirement with no retiring phase must not be ready to close")
	}
	// Every retiring phase ready => the retirement closes.
	retired := BashRetirementCategoryGate{Category: BashCategoryGitHubCLI, Retired: true}
	ready := append(append([]BashRetirementPhaseGate(nil), definitions...),
		BashRetirementPhaseGate{
			ID:         "phase_1_github",
			Measured:   true,
			Ready:      true,
			Categories: []BashRetirementCategoryGate{retired},
		})
	if !BashRetirementReadyToClose(ready) {
		t.Fatalf("all retiring phases ready must close the retirement")
	}
	// One open block anywhere keeps it open.
	blocked := append([]BashRetirementPhaseGate(nil), ready...)
	blocked[2].Ready = false
	blocked[2].Blockers = []string{"github_cli: share is not below the threshold"}
	if BashRetirementReadyToClose(blocked) {
		t.Fatalf("a single unreadied retiring phase must keep the retirement open")
	}
}

// Every phased category id must resolve through the plan registry, so a typo in
// the plan cannot silently hide a category from the gate. The gate order must
// also match the plan order.
func TestEvaluateBashRetirementPhasesTracksPlanShape(t *testing.T) {
	plan := BashRetirementPlanSnapshot()
	gates := EvaluateBashRetirementPhases(reportFor(0.01, 100))
	if len(gates) != len(plan.Phases) {
		t.Fatalf("gate count %d must match plan phase count %d", len(gates), len(plan.Phases))
	}
	for i, phase := range plan.Phases {
		if gates[i].ID != phase.ID || gates[i].Order != phase.Order {
			t.Fatalf("gate %d is %q/%d, plan has %q/%d",
				i, gates[i].ID, gates[i].Order, phase.ID, phase.Order)
		}
		for _, id := range phase.Categories {
			if _, ok := BashRetirementCategoryByID(id); !ok {
				t.Fatalf("phase %q references unknown category %q", phase.ID, id)
			}
		}
	}
}
