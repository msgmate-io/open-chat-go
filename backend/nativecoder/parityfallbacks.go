package nativecoder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ParityFallbackVersion is the schema version of the fallback evidence document
// (ParityFallbackJSON). Bump it when the document shape changes.
const ParityFallbackVersion = 1

// This file is the *fallback* half of the opencode parity tracker (ci#394).
//
// The registry in opencodeparity.go says which opencode capabilities the native
// coder is still missing. That is a good gate, but it tells an agent nothing
// about what to do *while* the gap is open: without a recorded workaround the
// model either improvises (and wastes a turn on a tool it does not have) or
// gives up on the subtask.
//
// Every gap therefore carries one line of fallback guidance that names at least
// one tool the coder actually ships (CoderSurfaceTools). The invariants are
// machine-checked - every registered gap has guidance, no guidance is attached
// to an unknown gap id, and each line references a shipped coder tool - so the
// guidance cannot silently drift away from the registry or promise a tool the
// coder does not have.
//
// No secrets, tokens or deployment configuration are part of this data: it is
// gap ids, coder tool names and one-line mitigations only.

// parityFallbackGuidance maps a ci#394 gap id to the workaround an agent should
// use with the current coder surface. Keep the gap ids in sync with
// openCodeParityRegistry; ValidateParityFallbackGuidance fails on drift.
var parityFallbackGuidance = map[string]string{
	"todo_list": "Narrate the plan in the first message and restate it whenever it changes; a scratch " +
		"checklist written with coder_write (or a coder_bash heredoc) is the durable stand-in for todowrite.",
	"session_trust": "Batch every related step into one coder_bash call so a single confirmation covers the " +
		"whole batch, and pre-approve the family through the chat's shared-config tools list instead of " +
		"confirming each action again.",
	"shell_modes": "Start dev servers, watchers and tail -f detached through coder_bash with the output " +
		"redirected to a log file in the workspace, then follow it with targeted coder_read calls instead of " +
		"holding the shell open.",
	"multi_edit_patch": "Apply a refactor as a sequence of coder_edit calls, or write the changed files and " +
		"let coder_bash apply one generated patch (git apply) when there are many hunks.",
	"image_and_structured_reads": "Ask for a textual description instead of the image, and inspect " +
		"structured documents by filtering them with coder_grep - a screenshot or a notebook has to be " +
		"reduced to text before the model sees it.",
	"context_compaction": "Call coder_context before a large dump and keep the working set small: " +
		"depth-bounded coder_list, path-scoped coder_grep and ranged coder_read instead of whole trees or files.",
	"subagents": "Split the work across separate chats (dispatch a peer agent) and do the mechanical " +
		"exploration with coder_grep/coder_list so the current context stays small instead of growing a " +
		"second context window.",
	"code_search_and_lsp": "Use coder_grep with a tight pattern for navigation and coder_bash to run the " +
		"project's own type-checker, vet or linter for the diagnostics an LSP would report.",
	"mcp_servers": "Per-workspace MCP tools are not exposed yet: reach the same service through the chat's " +
		"configured mcp: tools, coder_webfetch for HTTP APIs or coder_bash for a CLI that talks to it.",
}

// ParityFallbackFor returns the fallback guidance of a gap id. The lookup trims
// whitespace so a caller that copies the id out of rendered markdown keeps
// working.
func ParityFallbackFor(id string) (string, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", false
	}
	text, ok := parityFallbackGuidance[trimmed]
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// ParityFallbackGuidance returns a copy of the whole gap id -> guidance map, so
// a caller can render or mutate it without touching the package data.
func ParityFallbackGuidance() map[string]string {
	out := make(map[string]string, len(parityFallbackGuidance))
	for id, text := range parityFallbackGuidance {
		out[id] = text
	}
	return out
}

// ParityFallbackGuidanceLines returns "gap id: guidance" lines sorted by gap id,
// which is what a prompt or a runbook wants appended.
func ParityFallbackGuidanceLines() []string {
	ids := ParityFallbackGapIDs()
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf("%s: %s", id, parityFallbackGuidance[id]))
	}
	return lines
}

