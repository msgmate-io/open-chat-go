package nativecoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file is the machine-readable form of the opencode parity tracker
// (ci#394): the list of capability gaps between the native `msgmate_coder` agent
// and the `opencode` integration, the phase each gap is sequenced into, the tool
// that closes it and the legacy opencode tool it replaces.
//
// It is the single source of truth for the replacement goal (ci#365 / ci#390):
//
//   - the in-repo unit tests validate the registry (unique ids, known phases and
//     statuses, well-formed tool names, no contradiction between a `closed` gap
//     and the tools the coder actually ships),
//   - the retirement decision can ask for the *blocking* gaps
//     (`RetirementParityBlockers`) instead of relying on a hand-written list, and
//   - the human-facing summary is rendered from the same data
//     (`RenderOpenCodeParityMarkdown`), so a gap cannot be closed in the docs
//     without being closed here.
//
// Relationship to the ci#584 rollout: `RetirementBlockers` in rollout.go decides
// *whether the rollout mechanics allow* dropping opencode (every family flipped,
// rollback proven, fleet stable), while `RetirementParityBlockers` decides
// *whether the coder is capable enough* to be the only runtime. A family kept on
// opencode because of an unclosed gap belongs in the migration state's
// `RetainedParity` (with a recorded reason); while this registry reports open
// blocking gaps, that exception is the expected state, never a silent default.
//
// No secrets, tokens or deployment configuration are part of this registry: it
// contains gap descriptions, tool names, phase names and ticket numbers only.

// OpenCodeParityPlanVersion is the registry schema version. The live harness and
// the rollout state report it with every evaluation so evidence can be
// attributed to a registry revision.
const OpenCodeParityPlanVersion = 1

// Parity phases mirror the sequencing of ci#394: cheap, high-value gaps first,
// medium-sized parity work next, the genuinely large integrations last.
const (
	// ParityPhaseNow is the low-effort / high-value batch (todo list, session
	// trust, streaming+background shell).
	ParityPhaseNow = "now"
	// ParityPhaseNext is the medium batch (patch/multi-edit, image and
	// structured reads, context compaction).
	ParityPhaseNext = "next"
	// ParityPhaseLater is the large batch (sub-agents, code search/LSP, MCP).
	ParityPhaseLater = "later"
)

// Parity statuses describe how far a gap is closed. `partial` means the area
// exists but the opencode capability is not reached yet (a tool is present with
// a reduced interface) - it still blocks retirement.
const (
	// ParityStatusOpen means the capability is missing entirely.
	ParityStatusOpen = "open"
	// ParityStatusPartial means the capability exists in a reduced form.
	ParityStatusPartial = "partial"
	// ParityStatusClosed means the coder reaches opencode parity for the gap.
	ParityStatusClosed = "closed"
)

// CoderSurfaceTools are the tools the native coder exposes today (the surface
// ci#394 diffs against opencode). It is the baseline the registry is validated
// against: a tool named by a `closed` gap must be part of it.
func CoderSurfaceTools() []string {
	return []string{
		"coder_select_workspace",
		"coder_read",
		"coder_list",
		"coder_glob",
		"coder_grep",
		"coder_webfetch",
		"coder_websearch",
		"coder_ask",
		"coder_write",
		"coder_edit",
		"coder_bash",
		"coder_git",
	}
}

// ParityGap is one capability gap between the native coder and opencode.
type ParityGap struct {
	// ID is the stable gap identifier used by tests, the harness and the docs.
	ID string `json:"id"`
	// Title is a one-line human description of the gap.
	Title string `json:"title"`
	// Phase is the ci#394 sequencing bucket (now/next/later).
	Phase string `json:"phase"`
	// Status is how far the gap is closed (open/partial/closed).
	Status string `json:"status"`
	// CoderTools are the coder tools that expose the capability once the gap is
	// closed. An existing tool is listed when the gap extends it (eg coder_bash
	// for background/streaming execution).
	CoderTools []string `json:"coder_tools"`
	// OpenCodeTools are the opencode tools the capability replaces.
	OpenCodeTools []string `json:"opencode_tools"`
	// BlocksRetirement reports whether the gap must be closed before opencode
	// can be retired. A non-blocking gap is a recorded exception, not a default.
	BlocksRetirement bool `json:"blocks_retirement"`
	// Tracks lists the tracker tickets this gap belongs to.
	Tracks []int `json:"tracks"`
	// Note explains the current shortfall (and, for an extension gap, what is
	// still missing on an existing tool).
	Note string `json:"note"`
}

