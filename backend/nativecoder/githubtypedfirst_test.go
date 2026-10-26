package nativecoder

import (
	"strings"
	"testing"
)

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
		if !strings.Contains(GitHubTypedFirstGuidance(), tool) {
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
	if !strings.Contains(once, operator) {
		t.Fatal("the operator prompt was rewritten instead of extended")
	}
	if !strings.Contains(once, GitHubTypedFirstPromptMarker) {
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