// ParityFallbackGapIDs returns the ids that have fallback guidance, sorted.
func ParityFallbackGapIDs() []string {
	ids := make([]string, 0, len(parityFallbackGuidance))
	for id := range parityFallbackGuidance {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// UncoveredParityGapIDs returns the registered gaps without fallback guidance,
// sorted. It is empty for a healthy registry and is what the validation reports
// when a new gap was added without a mitigation.
func UncoveredParityGapIDs() []string {
	return uncoveredParityGapIDs(OpenCodeParityGaps(), parityFallbackGuidance)
}

// ValidateParityFallbackGuidance checks the shipped guidance against the shipped
// registry. It returns one error per problem instead of stopping at the first,
// matching ValidateOpenCodeParityPlan.
func ValidateParityFallbackGuidance() []error {
	return validateParityFallbackGuidance(OpenCodeParityGaps(), parityFallbackGuidance)
}

// validateParityFallbackGuidance is the injectable core of the validation so a
// test can feed it a deliberately broken registry or guidance map.
//
// Besides "every gap has guidance" it enforces the converse hygiene rule: a
// *closed* gap must not keep a fallback line. Guidance for a closed gap tells the
// agent to work around a capability the coder now ships, so the line has to be
// deleted with the status flip - otherwise the table slowly fills with stale
// workarounds an agent may still follow.
func validateParityFallbackGuidance(gaps []ParityGap, guidance map[string]string) []error {
	known := make(map[string]bool, len(gaps))
	var errs []error
	for _, gap := range gaps {
		id := strings.TrimSpace(gap.ID)
		if id == "" {
			continue
		}
		known[id] = true
		text := strings.TrimSpace(guidance[id])
		if gap.Status == ParityStatusClosed {
			if text != "" {
				errs = append(errs, fmt.Errorf(
					"parity fallback: gap %q is closed but still carries fallback guidance (closing tools: %s); drop it",
					id, strings.Join(gap.CoderTools, ", ")))
			}
			continue
		}
		if text == "" {
			errs = append(errs, fmt.Errorf("parity fallback: gap %q has no fallback guidance", id))
			continue
		}
		if !fallbackMentionsSurfaceTool(text) {
			errs = append(errs, fmt.Errorf("parity fallback: guidance for gap %q names no shipped coder tool", id))
		}
	}
	for _, id := range sortedFallbackIDs(guidance) {
		if !known[id] {
			errs = append(errs, fmt.Errorf("parity fallback: guidance for unknown gap %q", id))
		}
		if strings.TrimSpace(guidance[id]) == "" {
			errs = append(errs, fmt.Errorf("parity fallback: gap %q has blank fallback guidance", id))
		}
	}
	return errs
}

// uncoveredParityGapIDs is the shared implementation of the uncovered lookup.
// Closed gaps are skipped: they are expected to have no fallback line.
func uncoveredParityGapIDs(gaps []ParityGap, guidance map[string]string) []string {
	var missing []string
	for _, gap := range gaps {
		id := strings.TrimSpace(gap.ID)
		if id == "" || gap.Status == ParityStatusClosed {
			continue
		}
		if text, ok := guidance[id]; !ok || strings.TrimSpace(text) == "" {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

// fallbackMentionsSurfaceTool reports whether a guidance line names at least one
// tool of the shipped coder surface. The mention is a plain substring match, so
// the line reads naturally ("... one coder_bash call ...") and still proves the
// guidance points at a tool the coder has.
func fallbackMentionsSurfaceTool(text string) bool {
	for _, tool := range CoderSurfaceTools() {
		if strings.Contains(text, tool) {
			return true
		}
	}
	return false
}

// sortedFallbackIDs returns the guidance keys, sorted.
func sortedFallbackIDs(guidance map[string]string) []string {
	ids := make([]string, 0, len(guidance))
	for id := range guidance {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ParityFallbackJSON renders the fallback table as stable JSON (sorted by gap
// id) for the same run evidence OpenCodeParityJSON feeds. It is a separate
// document on purpose: the parity evidence of an already-shipped run keeps its
// schema (OpenCodeParityPlanVersion), while the mitigation table can grow a gap
// without invalidating recorded evidence.
func ParityFallbackJSON() (string, error) {
	type fallbackEntry struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Phase    string `json:"phase"`
		Status   string `json:"status"`
		Fallback string `json:"fallback"`
	}
	entries := make([]fallbackEntry, 0, len(parityFallbackGuidance))
	for _, gap := range OpenCodeParityGaps() {
		text, ok := ParityFallbackFor(gap.ID)
		if !ok {
			text = ""
		}
		entries = append(entries, fallbackEntry{
			ID:       gap.ID,
			Title:    gap.Title,
			Phase:    gap.Phase,
			Status:   gap.Status,
			Fallback: text,
		})
	}
	encoded, err := json.Marshal(struct {
		Version   int             `json:"version"`
		Uncovered []string        `json:"uncovered"`
		Fallbacks []fallbackEntry `json:"fallbacks"`
	}{
		Version:   ParityFallbackVersion,
		Uncovered: UncoveredParityGapIDs(),
		Fallbacks: entries,
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// RenderParityFallbackMarkdown renders the fallback table for the private
// runbook: one row per gap, phase and status included so an open row reads
// together with what the agent should do instead. A closed gap has no fallback
// (the capability is shipped), so it renders as "-" instead of a stale
// workaround.
func RenderParityFallbackMarkdown() string {
	var builder strings.Builder
	builder.WriteString("# Native coder / opencode parity fallbacks (ci#394)\n\n")
	builder.WriteString("What the native coder should do instead while a parity gap is still open. ")
	builder.WriteString("Every line names at least one tool the coder ships; the registry in ")
	builder.WriteString("`backend/nativecoder/opencodeparity.go` is the gate, this table is the mitigation.\n\n")
	builder.WriteString("| Gap | Phase | Status | Fallback |\n")
	builder.WriteString("| --- | --- | --- | --- |\n")
	for _, gap := range OpenCodeParityGaps() {
		text := "-"
		if gap.Status != ParityStatusClosed {
			text = "_no fallback recorded_"
			if recorded := strings.TrimSpace(parityFallbackGuidance[gap.ID]); recorded != "" {
				text = recorded
			}
		}
		builder.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n",
			gap.ID, gap.Phase, gap.Status, strings.ReplaceAll(text, "|", "\\|")))
	}
	return builder.String()
}
