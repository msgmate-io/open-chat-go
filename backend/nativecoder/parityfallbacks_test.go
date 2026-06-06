package nativecoder

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestParityFallbackGuidanceCoversEveryGap guards the core invariant of the
// ci#394 fallback table: while a parity gap is open, the agent must have a
// recorded mitigation for it. A new gap without a fallback line fails here.
func TestParityFallbackGuidanceCoversEveryGap(t *testing.T) {
	if errs := ValidateParityFallbackGuidance(); len(errs) != 0 {
		for _, err := range errs {
			t.Errorf("parity fallback validation: %v", err)
		}
	}
	if uncovered := UncoveredParityGapIDs(); len(uncovered) != 0 {
		t.Fatalf("gaps without fallback guidance: %v", uncovered)
	}
	for _, gap := range OpenCodeParityGaps() {
		text, ok := ParityFallbackFor(gap.ID)
		if !ok || strings.TrimSpace(text) == "" {
			t.Errorf("gap %q has no fallback guidance", gap.ID)
			continue
		}
		if !mentionsShippedTool(text) {
			t.Errorf("gap %q fallback names no shipped coder tool: %s", gap.ID, text)
		}
	}
}

// TestParityFallbackGuidanceHasNoOrphans guards the other direction: guidance
// for a gap that the registry does not know would be dead data (and a typo in a
// gap id would silently drop a mitigation).
func TestParityFallbackGuidanceHasNoOrphans(t *testing.T) {
	registered := map[string]bool{}
	for _, id := range OpenCodeParityGapIDs() {
		registered[id] = true
	}
	for _, id := range ParityFallbackGapIDs() {
		if !registered[id] {
			t.Errorf("fallback guidance for unknown gap %q", id)
		}
		if _, ok := OpenCodeParityGap(id); !ok {
			t.Errorf("gap id %q is not resolvable through OpenCodeParityGap", id)
		}
	}
}

// TestParityFallbackRejectsGuidanceForClosedGap guards the hygiene rule in the
// other direction: once a gap is closed the table must lose its workaround,
// otherwise an agent keeps following a mitigation for a capability the coder
// already ships.
func TestParityFallbackRejectsGuidanceForClosedGap(t *testing.T) {
	closed := []ParityGap{{
		ID:         "todo_list",
		Title:      "t",
		Phase:      ParityPhaseNow,
		Status:     ParityStatusClosed,
		CoderTools: []string{"coder_todo_write"},
	}}
	guidance := map[string]string{"todo_list": "narrate the plan with coder_write"}
	errs := validateParityFallbackGuidance(closed, guidance)
	if len(errs) == 0 {
		t.Fatal("validation accepted stale guidance for a closed gap")
	}
	if !strings.Contains(errs[0].Error(), "closed") {
		t.Errorf("error %q does not explain the closed gap", errs[0])
	}
	if got := uncoveredParityGapIDs(closed, guidance); len(got) != 0 {
		t.Errorf("uncoveredParityGapIDs(closed gap) = %v, want empty", got)
	}
	if errs := validateParityFallbackGuidance(closed, map[string]string{}); len(errs) != 0 {
		t.Errorf("closed gap without guidance should validate, got %v", errs)
	}
	// The shipped registry must satisfy the rule as well.
	if errs := ValidateParityFallbackGuidance(); len(errs) != 0 {
		for _, err := range errs {
			t.Errorf("shipped registry: %v", err)
		}
	}
}

// TestParityFallbackValidationRejectsDrift feeds the validation a deliberately
// broken pair of inputs, so the gate is proven to fail (a validation that can
// only pass is not a gate).
func TestParityFallbackValidationRejectsDrift(t *testing.T) {
	gaps := []ParityGap{
		{ID: "known", Title: "t", Phase: ParityPhaseNow, Status: ParityStatusOpen},
		{ID: "uncovered", Title: "t", Phase: ParityPhaseNow, Status: ParityStatusOpen},
		{ID: "aimless", Title: "t", Phase: ParityPhaseNow, Status: ParityStatusOpen},
	}
	guidance := map[string]string{
		"known":     "batch the work into one coder_bash call",
		"aimless":   "say something vague without naming a tool",
		"uncovered": "   ",
		"orphan":    "call coder_read",
	}
	errs := validateParityFallbackGuidance(gaps, guidance)
	joined := make([]string, 0, len(errs))
	for _, err := range errs {
		joined = append(joined, err.Error())
	}
	report := strings.Join(joined, "\n")
	for _, want := range []string{"uncovered", "aimless", "orphan"} {
		if !strings.Contains(report, want) {
			t.Errorf("validation did not report %q, got:\n%s", want, report)
		}
	}
	if strings.Contains(report, "\"known\"") {
		t.Errorf("validation reported the healthy gap:\n%s", report)
	}
	if missing := uncoveredParityGapIDs(gaps, guidance); len(missing) != 1 || missing[0] != "uncovered" {
		t.Errorf("uncoveredParityGapIDs = %v, want [uncovered]", missing)
	}
}

// TestParityFallbackLookupIsForgiving guards the accessor an agent/tool wrapper
// uses: whitespace around a copied gap id must not lose the guidance, while an
// empty or unknown id is reported as absent instead of returning an empty line.
func TestParityFallbackLookupIsForgiving(t *testing.T) {
	if _, ok := ParityFallbackFor("  todo_list  "); !ok {
		t.Fatal("padded gap id did not resolve")
	}
	for _, id := range []string{"", "   ", "not_a_gap"} {
		if text, ok := ParityFallbackFor(id); ok {
			t.Errorf("ParityFallbackFor(%q) = %q, true; want false", id, text)
		}
	}
}