// openCodeParityRegistry is the ci#394 registry. Adding a gap means adding an
// entry here; ValidateOpenCodeParityPlan then fails until it is well formed.
var openCodeParityRegistry = []ParityGap{
	{
		ID:     "todo_list",
		Title:  "Plan and update a visible task list",
		Phase:  ParityPhaseNow,
		Status: ParityStatusPartial,
		CoderTools: []string{
			"coder_todo_write",
			"coder_todo_read",
		},
		OpenCodeTools:    []string{"todowrite", "todoread"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "todoplan.go ships the per-chat plan state (validation, revision, render) but the coder surface still lacks the coder_todo_write/coder_todo_read tools, so the agent cannot reach it yet.",
	},
	{
		ID:     "session_trust",
		Title:  "Trust a session so repeated confirmations are not asked again",
		Phase:  ParityPhaseNow,
		Status: ParityStatusPartial,
		CoderTools: []string{
			"coder_trust",
		},
		OpenCodeTools:    []string{"permission allow-for-session"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "sessiontrust.go ships the per-chat trust state (action classes, grant/revoke, revision, AllowsTool) but the coder_trust tool is not exposed yet; every mutating action is still confirmed one at a time.",
	},
	{
		ID:     "shell_modes",
		Title:  "Long-running, streaming and interactive shell commands",
		Phase:  ParityPhaseNow,
		Status: ParityStatusPartial,
		CoderTools: []string{
			"coder_bash",
		},
		OpenCodeTools:    []string{"bash (background/streaming)"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "shellmodes.go ships the per-chat session state (start/append/tail-by-offset/kill, background, streaming and interactive modes, bounded output tail) but coder_bash still runs to completion under a timeout: the tool surface does not expose the modes yet, so dev servers, watchers and tail -f cannot be used from a run.",
	},
	{
		ID:     "multi_edit_patch",
		Title:  "Apply a multi-hunk patch in one call",
		Phase:  ParityPhaseNext,
		Status: ParityStatusOpen,
		CoderTools: []string{
			"coder_patch",
		},
		OpenCodeTools:    []string{"patch"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "Only single exact-string coder_edit exists; a large refactor costs one confirmation per hunk.",
	},
	{
		ID:     "image_and_structured_reads",
		Title:  "View images and structured documents",
		Phase:  ParityPhaseNext,
		Status: ParityStatusPartial,
		CoderTools: []string{
			"coder_read",
		},
		OpenCodeTools:    []string{"read (image attachments)"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "coder_read reports a binary file but cannot decode images, PDFs or notebooks, and the coder cannot attach an image to the model.",
	},
	{
		ID:     "context_compaction",
		Title:  "Compact a long session before the context window is exhausted",
		Phase:  ParityPhaseNext,
		Status: ParityStatusPartial,
		CoderTools: []string{
			"coder_context",
		},
		OpenCodeTools:    []string{"summarize/compaction"},
		BlocksRetirement: true,
		Tracks:           []int{394, 680},
		Note:             "coder_context reports the budget but nothing summarizes a long interaction, so the turn can still end in context_window_exceeded.",
	},
	{
		ID:     "subagents",
		Title:  "Delegate a subtask to a sub-agent with a fresh context",
		Phase:  ParityPhaseLater,
		Status: ParityStatusOpen,
		CoderTools: []string{
			"coder_task",
		},
		OpenCodeTools:    []string{"task"},
		BlocksRetirement: true,
		Tracks:           []int{394, 514},
		Note:             "Everything runs in one context window; there is no fresh-context, tool-restricted delegate.",
	},
	{
		ID:     "code_search_and_lsp",
		Title:  "Semantic code search and symbol/LSP navigation",
		Phase:  ParityPhaseLater,
		Status: ParityStatusOpen,
		CoderTools: []string{
			"coder_codesearch",
			"coder_diagnostics",
		},
		OpenCodeTools:    []string{"codesearch", "lsp"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "Only coder_grep (text) exists: no semantic search, no goto-definition / find-references, no diagnostics.",
	},
	{
		ID:     "mcp_servers",
		Title:  "Expose per-workspace MCP servers as tools",
		Phase:  ParityPhaseLater,
		Status: ParityStatusOpen,
		CoderTools: []string{
			"coder_mcp_list",
			"coder_mcp_call",
		},
		OpenCodeTools:    []string{"mcp"},
		BlocksRetirement: true,
		Tracks:           []int{394},
		Note:             "No per-workspace MCP tool exposure at all.",
	},
}

// OpenCodeParityGaps returns the registry as a copy sorted by gap id, so callers
// (and the JSON evidence of a run) get a deterministic order regardless of how
// the registry literal is laid out.
func OpenCodeParityGaps() []ParityGap {
	gaps := make([]ParityGap, len(openCodeParityRegistry))
	copy(gaps, openCodeParityRegistry)
	for i := range gaps {
		gaps[i] = cloneParityGap(gaps[i])
	}
	sort.SliceStable(gaps, func(i, j int) bool { return gaps[i].ID < gaps[j].ID })
	return gaps
}

// OpenCodeParityGap returns the gap with the given id.
func OpenCodeParityGap(id string) (ParityGap, bool) {
	id = strings.TrimSpace(id)
	for _, gap := range openCodeParityRegistry {
		if gap.ID == id {
			return cloneParityGap(gap), true
		}
	}
	return ParityGap{}, false
}

// OpenCodeParityGapIDs returns every gap id, sorted - the harness wants a stable
// list to report which gaps a run exercised.
func OpenCodeParityGapIDs() []string {
	gaps := OpenCodeParityGaps()
	ids := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		ids = append(ids, gap.ID)
	}
	return ids
}

// OpenCodeParityPhases returns the gap ids per phase, each phase sorted, so the
// sequencing of ci#394 is readable from code instead of a comment.
func OpenCodeParityPhases() map[string][]string {
	out := map[string][]string{
		ParityPhaseNow:   {},
		ParityPhaseNext:  {},
		ParityPhaseLater: {},
	}
	for _, gap := range OpenCodeParityGaps() {
		if _, known := out[gap.Phase]; !known {
			continue
		}
		out[gap.Phase] = append(out[gap.Phase], gap.ID)
	}
	return out
}

// UnclosedOpenCodeParityGaps returns every gap that is not closed, sorted by id.
func UnclosedOpenCodeParityGaps() []ParityGap {
	var out []ParityGap
	for _, gap := range OpenCodeParityGaps() {
		if gap.Status != ParityStatusClosed {
			out = append(out, gap)
		}
	}
	return out
}

// PendingParityTools returns the coder tools that a non-closed gap still has to
// add, ie tools no coder ships today. Tools that extend the existing surface (eg
// coder_bash for background/streaming) are not listed - their gap note carries
// the remaining shortfall - so the list is exactly "what the coder must grow".
func PendingParityTools() []string {
	return PendingParityToolsFor(OpenCodeParityGaps())
}

// PendingParityToolsFor is the testable form of PendingParityTools.
func PendingParityToolsFor(gaps []ParityGap) []string {
	implemented := map[string]bool{}
	for _, tool := range CoderSurfaceTools() {
		implemented[tool] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, gap := range gaps {
		if gap.Status == ParityStatusClosed {
			continue
		}
		for _, tool := range gap.CoderTools {
			tool = strings.TrimSpace(tool)
			if tool == "" || implemented[tool] || seen[tool] {
				continue
			}
			seen[tool] = true
			out = append(out, tool)
		}
	}
	sort.Strings(out)
	return out
}

// RetirementParityBlockers returns one human-readable line per gap that is still
// not closed and blocks retirement. An empty result is the "opencode can be
// retired" condition; anything else is the explanation a `RetainedParity` entry
// must record while a family stays on the legacy runtime.
func RetirementParityBlockers() []string {
	return RetirementParityBlockersFor(OpenCodeParityGaps())
}

// RetirementParityBlockersFor is the testable form of RetirementParityBlockers.
func RetirementParityBlockersFor(gaps []ParityGap) []string {
	var blockers []string
	for _, gap := range gaps {
		if gap.Status == ParityStatusClosed || !gap.BlocksRetirement {
			continue
		}
		blockers = append(blockers, fmt.Sprintf("opencode parity gap %q is %s (%s): %s",
			gap.ID, gap.Status, gap.Phase, gap.Title))
	}
	sort.Strings(blockers)
	return blockers
}

// OpenCodeParityJSON renders the whole registry as stable JSON (sorted by id) for
// run evidence and for the live harness.
func OpenCodeParityJSON() (string, error) {
	encoded, err := json.Marshal(struct {
		Version        int         `json:"version"`
		CoderTools     []string    `json:"coder_tools"`
		PendingTools   []string    `json:"pending_tools"`
		Gaps           []ParityGap `json:"gaps"`
		RetirementOpen bool        `json:"retirement_open"`
	}{
		Version:        OpenCodeParityPlanVersion,
		CoderTools:     CoderSurfaceTools(),
		PendingTools:   PendingParityTools(),
		Gaps:           OpenCodeParityGaps(),
		RetirementOpen: len(RetirementParityBlockers()) == 0,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// RenderOpenCodeParityMarkdown renders the registry as the human-facing parity
// summary (no secrets, tool and phase names only).
func RenderOpenCodeParityMarkdown() string {
	var b strings.Builder
	b.WriteString("| id | gap | phase | status | coder tools | opencode | blocks retirement |\n")
	b.WriteString("|----|-----|-------|--------|-------------|----------|-------------------|\n")
	for _, gap := range OpenCodeParityGaps() {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			gap.ID,
			gap.Title,
			gap.Phase,
			gap.Status,
			strings.Join(gap.CoderTools, ", "),
			strings.Join(gap.OpenCodeTools, ", "),
			parityYesNo(gap.BlocksRetirement),
		)
	}
	return b.String()
}

func parityYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// ValidateOpenCodeParityPlan validates the shipped registry.
func ValidateOpenCodeParityPlan() error {
	return ValidateOpenCodeParityGaps(OpenCodeParityGaps())
}

// ValidateOpenCodeParityGaps validates a registry: stable unique ids, known
// phases and statuses, well-formed tool names, at least one tracker ticket per
// gap, no tool claimed by two gaps, and no `closed` gap that names a coder tool
// the coder does not ship.
func ValidateOpenCodeParityGaps(gaps []ParityGap) error {
	if len(gaps) == 0 {
		return fmt.Errorf("opencode parity registry is empty")
	}
	knownPhases := map[string]bool{ParityPhaseNow: true, ParityPhaseNext: true, ParityPhaseLater: true}
	knownStatuses := map[string]bool{ParityStatusOpen: true, ParityStatusPartial: true, ParityStatusClosed: true}
	implemented := map[string]bool{}
	for _, tool := range CoderSurfaceTools() {
		implemented[tool] = true
	}

	seenIDs := map[string]bool{}
	toolOwner := map[string]string{}
	for _, gap := range gaps {
		id := strings.TrimSpace(gap.ID)
		switch {
		case id == "":
			return fmt.Errorf("opencode parity gap has an empty id")
		case id != gap.ID:
			return fmt.Errorf("opencode parity gap id %q has surrounding whitespace", gap.ID)
		case seenIDs[id]:
			return fmt.Errorf("duplicate opencode parity gap id %q", id)
		case strings.ToLower(id) != id:
			return fmt.Errorf("opencode parity gap id %q must be lowercase", id)
		}
		seenIDs[id] = true

		if strings.TrimSpace(gap.Title) == "" {
			return fmt.Errorf("opencode parity gap %q has no title", id)
		}
		if !knownPhases[gap.Phase] {
			return fmt.Errorf("opencode parity gap %q has unknown phase %q", id, gap.Phase)
		}
		if !knownStatuses[gap.Status] {
			return fmt.Errorf("opencode parity gap %q has unknown status %q", id, gap.Status)
		}
		if len(gap.Tracks) == 0 {
			return fmt.Errorf("opencode parity gap %q tracks no ticket", id)
		}
		for _, ticket := range gap.Tracks {
			if ticket <= 0 {
				return fmt.Errorf("opencode parity gap %q has an invalid tracker ticket %d", id, ticket)
			}
		}
		if len(gap.CoderTools) == 0 {
			return fmt.Errorf("opencode parity gap %q names no coder tool", id)
		}
		if len(gap.OpenCodeTools) == 0 {
			return fmt.Errorf("opencode parity gap %q names no opencode tool", id)
		}
		if strings.TrimSpace(gap.Note) == "" {
			return fmt.Errorf("opencode parity gap %q has no note", id)
		}
		for _, tool := range gap.CoderTools {
			if !strings.HasPrefix(tool, "coder_") {
				return fmt.Errorf("opencode parity gap %q names non-coder tool %q", id, tool)
			}
			if !implemented[tool] && gap.Status == ParityStatusClosed {
				return fmt.Errorf("opencode parity gap %q is closed but the coder does not ship %q", id, tool)
			}
		}
		for _, tool := range gap.CoderTools {
			if owner, taken := toolOwner[tool]; taken {
				return fmt.Errorf("coder tool %q is claimed by both parity gap %q and %q", tool, owner, id)
			}
			toolOwner[tool] = id
		}
	}
	return nil
}

// cloneParityGap copies a gap (and its slices) so a caller can never mutate the
// package registry through a returned value.
func cloneParityGap(gap ParityGap) ParityGap {
	gap.CoderTools = append([]string(nil), gap.CoderTools...)
	gap.OpenCodeTools = append([]string(nil), gap.OpenCodeTools...)
	gap.Tracks = append([]int(nil), gap.Tracks...)
	return gap
}
