package nativecoder

import "testing"

// TestGitHubTypedFirstGuidanceCoversEveryPlanTool keeps the prompt block and the
// retirement plan's `github_cli` target surface from drifting apart: every typed
// tool the plan promises to ship must be named by the prompt, and the prompt must
// name nothing the plan does not ship.
func TestGitHubTypedFirstGuidanceCoversEveryPlanTool(t *testing.T) {
	category, known := BashRetirementCategoryByID(BashCategoryGitHubCLI)
	if !known {
		t.Fatalf("%s is not in the plan registry", BashCategoryGitHubCLI)
	}
	if len(category.TargetTools) == 0 {
		t.Fatalf("%s declares no target tools", BashCategoryGitHubCLI)
	}
	declared := map[string]bool{}
	for _, tool := range GitHubTypedFirstTools() {
		declared[tool] = true
	}
	for _, tool := range category.TargetTools {
		if !declared[tool] {
			t.Errorf("the plan ships %q but the typed-first prompt does not name it", tool)
		}
	}
	if len(declared) != len(category.TargetTools) {
		t.Errorf("the prompt names %d tools, the plan ships %d", len(declared), len(category.TargetTools))
	}
	for _, tool := range GitHubTypedFirstTools() {
		if GitHubTypedFirstPurpose[tool] == "" {
			t.Errorf("%s has no prompt purpose", tool)
		}
		if !contains(GitHubTypedFirstGuidance(), tool) {
			t.Errorf("the rendered block does not mention %s", tool)
		}
	}
}

// TestGitHubTypedFirstAppendIsIdempotent asserts composing the block is safe to
// run on every request: the operator prompt is preserved, the block lands once,
// and an empty prompt still yields the block.
func TestGitHubTypedFirstAppendIsIdempotent(t *testing.T) {
	operator := "You are the msgmate coder. Follow DEVELOPMENT.md."
	once := GitHubTypedFirstAppend(operator)
	if !contains(once, operator) {
		t.Fatal("the operator prompt was rewritten instead of extended")
	}
	if !contains(once, GitHubTypedFirstPromptMarker) {
		t.Fatal("the typed-first block was not appended")
	}
	if twice := GitHubTypedFirstAppend(once); twice != once {
		t.Fatal("appending the block twice changed the prompt")
	}
	if bare := GitHubTypedFirstAppend("   "); bare != GitHubTypedFirstGuidance() {
		t.Fatal("an empty prompt did not become the typed-first block")
	}
}

// TestMeasureGitHubTypedFirstPrompt asserts the criterion is measured from the
// rendered prompt, so it cannot be satisfied by a constant existing in code.
func TestMeasureGitHubTypedFirstPrompt(t *testing.T) {
	ready := MeasureGitHubTypedFirstPrompt(GitHubTypedFirstAppend("coder prompt"))
	if !ready.Ready || !ready.GuidanceApplied || len(ready.Missing) != 0 {
		t.Fatalf("the composed prompt is not typed-first ready: %+v", ready)
	}
	if len(ready.Tools) != len(GitHubTypedFirstTools()) {
		t.Fatalf("evidence lists %d tools, want %d", len(ready.Tools), len(GitHubTypedFirstTools()))
	}
	bare := MeasureGitHubTypedFirstPrompt("You are the msgmate coder.")
	if bare.Ready || bare.GuidanceApplied {
		t.Fatalf("a prompt without the block reported ready: %+v", bare)
	}
	if len(bare.Missing) != len(GitHubTypedFirstTools()) {
		t.Fatalf("a bare prompt should miss every typed tool, got %d", len(bare.Missing))
	}
}

// TestEvaluateBashRetirementCriteriaGitHubTypedFirstPrompt asserts phase 1
// criterion 2 is attested from the rendered coder system prompt and stays
// blocked (not silently passed) when the prompt does not prefer typed tools.
func TestEvaluateBashRetirementCriteriaGitHubTypedFirstPrompt(t *testing.T) {
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
	evidence := BashRetirementEvidence{
		Coverage:          report,
		GitHub:            github,
		CoderSystemPrompt: GitHubTypedFirstAppend("You are the msgmate coder."),
	}
	criterion := gitHubPhaseCriterion(t, EvaluateBashRetirementCriteriaWithEvidence(evidence), 2)
	if criterion.Status != BashCriterionSatisfied {
		t.Fatalf("criterion 2 with the typed-first prompt = %q, want satisfied (blockers: %v)", criterion.Status, criterion.Blockers)
	}
	if len(criterion.Evidence) == 0 {
		t.Fatal("a satisfied criterion must record what it was derived from")
	}

	evidence.CoderSystemPrompt = "You are the msgmate coder."
	blocked := gitHubPhaseCriterion(t, EvaluateBashRetirementCriteriaWithEvidence(evidence), 2)
	if blocked.Status != BashCriterionBlocked {
		t.Fatalf("criterion 2 without the typed-first prompt = %q, want blocked", blocked.Status)
	}
	if len(blocked.Blockers) == 0 {
		t.Fatal("a blocked criterion must state the blocker")
	}
}

// gitHubPhaseCriterion returns the phase-1 criterion with the given index.
func gitHubPhaseCriterion(t *testing.T, criteria []BashRetirementPhaseCriteria, index int) BashRetirementCriterionEvidence {
	t.Helper()
	for _, phase := range criteria {
		if phase.ID != bashRetirementGitHubPhaseID {
			continue
		}
		for _, criterion := range phase.Criteria {
			if criterion.Index == index {
				return criterion
			}
		}
	}
	t.Fatalf("no %s criterion %d", bashRetirementGitHubPhaseID, index)
	return BashRetirementCriterionEvidence{}
}
