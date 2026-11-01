package nativecoder

import (
	"testing"
)

// TestCombinedChecksStateEmptyGuard covers the ci#928 stale-cache fingerprint
// at the state-resolution layer. The planned fix cross-checks /check-runs and
// the legacy /status payload before treating a bare `pending` as a block; this
// test pins the safety sides of that resolution so the eventual merge of a
// just-merged commit is never vetoed by the stale report, while a real red
// context still wins.
func TestCombinedChecksStateEmptyGuard(t *testing.T) {
	// A real red legacy status next to nothing else must decide as failure.
	if got := combinedStateFromFacts(nil, []statusFact{{Context: "private-sandbox-tests", State: "failure"}}); got != "failure" {
		t.Fatalf("combined state = %q, want failure for a real red status", got)
	}
	if got := combinedStateFromFacts(nil, []statusFact{{Context: "private-sandbox-tests", State: "pending"}}); got != "pending" {
		t.Fatalf("combined state = %q, want pending for a real pending status", got)
	}
	// A commit with no statuses at all (neither a push nor the stale /status
	// `pending` with an empty list) is "unknown" — the legacy aggregate returns
	// pending only when a real status context exists to be pending.
	if got := combinedStateFromFacts(nil, []statusFact{}); got != "unknown" {
		t.Fatalf("combined state = %q, want unknown for a commit with no statuses (a real pending context must exist to be pending)", got)
	}
	// Check runs remain authoritative for the aggregate.
	if got := combinedStateFromFacts([]checkRunFact{{Name: "Run PR CI Tests and Deployments", Status: "completed", Conclusion: "failure"}}, nil); got != "failure" {
		t.Fatalf("combined state = %q, want failure for a failed check run", got)
	}
}
