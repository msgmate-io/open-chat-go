package msgmate

import (
	"regexp"
	"strings"
)

// Compaction-summary stability (ci#793).
//
// The native coder persists a compaction summary (`context_summary`) and the
// reader injects it every turn as a leading message. The summary used to carry
// *volatile directives* - "ALL of those reads were later evicted from context -
// re-read the specific ranges with coder_read/.rev/*.py if needed" - because it
// was written against state the context budget evicts again immediately. A
// durable instruction that names ephemeral state turns compaction into a loop:
// every new turn inherits "re-fetch X", the re-fetched output is evicted, the
// next compaction repeats the directive. In ci#793 the same 8 KiB summary
// appeared 8x, with 32 `git show`, 47 `git grep`, 107 `coder_read` and a resumed
// turn paused after 122 tool calls - the review itself was never posted.
//
// The fix is to make the injected summary self-contained and stable: drop the
// lines that instruct a re-fetch and the lines that only narrate what the guard
// evicted, keep the findings verbatim, and state the rule that replaces the
// directive ("this block is the source of truth; re-fetch only a specifically
// named missing fact"). The pass is idempotent, so re-compacting does not
// duplicate the block and does not re-arm a directive that was already stripped.
const (
	// compactionSummaryStableMarker marks a stabilized summary and is the
	// idempotency guard that stops re-compaction from stacking headers.
	compactionSummaryStableMarker = "[stable review state]"

	// compactionSummaryStableHeader opens the findings block carried across
	// compactions.
	compactionSummaryStableHeader = compactionSummaryStableMarker +
		" Findings recorded in earlier turns, carried verbatim. This block is stable across compactions and is the source of truth."

	// compactionSummaryNoRefetchRule replaces the stripped directives.
	compactionSummaryNoRefetchRule = "Do NOT re-run git show/git grep/coder_bash/coder_read or re-read .rev/*.txt, .review_tmp/* " +
		"or any other snapshot to recover what is recorded above: those tool outputs are ephemeral and re-fetching them re-enters the " +
		"same loop. Fetch again only when a *specifically named* fact is missing from this summary, fetch just that fact, and then extend " +
		"this block with it."

	// compactionSummaryDirectiveReplacement marks where a volatile directive
	// was removed, so the model sees that evidence was dropped here rather than
	// never collected.
	compactionSummaryDirectiveReplacement = "[re-read directive removed: the findings it pointed at are recorded above]"
)

// compactionRefetchVerbRe matches a re-fetch verb (read/run/fetch/parse/check a
// thing again). A directive needs one of these plus a hint (below), so a finding
// that merely asserts behaviour ("the retry loop re-reads the config on every
// request") is not treated as an instruction.
var compactionRefetchVerbRe = regexp.MustCompile(`(?i)\bre-?(read|reads|run|runs|fetch|fetches|issue|issues|parse|parses|open|opens|check|checks|rerun|re-run)\b`)

// compactionDirectiveHintRe matches wording that only appears when the sentence
// tells the reader to go fetch evidence again, or points at an ephemeral
// artifact path as a source of truth.
// The hint list deliberately excludes a bare "re-reads"/"re-runs": those appear
// inside findings about *code behaviour* ("the retry helper re-reads the config
// file on every attempt"), which must survive verbatim.
var compactionDirectiveHintRe = regexp.MustCompile(`(?i)(if needed|as needed|if necessary|when needed|instead|(again)\b|you must|you should|remember to|be sure to|\.rev/|\.review_tmp/|evicted from context|was evicted|were evicted|later evicted)`)

// compactionEvictionNarrationRe matches a line that only narrates what the
// context budget did to the transcript. Combined with the "not a finding" guard
// below (no `code` span and no ci#/issue reference), those lines are dropped.
var compactionEvictionNarrationRe = regexp.MustCompile(`(?i)\b(evicted|eviction|dropped from the context|context ~?\d+% full)\b`)

