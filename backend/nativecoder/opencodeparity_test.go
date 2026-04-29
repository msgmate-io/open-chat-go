package nativecoder

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOpenCodeParityRegistryIsValid guards the shipped ci#394 registry.
func TestOpenCodeParityRegistryIsValid(t *testing.T) {
	if err := ValidateOpenCodeParityPlan(); err != nil {
		t.Fatalf("shipped opencode parity registry is invalid: %v", err)
	}
	gaps := OpenCodeParityGaps()
	if len(gaps) != 9 {
		t.Fatalf("expected the 9 opencode parity gaps of ci#394, got %d", len(gaps))
	}
	want := []string{
		"code_search_and_lsp",
		"context_compaction",
		"image_and_structured_reads",
		"mcp_servers",
		"multi_edit_patch",
		"session_trust",
		"shell_modes",
		"subagents",
		"todo_list",
	}
	got := OpenCodeParityGapIDs()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected gap ids: got %v want %v", got, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("gap ids must be sorted and unique, got %v", got)
		}
	}
}

// TestOpenCodeParityGapsTrackTheTicketAndPhase guards that every gap is
// attributable: it belongs to ci#394, sits in exactly one phase of the suggested
// sequencing and names both the coder tool and the opencode capability.
func TestOpenCodeParityGapsTrackTheTicketAndPhase(t *testing.T) {
	phases := OpenCodeParityPhases()
	if len(phases[ParityPhaseNow])+len(phases[ParityPhaseNext])+len(phases[ParityPhaseLater]) != len(OpenCodeParityGaps()) {
		t.Fatalf("every gap must belong to exactly one phase, got %v", phases)
	}
	for _, phase := range []string{ParityPhaseNow, ParityPhaseNext, ParityPhaseLater} {
		if len(phases[phase]) == 0 {
			t.Errorf("phase %q is empty: ci#394 sequences three batches", phase)
		}
	}
	for _, gap := range OpenCodeParityGaps() {
		found := false
		for _, ticket := range gap.Tracks {
			if ticket == 394 {
				found = true
			}
		}
		if !found {
			t.Errorf("gap %q does not track ci#394", gap.ID)
		}
		if len(gap.OpenCodeTools) == 0 {
			t.Errorf("gap %q names no opencode capability", gap.ID)
		}
		if gap.Status == ParityStatusClosed {
			t.Errorf("gap %q is claimed closed; closing a gap requires a coder tool that ships", gap.ID)
		}
	}
	now := phases[ParityPhaseNow]
	if len(now) < 3 {
		t.Errorf("expected todo list, session trust and shell modes in phase %q, got %v", ParityPhaseNow, now)
	}
}

// TestPendingParityToolsAreOutsideTheCurrentSurface guards the "what the coder
// must grow" list: it never names a tool the coder already ships.
func TestPendingParityToolsAreOutsideTheCurrentSurface(t *testing.T) {
	surface := map[string]bool{}
	for _, tool := range CoderSurfaceTools() {
		surface[tool] = true
	}
	pending := PendingParityTools()
	if len(pending) == 0 {
		t.Fatal("expected pending parity tools while gaps are open")
	}
	for _, tool := range pending {
		if surface[tool] {
			t.Errorf("pending tool %q is already part of the coder surface", tool)
		}
	}
	for _, want := range []string{"coder_todo_write", "coder_todo_read", "coder_trust", "coder_patch"} {
		found := false
		for _, tool := range pending {
			if tool == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q in the pending parity tools, got %v", want, pending)
		}
	}
	if containsString(pending, "coder_bash") {
		t.Error("coder_bash exists: an extension gap must not be reported as a missing tool")
	}

	// A closed gap stops contributing its tools, and an extension gap never does.
	closed := OpenCodeParityGaps()
	for i := range closed {
		if closed[i].ID == "todo_list" {
			closed[i].Status = ParityStatusClosed
		}
	}
	remaining := PendingParityToolsFor(closed)
	for _, tool := range remaining {
		if tool == "coder_todo_write" || tool == "coder_todo_read" {
			t.Errorf("closed gap still reported %q as pending", tool)
		}
	}
}

