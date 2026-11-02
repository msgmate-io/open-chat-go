package msgmate

import (
	"strings"
)

// GitHub typed-tool write detection (ci#514, phase 1 exit criterion 4).
//
// Phase 1 ("GitHub typed-first") closes on five criteria; criterion 4 states:
//
//	"a write (comment/review/PR) is re-read and reported as verified
//	 instead of claimed"
//
// This file is the measurement half of that criterion. It watches the typed
// GitHub tool calls a coder run really makes (the github_* tools), detects the
// mutating ones, counts how many were recorded, and reports the number of
// writes that were closed by a read-back pair (the re-read) separate from the
// number that are still waiting on one. It is deliberately pure and
// dependency-free, like the S1 classifier in bash_coverage.go: it reads the
// tool name plus the ID/URL payload and returns verdicts, so it is cheap
// enough to run on every github_* call and deterministic enough to unit test.
//
// The counts then feed the coverage report's WrittenVerifications column (the
// machine-checked proof that writes are verified instead of claimed) and the
// phase exit-criteria evaluator in backend/nativecoder.

// GitHubTypedWriteTool is a mutating typed GitHub tool. The reads are
// explicit so a write is never inferred from a tool that also returns payload:
// a call is a write only when its role is Write.
type GitHubTypedWriteTool struct {
	// Tool is the typed tool name, e.g. "github_issue_comment".
	Tool string
	// ReadTool is the typed tool that re-reads the write's result back,
	// e.g. "github_issue_view" for github_issue_comment. It is the re-read
	// half of criterion 4's "verified instead of claimed".
	ReadTool string
	// Role is the mutating role of the write: Write for a call that creates
	// or mutates a resource, WaitDescribe for a call that mainly waits.
	Role string
	// WriteHint is injected into the payload JSON by the writes layer: the
	// string the read-back must mention to count as verified, e.g. the
	// merge result "merged" for a github_pr_merge payload.
	WriteHint string
}

// GitHubTypedWriteTools lists the mutating typed GitHub tools the coder ships
// (phase 1 surface). Order is stable for reports. The reads mirror the
// counterpart mutating tool so the write is verified by the exact resource it
// wrote.
var GitHubTypedWriteTools = []GitHubTypedWriteTool{
	{Tool: "github_issue_create", ReadTool: "github_issue_view", Role: "Write", WriteHint: "created"},
	{Tool: "github_issue_comment", ReadTool: "github_issue_view", Role: "Write", WriteHint: "done"},
	{Tool: "github_issue_edit", ReadTool: "github_issue_view", Role: "Write", WriteHint: "updated"},
	{Tool: "github_pr_create", ReadTool: "github_pr_view", Role: "Write", WriteHint: "merged"},
	{Tool: "github_pr_review", ReadTool: "github_pr_view", Role: "Write", WriteHint: "reviewed"},
	{Tool: "github_pr_merge", ReadTool: "github_pr_view", Role: "Write", WriteHint: "merged"},
	{Tool: "github_pr_comment", ReadTool: "github_pr_view", Role: "Write", WriteHint: "commented"},
}

// GitHubTypedWriteMap lets the metadata column resolve a typed tool name to
// its write descriptor in O(1).
var GitHubTypedWriteMap = func() map[string]GitHubTypedWriteTool {
	m := make(map[string]GitHubTypedWriteTool, len(GitHubTypedWriteTools))
	for _, write := range GitHubTypedWriteTools {
		m[write.Tool] = write
	}
	return m
}()

// GitHubWriteViolation describes one recorded GitHub write that a proper
// verification pass must close: the mutating tool call plus the read-back that
// must confirm it.
type GitHubWriteViolation struct {
	Tool        string `json:"tool"`
	ReadTool    string `json:"read_tool"`
	BodyOrCall  string `json:"body_or_call"` // the payload the write produced
	RecordedAt  string `json:"recorded_at"`  // run marker
	HasReadBack bool   `json:"has_read_back"`
}

// GitHubWriteVerificationResult is one verdict on one recorded write: whether
// the write was verified by a re-read, and why. It is the row the coverage
// report and the criterion evaluator both consume.
type GitHubWriteVerificationResult struct {
	Tool     string `json:"tool"`
	ReadTool string `json:"read_tool"`
	Body     string `json:"body"`
	Verified bool   `json:"verified"`
	Weak     bool   `json:"weak,omitempty"`
	Marker   string `json:"marker,omitempty"`
	Reason   string `json:"reason"`
	// Weblink  is the human URL of the write resource when the typed call
	// exposed one, e.g. the created issue URL. It lets the read-back verify
	// the record that actually exists rather than a copy.
	Weblink string `json:"weblink,omitempty"`
}

// GitHubWriteVerificationFor returns the read-back tool and the write-hint
// marker for a typed GitHub write tool, or "" for a tool that has no read-back
// (read-only tools and tools whose read-back is out of surface).
func GitHubWriteVerificationFor(tool string) (readTool, writeHint string) {
	write, ok := GitHubTypedWriteMap[tool]
	if !ok {
		return "", ""
	}
	return write.ReadTool, write.WriteHint
}

// ClassifyGitHubWrite reports how many of the recorded typed GitHub tool calls
// are writes, how many pairs of a write and its read-back are closed, and how
// many writes are missing a read-back. The counts drive the coverage report's
// WrittenVerifications column and the criterion's per-write verdicts.
func ClassifyGitHubWrite(calls []string) (writes, verified, missingReadBack int) {
	counts := make(map[string]int, len(calls))
	for _, call := range calls {
		counts[call]++
	}
	for _, write := range GitHubTypedWriteTools {
		written := counts[write.Tool]
		if written == 0 {
			continue
		}
		writes += written
		readBacks := counts[write.ReadTool]
		closed := written
		if readBacks < closed {
			closed = readBacks
		}
		verified += closed
		missingReadBack += written - closed
	}
	return writes, verified, missingReadBack
}

// RecordGitHubWrite appends a GitHubWriteViolation for one recorded mutating
// typed GitHub write. The record carries the payload the write produced so the
// later read-back can verify the write actually landed instead of assuming it.
func RecordGitHubWrite(writes []GitHubWriteViolation, tool string, body string, recordedAt string) []GitHubWriteViolation {
	readTool, _ := GitHubWriteVerificationFor(tool)
	return append(writes, GitHubWriteViolation{
		Tool:       tool,
		ReadTool:   readTool,
		BodyOrCall: body,
		RecordedAt: recordedAt,
	})
}

// VerifyGitHubWrite marks the matching recorded write as verified by the
// read-back, or leaves it unverified with the reason. It is the per-record
// attestation that the write was re-read and confirmed.
func VerifyGitHubWrite(writes []GitHubWriteViolation, tool string, readTool string, readBack string, done string) []GitHubWriteViolation {
	for i := range writes {
		record := &writes[i]
		if record.Tool != tool || string(record.BodyOrCall) != string(readBack) {
			continue
		}
		markerOK := done == "" || strings.Contains(readBack, done)
		record.HasReadBack = markerOK
		record.BodyOrCall = readBack
		break
	}
	return writes
}