// TestParityFallbackAccessorsAreCopies guards that a caller cannot mutate the
// package data (or shadow it between calls).
func TestParityFallbackAccessorsAreCopies(t *testing.T) {
	first := ParityFallbackGuidance()
	if len(first) == 0 {
		t.Fatal("no fallback guidance shipped")
	}
	for id := range first {
		first[id] = "mutated"
	}
	second := ParityFallbackGuidance()
	for id, text := range second {
		if text == "mutated" {
			t.Errorf("gap %q guidance was mutated through the accessor", id)
		}
	}
}

// TestParityFallbackLinesAreSortedAndLabelled guards the shape a prompt or a
// runbook consumes: one "gap id: guidance" line per gap, sorted by id.
func TestParityFallbackLinesAreSortedAndLabelled(t *testing.T) {
	lines := ParityFallbackGuidanceLines()
	if len(lines) != len(ParityFallbackGuidance()) {
		t.Fatalf("got %d lines, want %d", len(lines), len(ParityFallbackGuidance()))
	}
	if !sort.StringsAreSorted(lines) {
		t.Errorf("lines are not sorted: %v", lines)
	}
	for _, line := range lines {
		id, text, found := strings.Cut(line, ": ")
		if !found || strings.TrimSpace(text) == "" {
			t.Errorf("line %q is not a labelled fallback", line)
			continue
		}
		if _, ok := OpenCodeParityGap(id); !ok {
			t.Errorf("line %q does not start with a known gap id", line)
		}
	}
}

// TestRenderParityFallbackMarkdownShowsEveryOpenGap guards the human-facing
// table: an agent reading it must find each non-closed gap together with the
// mitigation to use until the gap is closed.
func TestRenderParityFallbackMarkdownShowsEveryOpenGap(t *testing.T) {
	markdown := RenderParityFallbackMarkdown()
	if !strings.Contains(markdown, "| Gap | Phase | Status | Fallback |") {
		t.Fatalf("markdown has no table header:\n%s", markdown)
	}
	if strings.Contains(markdown, "_no fallback recorded_") {
		t.Errorf("markdown reports an unmitigated gap:\n%s", markdown)
	}
	for _, gap := range OpenCodeParityGaps() {
		if !strings.Contains(markdown, "`"+gap.ID+"`") {
			t.Errorf("markdown does not list gap %q", gap.ID)
		}
	}
	for _, gap := range UnclosedOpenCodeParityGaps() {
		text, _ := ParityFallbackFor(gap.ID)
		if !strings.Contains(markdown, text) {
			t.Errorf("markdown does not carry the fallback of open gap %q", gap.ID)
		}
	}
}

// TestParityFallbackJSONIsStableEvidence guards the machine-readable mitigation
// table: it carries the schema version, one entry per registered gap and an
// explicitly empty `uncovered` list, so a run can attach it and a later reader
// can tell a healthy table from a partially documented one.
func TestParityFallbackJSONIsStableEvidence(t *testing.T) {
	raw, err := ParityFallbackJSON()
	if err != nil {
		t.Fatalf("rendering the fallback evidence failed: %v", err)
	}
	var decoded struct {
		Version   int `json:"version"`
		Uncovered []string
		Fallbacks []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Phase    string `json:"phase"`
			Status   string `json:"status"`
			Fallback string `json:"fallback"`
		} `json:"fallbacks"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("fallback evidence is not valid JSON: %v", err)
	}
	if decoded.Version != ParityFallbackVersion {
		t.Errorf("expected schema version %d, got %d", ParityFallbackVersion, decoded.Version)
	}
	if len(decoded.Uncovered) != 0 {
		t.Errorf("evidence reports uncovered gaps: %v", decoded.Uncovered)
	}
	if len(decoded.Fallbacks) != len(OpenCodeParityGaps()) {
		t.Fatalf("got %d entries, want %d", len(decoded.Fallbacks), len(OpenCodeParityGaps()))
	}
	if !json.Valid([]byte(raw)) || raw != strings.TrimSpace(raw) {
		t.Errorf("evidence is not trimmed valid JSON: %q", raw)
	}
	for i := 1; i < len(decoded.Fallbacks); i++ {
		if decoded.Fallbacks[i-1].ID >= decoded.Fallbacks[i].ID {
			t.Errorf("entries are not sorted by unique id: %+v", decoded.Fallbacks)
			break
		}
	}
	for _, entry := range decoded.Fallbacks {
		if entry.Status == ParityStatusClosed {
			continue
		}
		if strings.TrimSpace(entry.Fallback) == "" {
			t.Errorf("entry %q has no fallback in the evidence", entry.ID)
		}
	}
}

// TestParityFallbackGuidanceCarriesNoSecret guards the "never a credential in a
// config/plan" rule of the parity data: the guidance is prompt material, so it
// must never contain something that looks like a token or key.
func TestParityFallbackGuidanceCarriesNoSecret(t *testing.T) {
	for id, text := range ParityFallbackGuidance() {
		lower := strings.ToLower(text)
		for _, marker := range []string{"ghp_", "github_pat_", "sk-", "-----begin", "bearer ", "password="} {
			if strings.Contains(lower, marker) {
				t.Errorf("gap %q fallback looks like it carries a credential (%q)", id, marker)
			}
		}
	}
}

// mentionsShippedTool mirrors the package-level check so the test asserts the
// same predicate the validation uses.
func mentionsShippedTool(text string) bool {
	return fallbackMentionsSurfaceTool(text)
}