// TestRetirementParityBlockersListOpenGaps guards that the retirement gate reads
// the registry: while any blocking gap is open, replacing opencode is not
// allowed, and the blockers name the gap and its phase.
func TestRetirementParityBlockersListOpenGaps(t *testing.T) {
	blockers := RetirementParityBlockers()
	if len(blockers) == 0 {
		t.Fatal("expected opencode parity to block retirement while gaps are open")
	}
	joined := strings.Join(blockers, "\n")
	for _, want := range []string{"todo_list", "multi_edit_patch", "mcp_servers"} {
		if !strings.Contains(joined, want) {
			t.Errorf("blockers do not mention gap %q:\n%s", want, joined)
		}
	}

	// Closing every gap clears the parity gate.
	closed := OpenCodeParityGaps()
	for i := range closed {
		closed[i].Status = ParityStatusClosed
	}
	if got := RetirementParityBlockersFor(closed); len(got) != 0 {
		t.Fatalf("expected no parity blockers once every gap is closed, got %v", got)
	}

	// A non-blocking, still-open gap is a recorded exception, not a gate.
	exception := []ParityGap{{
		ID: "cosmetic", Title: "cosmetic only", Phase: ParityPhaseLater, Status: ParityStatusOpen,
		CoderTools: []string{"coder_cosmetic"}, OpenCodeTools: []string{"cosmetic"},
		Tracks: []int{394}, Note: "does not gate retirement",
	}}
	if got := RetirementParityBlockersFor(exception); len(got) != 0 {
		t.Fatalf("expected a non-blocking gap to stay out of the gate, got %v", got)
	}
}

// TestParityBlockersAreTheOnlyGateOnceTheRolloutIsComplete ties the ci#394
// parity registry to the ci#584 rollout: with every family flipped and the
// rollback proven on a non-critical family, the mechanical blockers are gone and
// the parity gaps are what still keeps opencode in the deployment profile - which
// is exactly the retention the migration state has to record with a reason.
func TestParityBlockersAreTheOnlyGateOnceTheRolloutIsComplete(t *testing.T) {
	stages := NativeBotStages()
	last := stages[len(stages)-1]
	repl, ok := NativeBotStageByName(LegacyRuntimeBotAliases()["opencode"])
	if !ok {
		t.Fatal("the opencode alias does not resolve to a rollout family")
	}

	state := NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
		RetainedParity:   map[string]string{repl.Stage: "opencode parity gaps of ci#394 are still open"},
	}
	if blockers := RetirementBlockers(state); len(blockers) != 0 {
		t.Fatalf("a fully flipped, rollback-proven fleet with a recorded retention must have no mechanical blocker, got %v", blockers)
	}
	if len(RetirementParityBlockers()) == 0 {
		t.Fatal("expected the parity gate to still hold opencode while ci#394 is open")
	}

	// The composed gate is what a runbook actually asks: mechanics plus parity.
	readiness := RetirementReadiness(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
		RetainedParity:   ParityRetentionEntries(),
	})
	joinedReadiness := strings.Join(readiness, "\n")
	if !strings.Contains(joinedReadiness, "todo_list") {
		t.Fatalf("the composed retirement gate must report the open ci#394 gaps, got %v", readiness)
	}
	if strings.Contains(joinedReadiness, "still runs opencode") {
		t.Fatalf("no family should still run opencode in this state, got %v", readiness)
	}

	// ParityRetentionEntries is the derived exception the migration state needs;
	// without it the rollout state itself flags the retention as unreasoned.
	if entries := ParityRetentionEntries(); len(entries) != len(stages) {
		t.Fatalf("expected a derived parity reason for every family, got %v", entries)
	}
	if blockers := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
		RetainedParity:   ParityRetentionEntries(),
	}); len(blockers) != 0 {
		t.Fatalf("a derived retention reason must satisfy the rollout state, got %v", blockers)
	}
	if blockers := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
		RetainedParity:   map[string]string{repl.Stage: "  "},
	}); len(blockers) == 0 {
		t.Fatal("a blank retention reason must stay a blocker")
	}

	// Metric-compatible successor: once the gap registry is closed, retirement
	// needs no retention exception at all.
	closed := OpenCodeParityGaps()
	for i := range closed {
		closed[i].Status = ParityStatusClosed
	}
	if blockers := RetirementBlockers(NativeBotMigrationState{
		RolledOutThrough: last.Stage,
		RollbackProvenOn: stages[0].Stage,
		NativeStableDays: NativeBotStabilityWindowDays,
	}); len(blockers) != 0 {
		t.Fatalf("expected a clean retirement once parity is closed, got %v", blockers)
	}
}

