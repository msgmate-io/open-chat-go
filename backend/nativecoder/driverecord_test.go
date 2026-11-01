package nativecoder

import "testing"

// TestBashRetirementDriveRecordCheck proves that the drive record's self-check
// against the classifier is green for the primitive shapes the record claims
// are covered (GitHub CLI + git), so the drive documentation can never drift
// from the classifier that produces the retirement metric.
func TestBashRetirementDriveRecordCheck(t *testing.T) {
	got := BashRetirementDriveRecordCheck(BashRetirementDriveRecord{})
	if !got.Checked {
		t.Fatalf("drive record self-check against classifier failed: verified=%v", got.Verified)
	}
	if len(got.Verified) == 0 {
		t.Fatalf("drive record self-check verified nothing")
	}
}

// TestValidateBashRetirementDriveRecord covers the invariant validation that
// keeps the drive record (and so the phases/categories tables) consistent: a
// category may be retired only by the phase that declares it retires that
// category, states come from the allowed set, and ids/ticket are present.
func TestValidateBashRetirementDriveRecord(t *testing.T) {
	valid := BashRetirementDriveRecord{
		Phase:  "ci514-drive",
		Ticket: "ci#514",
		Surface: []string{
			"github_issue_list", "github_pr_view", "github_api", "coder_git",
		},
		Phases: []BashRetirementDrivePhase{
			{ID: "phase_0_measure", State: BashRetirementDriveStateShipped, Targets: "classifier + bash_retirement_report", EvidenceAnchor: "bashretirement.go"},
			{ID: "phase_1_github", Retires: BashCategoryGitHubCLI, State: BashRetirementDriveStateShipped, Targets: "githubshell.go bridge + github_* typed tools", EvidenceAnchor: "githubshell.go"},
		},
		Categories: []BashRetirementDriveCategory{
			{Category: BashCategoryGitHubCLI, RetiredBy: "phase_1_github", Shipped: true},
		},
	}
	if err := validateBashRetirementDriveRecord(valid); err != nil {
		t.Fatalf("valid drive record rejected: %v", err)
	}

	// A category retires in a phase that does not retire it must fail.
	bad := valid
	bad.Categories[0] = BashRetirementDriveCategory{Category: BashCategoryGitHubCLI, RetiredBy: "phase_1_github", Shipped: false}
	bad.Categories = append(bad.Categories, BashRetirementDriveCategory{Category: BashCategoryGitMutation, RetiredBy: "phase_1_github", Shipped: true})
	if err := validateBashRetirementDriveRecord(bad); err == nil {
		t.Fatalf("category retired by a phase that does not retire it was accepted")
	}

	// A mutating category may still be shipped (typed git surface is the
	// replacement) even before the phase that retires it is closed.
	gitShipped := valid
	gitShipped.Categories = append(gitShipped.Categories, BashRetirementDriveCategory{Category: BashCategoryGitMutation, Shipped: true})
	if err := validateBashRetirementDriveRecord(gitShipped); err != nil {
		t.Fatalf("shipped git mutation category rejected while phase open: %v", err)
	}
}