// CompactionSummaryReport describes what SanitizeCompactionSummary did, so the
// de-looping is observable the way the context budget is (`compaction_loops` /
// `volatile_directives` in the ticket's wording).
type CompactionSummaryReport struct {
	// VolatileDirectives counts the lines removed because they instructed a
	// re-fetch, EvictionNarration the lines removed because they only narrated
	// what the guard evicted.
	VolatileDirectives int `json:"volatile_directives"`
	EvictionNarration  int `json:"eviction_narration"`
	// Stabilized reports whether this pass wrapped the summary in the stable
	// block, AlreadyStable whether it already carried it.
	Stabilized    bool `json:"stabilized"`
	AlreadyStable bool `json:"already_stable"`
}

// Replaced reports whether the pass removed any volatile instruction.
func (r CompactionSummaryReport) Replaced() bool {
	return r.VolatileDirectives > 0 || r.EvictionNarration > 0
}

// CompactionLoopSuspected reports whether a summary still carries a re-fetch
// directive, i.e. persisting it would keep the compaction loop alive. It is the
// signal the turn metadata exposes as `compaction_loops`.
func CompactionLoopSuspected(summary string) bool {
	lines := strings.Split(strings.ReplaceAll(summary, "\r\n", "\n"), "\n")
	for _, line := range lines {
		if compactionSummaryStableMarker != "" && strings.Contains(line, compactionSummaryStableMarker) {
			return false
		}
	}
	_, report := SanitizeCompactionSummary(summary)
	return report.Replaced()
}

// SanitizeCompactionSummary makes a persisted compaction summary self-contained
// and stable (ci#793): it drops the directive and eviction-narration lines, keeps
// every remaining line (the findings) verbatim, and frames the result with the
// stable findings block and the no-re-fetch rule.
//
// It is idempotent: an already stabilized summary is returned unchanged, so
// re-compacting a summary neither duplicates the block nor re-arms a directive.
func SanitizeCompactionSummary(summary string) (string, CompactionSummaryReport) {
	report := CompactionSummaryReport{}
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return "", report
	}
	if strings.Contains(trimmed, compactionSummaryStableMarker) {
		report.AlreadyStable = true
		return trimmed, report
	}

	kept := make([]string, 0, 16)
	for _, raw := range strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if isCompactionRefetchDirective(line) {
			report.VolatileDirectives++
			continue
		}
		if isEvictionNarrationOnly(line) {
			report.EvictionNarration++
			continue
		}
		kept = append(kept, line)
	}

	findings := strings.Join(kept, "\n")
	if strings.TrimSpace(findings) == "" {
		findings = "(no findings were recorded before this compaction - collect them once and extend this block)"
	}

	var out strings.Builder
	out.WriteString(compactionSummaryStableHeader)
	out.WriteString("\n")
	if report.Replaced() {
		out.WriteString(compactionSummaryDirectiveReplacement)
		out.WriteString("\n")
	}
	out.WriteString(findings)
	out.WriteString("\n")
	out.WriteString(compactionSummaryNoRefetchRule)
	report.Stabilized = true
	return out.String(), report
}

// isCompactionRefetchDirective reports whether a line tells the reader to fetch
// already-collected evidence again.
func isCompactionRefetchDirective(line string) bool {
	return compactionRefetchVerbRe.MatchString(line) && compactionDirectiveHintRe.MatchString(line)
}

// isEvictionNarrationOnly reports whether a line only narrates the context
// budget's work. Lines that look like a finding (they quote `code` or a ci#/PR
// reference) are preserved even when they mention eviction.
func isEvictionNarrationOnly(line string) bool {
	if !compactionEvictionNarrationRe.MatchString(line) {
		return false
	}
	if strings.Contains(line, "`") || strings.Contains(line, "ci#") || strings.Contains(line, "PR #") {
		return false
	}
	return isCompactionRefetchDirective(line) || len(line) < 240
}

// FrameCompactionSummaryForContext renders the summary injected as a leading
// message. It is what injectContextSummary uses, so the persisted summary and
// every re-injected copy are always the stable form.
func FrameCompactionSummaryForContext(summary string) (string, CompactionSummaryReport) {
	return SanitizeCompactionSummary(summary)
}
