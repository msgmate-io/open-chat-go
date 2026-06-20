package msgmate

import (
	"strings"
	"testing"
)

// ci#793: the persisted compaction summary used to carry a durable
// "was evicted, re-read ..." directive. This is the verbatim excerpt from the
// reported interaction's backend log.
const ci793VolatileSummary = `Task: deep review of a-little-world/little-world-backend#1327 (open PR branch pr1327).
Findings so far: the retry helper in ` + "`internal/http/retry.go`" + ` re-reads the config file on every attempt,
which matches the report in ci#1327 comment 2.
Evidence collected: ` + "`git show pr1327:internal/http/retry.go`" + ` shows the reload at line 42.
ALL of those reads were later evicted from context — re-read the specific ranges with coder_read/.rev/*.py if needed (keep outputs small: context ~88% full).
Open questions: does the caller bound the attempt count?
`

func TestSanitizeCompactionSummaryDropsRefetchDirective(t *testing.T) {
	out, report := SanitizeCompactionSummary(ci793VolatileSummary)
	if !report.Replaced() {
		t.Fatalf("expected the volatile directive to be reported as replaced, got %+v", report)
	}
	if report.VolatileDirectives == 0 {
		t.Fatalf("expected at least one volatile directive, got %+v", report)
	}
	if !report.Stabilized || report.AlreadyStable {
		t.Fatalf("expected a fresh stabilization, got %+v", report)
	}

	// The directive must be gone: no instruction to re-read/re-fetch evidence.
	lower := strings.ToLower(out)
	for _, forbidden := range []string{
		"re-read the specific ranges",
		"with coder_read/.rev/*.py if needed",
	} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("sanitized summary still instructs a re-fetch (%q):\n%s", forbidden, out)
		}
	}

	// The findings must survive verbatim, not be summarized away.
	for _, wanted := range []string{
		"internal/http/retry.go",
		"line 42",
		"a-little-world/little-world-backend#1327",
		"does the caller bound the attempt count?",
		"ci#1327 comment 2",
	} {
		if !strings.Contains(out, wanted) {
			t.Errorf("sanitized summary lost the finding %q:\n%s", wanted, out)
		}
	}

	// The stable block and the explicit replacement rule must be present.
	if !strings.Contains(out, compactionSummaryStableHeader) {
		t.Errorf("missing stable findings header:\n%s", out)
	}
	if !strings.Contains(out, compactionSummaryNoRefetchRule) {
		t.Errorf("missing no-re-fetch rule:\n%s", out)
	}
	if !strings.Contains(out, compactionSummaryDirectiveReplacement) {
		t.Errorf("missing directive-removal marker:\n%s", out)
	}
}

func TestSanitizeCompactionSummaryIsIdempotent(t *testing.T) {
	first, firstReport := SanitizeCompactionSummary(ci793VolatileSummary)
	if !firstReport.Stabilized {
		t.Fatalf("first pass did not stabilize: %+v", firstReport)
	}
	second, secondReport := SanitizeCompactionSummary(first)
	if !secondReport.AlreadyStable {
		t.Fatalf("re-compaction of a stable summary was not detected as stable: %+v", secondReport)
	}
	if secondReport.Replaced() {
		t.Fatalf("re-compaction re-armed a directive: %+v", secondReport)
	}
	// Re-compacting must not stack headers or grow the block, otherwise the
	// summary itself becomes the unbounded artifact (ci#793).
	if second != first {
		t.Fatalf("sanitize is not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
	if n := strings.Count(second, compactionSummaryStableMarker); n != 1 {
		t.Fatalf("stable marker appears %d times, want 1", n)
	}
}

func TestCompactionLoopSuspected(t *testing.T) {
	if !CompactionLoopSuspected(ci793VolatileSummary) {
		t.Fatal("a summary with a re-fetch directive must be flagged as a compaction loop")
	}
	stable, _ := SanitizeCompactionSummary(ci793VolatileSummary)
	if CompactionLoopSuspected(stable) {
		t.Fatalf("a stabilized summary must not be flagged:\n%s", stable)
	}
	if CompactionLoopSuspected("") {
		t.Fatal("an empty summary must not be flagged")
	}
}

func TestSanitizeCompactionSummaryKeepsFindingsThatMentionEviction(t *testing.T) {
	// A finding is allowed to describe the eviction (it is a fact about the
	// turn). It is dropped only when the line *also* points at an ephemeral
	// artifact or re-fetches evidence.
	finding := "Observed `evicted_tool_results` = 7 in the first turn, so the guard works as designed."
	out, report := SanitizeCompactionSummary(finding)
	if report.EvictionNarration != 0 {
		t.Fatalf("a finding mentioning eviction was dropped: %+v", report)
	}
	if !strings.Contains(out, "`evicted_tool_results` = 7") {
		t.Fatalf("finding lost in sanitize:\n%s", out)
	}
}

func TestSanitizeCompactionSummaryHandlesEmptyAndNarrationOnly(t *testing.T) {
	if out, report := SanitizeCompactionSummary("   \n  "); out != "" || report.Stabilized {
		t.Fatalf("blank summary should be returned as-is and untouched, got %q %+v", out, report)
	}

	narrationOnly := "The earlier reads were evicted from the context during this turn.\n" +
		"Context ~88% full, so keep tool outputs small.\n"
	out, report := SanitizeCompactionSummary(narrationOnly)
	if report.EvictionNarration != 2 {
		t.Fatalf("expected 2 narration lines removed, got %+v", report)
	}
	if strings.Contains(strings.ToLower(out), "keep tool outputs small") {
		t.Fatalf("eviction narration survived:\n%s", out)
	}
	if !strings.Contains(out, compactionSummaryNoRefetchRule) {
		t.Fatalf("a narration-only summary must still get the stable block:\n%s", out)
	}
}

func TestFrameCompactionSummaryForContextMatchesSanitize(t *testing.T) {
	framed, report := FrameCompactionSummaryForContext(ci793VolatileSummary)
	sanitized, _ := SanitizeCompactionSummary(ci793VolatileSummary)
	if framed != sanitized {
		t.Fatal("injected frame and persisted form diverged")
	}
	if !strings.Contains(framed, "source of truth") {
		t.Fatalf("frame lost the source-of-truth statement:\n%s", framed)
	}
	if report.VolatileDirectives != 1 {
		t.Fatalf("unexpected directive count: %+v", report)
	}
}