// TestOpenCodeParityValidatorRejectsMalformedRegistries keeps the registry
// honest: a bad entry fails validation instead of silently shipping.
func TestOpenCodeParityValidatorRejectsMalformedRegistries(t *testing.T) {
	base, ok := OpenCodeParityGap("todo_list")
	if !ok || base.ID == "" || len(base.CoderTools) == 0 {
		t.Fatal("expected the todo_list gap to exist")
	}
	clone := func() ParityGap { return base }

	cases := []struct {
		name string
		gaps []ParityGap
	}{
		{"empty registry", nil},
		{"empty id", []ParityGap{{Title: "t", Phase: ParityPhaseNow, Status: ParityStatusOpen, CoderTools: []string{"coder_x"}, OpenCodeTools: []string{"x"}, Tracks: []int{394}, Note: "n"}}},
		{"duplicate id", []ParityGap{clone(), clone()}},
		{"unknown phase", func() []ParityGap { g := clone(); g.Phase = "soon"; return []ParityGap{g} }()},
		{"unknown status", func() []ParityGap { g := clone(); g.Status = "maybe"; return []ParityGap{g} }()},
		{"closed without shipping the tool", func() []ParityGap { g := clone(); g.Status = ParityStatusClosed; return []ParityGap{g} }()},
		{"no tracker ticket", func() []ParityGap { g := clone(); g.Tracks = nil; return []ParityGap{g} }()},
		{"invalid tracker ticket", func() []ParityGap { g := clone(); g.Tracks = []int{0}; return []ParityGap{g} }()},
		{"no note", func() []ParityGap { g := clone(); g.Note = "  "; return []ParityGap{g} }()},
		{"no title", func() []ParityGap { g := clone(); g.Title = ""; return []ParityGap{g} }()},
		{"no coder tool", func() []ParityGap { g := clone(); g.CoderTools = nil; return []ParityGap{g} }()},
		{"no opencode tool", func() []ParityGap { g := clone(); g.OpenCodeTools = nil; return []ParityGap{g} }()},
		{"non coder tool", func() []ParityGap { g := clone(); g.CoderTools = []string{"bash"}; return []ParityGap{g} }()},
		{"uppercase id", func() []ParityGap { g := clone(); g.ID = "Todo_List"; return []ParityGap{g} }()},
		{"padded id", func() []ParityGap { g := clone(); g.ID = " todo_list"; return []ParityGap{g} }()},
		{"tool claimed twice", func() []ParityGap {
			g := clone()
			other := clone()
			other.ID = "other"
			return []ParityGap{g, other}
		}()},
	}
	for _, tc := range cases {
		if err := ValidateOpenCodeParityGaps(tc.gaps); err == nil {
			t.Errorf("expected %s to be rejected", tc.name)
		}
	}
}

