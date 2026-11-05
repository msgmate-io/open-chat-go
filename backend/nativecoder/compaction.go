package nativecoder

// coder_compact core-side planner - ci#394 gap 7 (context compaction).
//
// The native coder agent runs a long interaction in one context window, and a
// long session can exhaust the provider window (context_window_exceeded) before
// the task ends. opencode handles this with summarization/compaction. The rest
// of the pipeline already supports it: the reader injects a persisted
// `context_summary` as a stable, sanitized leading block (ci#793). What was
// missing is the agent-side step that turns a long transcript into the compact
// summary to persist.
//
// This file ships that step as a pure, deterministic surface the integration
// can expose as `coder_compact`: combine the already-persisted findings with
// the notes collected this session, drop exact duplicates, and bound the result
// to a byte cap so an overlong session shrinks instead of mirroring the whole
// transcript. It deliberately does not re-implement the sanitization of the
// injected summary (ci#793) - that is the reader's job on every inject - so the
// two concerns stay on the side they belong.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultCoderSummaryBytes caps a compacted summary when the caller does not
// request a smaller one.
const DefaultCoderSummaryBytes = 8 << 10 // 8 KiB

// CoderCompactionNote is one finding collected in the session.
type CoderCompactionNote struct {
	// Text is the finding, whitespace-trimmed.
	Text string
}

// CoderCompactionRequest is the tool-dispatch form of `coder_compact`.
type CoderCompactionRequest struct {
	// ChatID identifies the chat whose session is compacted.
	ChatID string
	// Existing is the currently persisted summary (empty when none).
	Existing string
	// Notes are the findings not yet persisted.
	Notes []CoderCompactionNote
	// MaxBytes bounds the produced summary; 0 uses DefaultCoderSummaryBytes.
	MaxBytes int
}

// CoderCompactionMeta reports what a compaction pass did.
type CoderCompactionMeta struct {
	LinesAdded    int  `json:"lines_added"`
	LinesKept     int  `json:"lines_kept"`
	DupesDropped  int  `json:"dupes_dropped"`
	Truncated     int  `json:"truncated"`
	Bytes         int  `json:"bytes"`
	MaxBytes      int  `json:"max_bytes"`
	AlreadyStable bool `json:"already_stable"`
	Persist       bool `json:"persist"`
}

// CoderCompactionResult is the tool-dispatch result of a compaction.
type CoderCompactionResult struct {
	// Summary is the bounded, de-duplicated summary to persist.
	Summary string
	// Rendered is the deterministic markdown a chat panel shows.
	Rendered string
	// JSON is the stable JSON form of the meta for structured tool metadata.
	JSON string
	// Meta reports what was added, dropped and truncated.
	Meta CoderCompactionMeta
}

// CompileCoderSummary builds the compacted summary for a chat (ci#394 gap 7).
// It keeps already-persisted findings, appends the session notes (dropping
// exact duplicates), and bounds the combined result to MaxBytes, trimming the
// oldest finding lines first so the newest notes survive an overlong session.
func CompileCoderSummary(existing string, notes []string, maxBytes int) (CoderCompactionResult, error) {
	meta := CoderCompactionMeta{MaxBytes: maxBytes}
	if meta.MaxBytes <= 0 {
		meta.MaxBytes = DefaultCoderSummaryBytes
	}
	meta.AlreadyStable = strings.TrimSpace(existing) != ""

	var lines []string
	seen := map[string]bool{}
	add := func(l string) {
		l = strings.TrimSpace(l)
		if l == "" {
			return
		}
		if seen[l] {
			meta.DupesDropped++
			return
		}
		seen[l] = true
		lines = append(lines, l)
	}

	for _, l := range splitSummaryLines(existing) {
		add(l)
	}
	for _, n := range notes {
		text := strings.TrimSpace(n)
		if text == "" {
			continue
		}
		before := len(lines)
		add(text)
		if len(lines) > before {
			meta.LinesAdded++
		}
	}
	meta.LinesKept = len(lines)

	joined := strings.Join(lines, "\n")
	for len(joined) > meta.MaxBytes {
		nl := strings.IndexByte(joined, '\n')
		if nl < 0 {
			meta.Truncated += meta.LinesKept
			joined = ""
			meta.LinesKept = 0
			break
		}
		joined = strings.TrimLeft(joined[nl+1:], "\n")
		meta.Truncated++
		meta.LinesKept--
	}

	summary := "Compacted session summary (ci#394):\n"
	if joined != "" {
		summary += joined + "\n"
	}
	meta.Bytes = len(summary)
	meta.Persist = meta.LinesAdded > 0 || (!meta.AlreadyStable && meta.LinesKept > 0)

	res := CoderCompactionResult{
		Summary:  summary,
		Rendered: summary,
		Meta:     meta,
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return CoderCompactionResult{}, fmt.Errorf("encode compaction meta: %w", err)
	}
	res.JSON = string(encoded)
	return res, nil
}

// splitSummaryLines returns the non-empty, trimmed lines of a blob.
func splitSummaryLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
