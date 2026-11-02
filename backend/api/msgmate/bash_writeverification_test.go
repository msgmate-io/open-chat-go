package msgmate

import (
	"testing"
)

// TestGitHubWriteVerificationsSummary counts every verdict row exactly once,
// splits verified from not-yet-verified, and stays zero for no evidence.
func TestGitHubWriteVerificationsSummary(t *testing.T) {
	recorded, verified, unverified := GitHubWriteVerificationsSummary(nil)
	if recorded != 0 || verified != 0 || unverified != 0 {
		t.Fatalf("empty summary = (%d,%d,%d), want (0,0,0)", recorded, verified, unverified)
	}

	verdicts := []GitHubWriteVerificationResult{
		{Tool: "github_issue_comment", Verified: true},
		{Tool: "github_pr_create", Verified: true},
		{Tool: "github_pr_merge", Verified: false, Reason: "read-back still pending"},
	}
	recorded, verified, unverified = GitHubWriteVerificationsSummary(verdicts)
	if recorded != 3 || verified != 2 || unverified != 1 {
		t.Fatalf("summary = (%d,%d,%d), want (3,2,1)", recorded, verified, unverified)
	}
}

// TestWrittenCoverageFraction drives the phase-1 exit-criterion-4 metric: it is
// verified/recorded, not the old "0 or 1" stub. 0 with nothing recorded means
// unattested (the evaluator's no-exit-evidence case), not all-verified.
func TestWrittenCoverageFraction(t *testing.T) {
	empty := BashCoverageReport{}
	if fraction := empty.WrittenCoverageFraction(); fraction != 0 {
		t.Fatalf("empty report fraction = %v, want 0 (unattested)", fraction)
	}
	if percent := empty.WrittenCoveragePercent(); percent != 0 {
		t.Fatalf("empty report percent = %d, want 0", percent)
	}

	half := BashCoverageReport{WrittenVerifications: 2, WrittenRecorded: 4}
	if fraction := half.WrittenCoverageFraction(); !almostEqual(fraction, 0.5) {
		t.Fatalf("half fraction = %v, want 0.5", fraction)
	}
	if percent := half.WrittenCoveragePercent(); percent != 50 {
		t.Fatalf("half percent = %d, want 50", percent)
	}

	full := BashCoverageReport{WrittenVerifications: 3, WrittenRecorded: 3}
	if fraction := full.WrittenCoverageFraction(); fraction != 1 {
		t.Fatalf("full fraction = %v, want 1", fraction)
	}
	if percent := full.WrittenCoveragePercent(); percent != 100 {
		t.Fatalf("full percent = %d, want 100", percent)
	}
}

// TestApplyGitHubWriteVerifications folds the per-record verdicts into the
// report columns without touching the classification buckets.
func TestApplyGitHubWriteVerifications(t *testing.T) {
	report := BuildBashCoverageReport([]string{"gh pr create"})
	report = report.ApplyGitHubWriteVerifications([]GitHubWriteVerificationResult{
		{Tool: "github_pr_create", Verified: true, Weblink: "https://example.test/pr/1"},
		{Tool: "github_issue_comment", Verified: false, Reason: "read-back pending"},
	})
	if report.Total != 1 || report.WrittenVerifications != 1 || report.WrittenRecorded != 2 || report.WrittenUnverified != 1 {
		t.Fatalf("applied report = %#v", report)
	}
	if fraction := report.WrittenCoverageFraction(); !almostEqual(fraction, 0.5) {
		t.Fatalf("applied fraction = %v, want 0.5", fraction)
	}
}

// TestWrittenCoverageMetadataShape pins the metadata keys the coder_context
// payload and the CI coverage publication consume for the write column.
func TestWrittenCoverageMetadataShape(t *testing.T) {
	report := BuildBashCoverageReport([]string{"gh pr create"}).
		ApplyGitHubWriteVerifications([]GitHubWriteVerificationResult{{Tool: "github_pr_create", Verified: true}})
	metadata := report.Metadata()
	for _, key := range []string{"written_verifications", "written_recorded", "written_unverified", "written_coverage_percent"} {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("metadata misses %q: %#v", key, metadata)
		}
	}
	if metadata["written_coverage_percent"] != 100 || metadata["written_recorded"] != 1 {
		t.Fatalf("metadata write values = %#v, want recorded=1 percent=100", metadata)
	}
}

func almostEqual(a, b float64) bool {
	delta := a - b
	if delta < 0 {
		delta = -delta
	}
	return delta < 1e-9
}
