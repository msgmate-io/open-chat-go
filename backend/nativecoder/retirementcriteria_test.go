package nativecoder

import "testing"

// TestEvaluateBashRetirementCriteriaIsCompleteOverThePlan asserts the checklist
// covers every declared exit criterion of every phase, and that nothing is ever
// declared ready from an empty sample.
func TestEvaluateBashRetirementCriteriaIsCompleteOverThePlan(t *testing.T) {
	plan := BashRetirementPlanSnapshot()
	criteria := EvaluateBashRetirementCriteria(BashCoverageReport{}, nil)
	if len(criteria) != len(plan.Phases) {
		t.Fatalf("expected one checklist per phase (%d), got %d", len(plan.Phases), len(criteria))
	}
	valid := map[string]bool{
		BashCriterionSatisfied:  true,
		BashCriterionBlocked:    true,
		BashCriterionUnattested: true,
	}
	for i, phase := range plan.Phases {
		entry := criteria[i]
		if entry.ID != phase.ID {
			t.Fatalf("checklist %d is for %q, want %q", i, entry.ID, phase.ID)
		}
		if len(entry.Criteria) != len(phase.ExitCriteria) {
			t.Fatalf("%s: got %d criteria, want %d", phase.ID, len(entry.Criteria), len(phase.ExitCriteria))
		}
		if entry.Ready {
			t.Fatalf("%s: reported ready from an empty sample", phase.ID)
		}
		if len(entry.Criteria) > 0 && len(entry.Blockers) == 0 {
			t.Fatalf("%s: criteria are unmet but no blocker is reported", phase.ID)
		}
		for j, criterion := range entry.Criteria {
			if !valid[criterion.Status] {
				t.Fatalf("%s criterion %d has unknown status %q", phase.ID, j+1, criterion.Status)
			}
			if criterion.Text != phase.ExitCriteria[j] {
				t.Fatalf("%s criterion %d text drifted from the plan", phase.ID, j+1)
			}
			if criterion.Index != j+1 {
				t.Fatalf("%s criterion %d has index %d", phase.ID, j+1, criterion.Index)
			}
		}
	}
	if BashRetirementCriteriaReadyToClose(criteria) {
		t.Fatal("the whole plan reported ready from an empty sample")
	}
	if BashRetirementCriteriaReadyToClose(nil) {
		t.Fatal("an empty checklist reported ready")
	}
}

// TestEvaluateBashRetirementCriteriaGitHubPhase uses the coverage and GitHub
// translation evidence a retired github_cli category would produce, and asserts
// the criteria that have evidence become satisfied while the ones without stay
// unattested and keep the phase open.
func TestEvaluateBashRetirementCriteriaGitHubPhase(t *testing.T) {
	report := BashCoverageReport{
		Threshold:       0.02,
		TotalCalls:      100,
		ClassifiedCalls: 100,
		Buckets: []BashCoverageBucket{
			{Category: BashCategoryGitHubCLI, Share: 0.01},
		},
	}
	github := &GitHubShellTranslationReport{
		PlanVersion:        BashRetirementPlanVersion,
		SourceTool:         "coder_bash",
		Threshold:          0.02,
		TotalCalls:         10,
		Commands:           10,
		GitHubCommands:     10,
		TranslatedCommands: 10,
		TranslationRatio:   1,
		PhaseReady:         true,
	}

	var entry *BashRetirementPhaseCriteria
	all := EvaluateBashRetirementCriteria(report, github)
	for i := range all {
		if all[i].ID == bashRetirementGitHubPhaseID {
			entry = &all[i]
		}
	}
	if entry == nil {
		t.Fatalf("no checklist for %s", bashRetirementGitHubPhaseID)
	}
	if !entry.Measured {
		t.Fatal("a non-empty sample must mark the phase measured")
	}
	byIndex := map[int]BashRetirementCriterionEvidence{}
	for _, criterion := range entry.Criteria {
		byIndex[criterion.Index] = criterion
	}
	if got := byIndex[3].Status; got != BashCriterionSatisfied {
		t.Fatalf("criterion 3 (share below threshold) = %q, want satisfied (blockers: %v)", got, byIndex[3].Blockers)
	}
	if got := byIndex[5].Status; got != BashCriterionSatisfied {
		t.Fatalf("criterion 5 (typed-GitHub scenario) = %q, want satisfied (blockers: %v)", got, byIndex[5].Blockers)
	}
	if got := byIndex[2].Status; got != BashCriterionUnattested {
		t.Fatalf("criterion 2 (typed-first prompt) = %q, want unattested", got)
	}
	if got := byIndex[4].Status; got != BashCriterionUnattested {
		t.Fatalf("criterion 4 (verified write) = %q, want unattested", got)
	}
	if entry.Ready {
		t.Fatal("a phase with unattested criteria must not report ready")
	}
	if entry.Attested {
		t.Fatal("a phase with unattested criteria must not report attested")
	}
	joined := ""
	for _, blocker := range entry.Blockers {
		joined += blocker + "\n"
	}
	for _, want := range []string{"criterion_2", "criterion_4"} {
		if !contains(joined, want) {
			t.Fatalf("blockers %q do not mention %s", joined, want)
		}
	}
}

// TestEvaluateBashRetirementCriteriaGitHubPhaseWithoutEvidence asserts a
// missing translation report keeps the criterion unattested instead of passing.
func TestEvaluateBashRetirementCriteriaGitHubPhaseWithoutEvidence(t *testing.T) {
	for _, phase := range EvaluateBashRetirementCriteria(BashCoverageReport{}, nil) {
		if phase.ID != bashRetirementGitHubPhaseID {
			continue
		}
		for _, criterion := range phase.Criteria {
			if criterion.Index == 5 && criterion.Status != BashCriterionUnattested {
				t.Fatalf("criterion 5 without a report = %q, want unattested", criterion.Status)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
