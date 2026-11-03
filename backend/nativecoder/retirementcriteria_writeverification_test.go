package nativecoder

import (
	"strings"
	"testing"
)

// ci#514 phase 1 exit criterion 4: "a write (comment/review/PR) is re-read and
// reported as verified instead of claimed". These tests pin the machine-checked
// statuses the criteria evaluator derives from the write-verification report
// (githubwriteverify.go): satisfied only when every recorded write is verified
// by its read-back, blocked when any write is unverified or only weakly
// verified, unattested when no report was supplied at all.

func criterion4For(t *testing.T, results []GitHubWriteVerificationResult) BashRetirementCriterionEvidence {
	t.Helper()
	phases := EvaluateBashRetirementCriteriaWithEvidence(BashRetirementEvidence{
		WriteVerification: results,
	})
	for _, phase := range phases {
		if phase.ID != bashRetirementGitHubPhaseID {
			continue
		}
		for _, criterion := range phase.Criteria {
			if criterion.Index == 4 {
				return criterion
			}
		}
	}
	t.Fatalf("phase %s declares no criterion 4", bashRetirementGitHubPhaseID)
	return BashRetirementCriterionEvidence{}
}

func TestPhase1Criterion4NoVerificationReportIsUnattested(t *testing.T) {
	criterion := criterion4For(t, nil)
	if criterion.Status != BashCriterionUnattested {
		t.Fatalf("status = %q, want %q", criterion.Status, BashCriterionUnattested)
	}
	if len(criterion.Blockers) == 0 || !strings.Contains(criterion.Blockers[0], "no write-verification report") {
		t.Fatalf("blockers = %v, want a missing-report blocker", criterion.Blockers)
	}
}

func TestPhase1Criterion4VerifiedWritesAreSatisfied(t *testing.T) {
	criterion := criterion4For(t, []GitHubWriteVerificationResult{
		{Tool: "github_issue_comment", ReadTool: "github_issue_view", Verified: true, Marker: "done", Reason: "read it back"},
		{Tool: "github_pr_merge", ReadTool: "github_pr_view", Verified: true, Marker: "merged", Reason: "read it back"},
	})
	if criterion.Status != BashCriterionSatisfied {
		t.Fatalf("status = %q, want %q (blockers: %v)", criterion.Status, BashCriterionSatisfied, criterion.Blockers)
	}
	want := 3 // one evidence line per verified write, plus the total line
	if len(criterion.Evidence) != want {
		t.Fatalf("evidence = %v, want %d lines", criterion.Evidence, want)
	}
	if !strings.Contains(criterion.Evidence[0], "github_issue_comment verified via github_issue_view re-read") {
		t.Fatalf("evidence = %v, want the per-write re-read line", criterion.Evidence)
	}
}

func TestPhase1Criterion4UnverifiedWriteIsBlocked(t *testing.T) {
	criterion := criterion4For(t, []GitHubWriteVerificationResult{
		{Tool: "github_issue_comment", ReadTool: "github_issue_view", Verified: true, Marker: "done", Reason: "read it back"},
		{Tool: "github_pr_merge", ReadTool: "github_pr_view", Verified: false, Reason: "the read-back is empty: github_pr_merge is NOT verified"},
	})
	if criterion.Status != BashCriterionBlocked {
		t.Fatalf("status = %q, want %q", criterion.Status, BashCriterionBlocked)
	}
	if len(criterion.Blockers) == 0 || !strings.Contains(criterion.Blockers[0], "github_pr_merge is not verified") {
		t.Fatalf("blockers = %v, want the unverified-write reason", criterion.Blockers)
	}
}

func TestPhase1Criterion4WeakVerificationBlocksThePhase(t *testing.T) {
	criterion := criterion4For(t, []GitHubWriteVerificationResult{
		{Tool: "github_pr_review", ReadTool: "github_pr_view", Verified: true, Weak: true, Reason: "weakly verified: no marker to match"},
	})
	if criterion.Status != BashCriterionBlocked {
		t.Fatalf("status = %q, want %q", criterion.Status, BashCriterionBlocked)
	}
	if len(criterion.Blockers) == 0 || !strings.Contains(criterion.Blockers[0], "only weakly verified") {
		t.Fatalf("blockers = %v, want the weak-verification reason", criterion.Blockers)
	}
}