// TestOpenCodeParityGapLookupAndIsolation guards the accessors: lookups are
// trimmed and case-sensitive, and callers can never mutate the registry.
func TestOpenCodeParityGapLookupAndIsolation(t *testing.T) {
	gap, ok := OpenCodeParityGap("  todo_list ")
	if !ok || gap.ID != "todo_list" {
		t.Fatalf("expected a trimmed lookup to find todo_list, got %+v ok=%v", gap, ok)
	}
	if _, ok := OpenCodeParityGap("TODO_LIST"); ok {
		t.Error("gap ids are lowercase and lookups must be case-sensitive")
	}
	if _, ok := OpenCodeParityGap("does_not_exist"); ok {
		t.Error("expected an unknown gap to report ok=false")
	}

	tools := OpenCodeParityGaps()
	phase := OpenCodeParityPhases()
	for i := range tools {
		tools[i].Status = ParityStatusClosed
		tools[i].CoderTools = append(tools[i].CoderTools, "coder_injected")
	}
	phase[ParityPhaseNow] = append(phase[ParityPhaseNow], "injected")
	if got := RetirementParityBlockers(); len(got) == 0 {
		t.Fatal("mutating a returned gap must not close the shipped registry")
	}
	after, _ := OpenCodeParityGap("todo_list")
	if after.Status == ParityStatusClosed || containsString(after.CoderTools, "coder_injected") {
		t.Fatalf("the registry leaked a mutation: %+v", after)
	}
	if containsString(OpenCodeParityPhases()[ParityPhaseNow], "injected") {
		t.Fatal("the phase view leaked a mutation")
	}
}

// TestRenderedOpenCodeParityDocsCoverTheRegistry guards the human-facing summary:
// every gap and both tool columns appear, and no secret-ish content is rendered.
func TestRenderedOpenCodeParityDocsCoverTheRegistry(t *testing.T) {
	markdown := RenderOpenCodeParityMarkdown()
	rows := strings.Count(markdown, "\n| ")
	if rows != len(OpenCodeParityGaps()) {
		t.Fatalf("expected %d gap rows, got %d:\n%s", len(OpenCodeParityGaps()), rows, markdown)
	}
	for _, gap := range OpenCodeParityGaps() {
		if !strings.Contains(markdown, gap.ID) {
			t.Errorf("parity summary is missing gap %q", gap.ID)
		}
		if !strings.Contains(markdown, gap.CoderTools[0]) {
			t.Errorf("parity summary is missing the coder tool of %q", gap.ID)
		}
	}
	for _, gap := range UnclosedOpenCodeParityGaps() {
		if !strings.Contains(markdown, gap.Phase) {
			t.Errorf("parity summary is missing the phase of %q", gap.ID)
		}
	}
	if strings.Contains(strings.ToLower(markdown), "token") {
		t.Error("the rendered summary must not contain credential-shaped fields")
	}
}

// TestOpenCodeParityJSONIsStableEvidence guards the JSON evidence a run can
// attach: it carries the schema version, the current surface, the pending tools
// and the still-open retirement gate.
func TestOpenCodeParityJSONIsStableEvidence(t *testing.T) {
	raw, err := OpenCodeParityJSON()
	if err != nil {
		t.Fatalf("rendering the parity evidence failed: %v", err)
	}
	var decoded struct {
		Version        int         `json:"version"`
		CoderTools     []string    `json:"coder_tools"`
		PendingTools   []string    `json:"pending_tools"`
		Gaps           []ParityGap `json:"gaps"`
		RetirementOpen bool        `json:"retirement_open"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("parity evidence is not valid JSON: %v", err)
	}
	if decoded.Version != OpenCodeParityPlanVersion {
		t.Errorf("expected schema version %d, got %d", OpenCodeParityPlanVersion, decoded.Version)
	}
	if len(decoded.Gaps) != len(OpenCodeParityGaps()) || len(decoded.CoderTools) != len(CoderSurfaceTools()) {
		t.Errorf("evidence is incomplete: %+v", decoded)
	}
	if decoded.RetirementOpen {
		t.Error("retirement must still be gated while parity gaps are open")
	}
	for i := 1; i < len(decoded.PendingTools); i++ {
		if decoded.PendingTools[i-1] >= decoded.PendingTools[i] {
			t.Errorf("pending tools are not sorted and unique: %v", decoded.PendingTools)
		}
	}
}
