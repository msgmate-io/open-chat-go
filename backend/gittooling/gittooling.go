// Package gittooling hosts the shared result-shaping helpers of the native
// coder git tools (coder_git and its read-only sibling coder_git_read).
//
// The handlers themselves live in the coder integrations
// (msgmate_coder/community_coder); the shapes they return are described, bounded
// and pinned here so every profile emits the same, parseable envelope (ci#1030).
//
// ci#1030: read-only actions (diff, show, log, blame) return a self-describing
// envelope that always carries an explicit `truncated` flag and, whenever
// something was cut, an actionable `hint`. A diff is only ever cut at a
// `diff --git` / `@@` boundary, so a model never receives half a hunk.
//
// The shaping is the handlers' job. The chat context budget deliberately does
// not re-bound these envelopes: it keeps the plain, pre-existing cut for every
// tool so a single promise covers the whole results path.
package gittooling

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Read-only git actions.
const (
	GitActionStatus   = "status"
	GitActionLog      = "log"
	GitActionDiff     = "diff"
	GitActionShow     = "show"
	GitActionBlame    = "blame"
	GitActionRemote   = "remote"
	GitActionLsFiles  = "ls_files"
	GitActionRevParse = "rev_parse"
	GitActionCatFile  = "cat_file"
)

// Mutating git actions (confirmable, never bounded by this package).
const (
	GitActionAdd      = "add"
	GitActionCommit   = "commit"
	GitActionBranch   = "branch"
	GitActionCheckout = "checkout"
	GitActionPush     = "push"
	GitActionReset    = "reset"
)

// Default bounds for the read-only results.
const (
	DefaultMaxDiffLines  = 400
	DefaultMaxDiffBytes  = 20000
	DefaultMaxCommits    = 20
	TruncationTailPrefix = "\u2026 [diff truncated"
)

// GitReadOnlyActions lists the inspection actions.
func GitReadOnlyActions() []string {
	return []string{GitActionStatus, GitActionLog, GitActionDiff, GitActionShow, GitActionBlame,
		GitActionRemote, GitActionLsFiles, GitActionRevParse, GitActionCatFile}
}

// GitMutatingActions lists the confirmable actions.
func GitMutatingActions() []string {
	return []string{GitActionAdd, GitActionCommit, GitActionBranch, GitActionCheckout, GitActionPush, GitActionReset}
}

// GitFileChange is one entry of the `files` array of a diff/show result.
type GitFileChange struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Hunks     int    `json:"hunks"`
}

// GitLogEntry is one entry of the `entries` array of a log result.
type GitLogEntry struct {
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Subject  string `json:"subject"`
}

// GitBlameEntry is one row of the `blame` array (one source line).
type GitBlameEntry struct {
	Line     int    `json:"line"`
	ShortSHA string `json:"short_sha"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Content  string `json:"content"`
}

// GitBlameRange groups consecutive lines last touched by the same commit, so a
// model can answer "who changed this block" without re-reading every row.
type GitBlameRange struct {
	Start    int    `json:"start"`
	End      int    `json:"end"`
	ShortSHA string `json:"short_sha"`
	Author   string `json:"author"`
	Date     string `json:"date"`
}

// GitResult is the bounded envelope shared by the read-only actions. Truncated
// is always emitted (never omitted) so a consumer can tell a complete result
// from a cut one; Hint is set whenever anything was dropped or the request was
// ambiguous.
type GitResult struct {
	Action    string          `json:"action"`
	Path      string          `json:"path,omitempty"`
	Commit    string          `json:"commit,omitempty"`
	Branch    string          `json:"branch,omitempty"`
	Status    string          `json:"status,omitempty"`
	SHA       string          `json:"sha,omitempty"`
	Author    string          `json:"author,omitempty"`
	Date      string          `json:"date,omitempty"`
	Subject   string          `json:"subject,omitempty"`
	Stat      string          `json:"stat,omitempty"`
	Files     []GitFileChange `json:"files,omitempty"`
	Hunks     int             `json:"hunks,omitempty"`
	Diff      string          `json:"diff,omitempty"`
	Entries   []GitLogEntry   `json:"entries,omitempty"`
	Blame     []GitBlameEntry `json:"blame,omitempty"`
	Ranges    []GitBlameRange `json:"ranges,omitempty"`
	Truncated bool            `json:"truncated"`
	Hint      string          `json:"hint,omitempty"`
}

// ValidateReadInput returns an error naming the missing parameter for the two
// actions that are ambiguous without one (show needs a commit, blame a path).
func ValidateReadInput(action, commit, path string) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return fmt.Errorf("action is required (one of: %s)", strings.Join(GitReadOnlyActions(), ", "))
	}
	known := false
	for _, candidate := range GitReadOnlyActions() {
		if action == candidate {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("action must be one of: %s (use coder_git for the mutating actions %s)",
			strings.Join(GitReadOnlyActions(), ", "), strings.Join(GitMutatingActions(), ", "))
	}
	switch action {
	case GitActionShow:
		if strings.TrimSpace(commit) == "" {
			return fmt.Errorf("show requires the `commit` parameter (a sha, branch, tag or HEAD~n)")
		}
	case GitActionBlame:
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("blame requires the `path` parameter (one file to attribute line by line)")
		}
	}
	return nil
}

type diffHunk struct{ lines []string }

type diffSection struct {
	header []string
	hunks  []diffHunk
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func linesSize(lines []string) (int, int) {
	size := 0
	for _, line := range lines {
		size += len(line) + 1
	}
	return len(lines), size
}

// parseDiffSections splits a unified diff at `diff --git` boundaries and, inside
// a section, at `@@` boundaries. Every resulting hunk is complete.
func parseDiffSections(diff string) []diffSection {
	var sections []diffSection
	var current *diffSection
	var hunk *diffHunk
	flushHunk := func() {
		if current != nil && hunk != nil {
			current.hunks = append(current.hunks, *hunk)
			hunk = nil
		}
	}
	flushSection := func() {
		flushHunk()
		if current != nil {
			sections = append(sections, *current)
			current = nil
		}
	}
	for _, line := range splitLines(diff) {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushSection()
			current = &diffSection{header: []string{line}}
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			if current == nil {
				current = &diffSection{}
			}
			hunk = &diffHunk{lines: []string{line}}
		default:
			switch {
			case hunk != nil:
				hunk.lines = append(hunk.lines, line)
			case current != nil:
				current.header = append(current.header, line)
			default:
				current = &diffSection{header: []string{line}}
			}
		}
	}
	flushSection()
	return sections
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sectionPath(header []string) (string, string) {
	status := "modified"
	for _, line := range header {
		switch {
		case strings.HasPrefix(line, "new file mode"):
			status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			status = "deleted"
		case strings.HasPrefix(line, "rename from"):
			status = "renamed"
		}
	}
	for _, line := range header {
		if strings.HasPrefix(line, "diff --git ") {
			rest := strings.TrimPrefix(line, "diff --git ")
			if i := strings.LastIndex(rest, " b/"); i >= 0 {
				return rest[i+3:], status
			}
			return rest, status
		}
	}
	return "", status
}

// SummarizeDiff returns the complete `files` array and the total hunk count of a
// diff, computed from the diff text itself so no extra git call is needed.
func SummarizeDiff(diff string) ([]GitFileChange, int) {
	sections := parseDiffSections(diff)
	files := make([]GitFileChange, 0, len(sections))
	totalHunks := 0
	for _, section := range sections {
		path, status := sectionPath(section.header)
		change := GitFileChange{Path: path, Status: status, Hunks: len(section.hunks)}
		for _, hunk := range section.hunks {
			for i, line := range hunk.lines {
				if i == 0 || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
					continue
				}
				if strings.HasPrefix(line, "+") {
					change.Additions++
				}
				if strings.HasPrefix(line, "-") {
					change.Deletions++
				}
			}
		}
		totalHunks += len(section.hunks)
		files = append(files, change)
	}
	return files, totalHunks
}

// DiffStat renders a compact, always non-empty `git diff --stat`-style summary.
func DiffStat(files []GitFileChange) string {
	insertions, deletions := 0, 0
	var b strings.Builder
	for _, file := range files {
		insertions += file.Additions
		deletions += file.Deletions
		bar := strings.Repeat("+", file.Additions) + strings.Repeat("-", file.Deletions)
		if len(bar) > 40 {
			bar = bar[:40]
		}
		fmt.Fprintf(&b, " %s | %d %s\n", file.Path, file.Additions+file.Deletions, bar)
	}
	fmt.Fprintf(&b, " %d %s changed, %d insertion%s(+), %d deletion%s(-)",
		len(files), plural(len(files), "file", "files"),
		insertions, pluralSuffix(insertions), deletions, pluralSuffix(deletions))
	return b.String()
}

// BoundDiff cuts a unified diff so that it never exceeds maxLines/maxBytes while
// only ever dropping whole hunks. The `diff --git` header of every file is kept,
// and a machine-readable tail states what was dropped.
func BoundDiff(diff string, maxLines, maxBytes int) (string, int, int, bool) {
	if maxLines <= 0 {
		maxLines = DefaultMaxDiffLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDiffBytes
	}
	sections := parseDiffSections(diff)
	if len(sections) == 0 {
		return diff, 0, 0, false
	}
	if sizeLines, sizeBytes := linesSize(splitLines(diff)); sizeLines <= maxLines && sizeBytes <= maxBytes {
		return diff, 0, 0, false
	}

	var b strings.Builder
	usedLines, usedBytes := 0, 0
	omittedFiles, omittedHunks := 0, 0
	// Reserve one line and a small byte budget for the truncation tail.
	fits := func(lines, bytes int) bool {
		return usedLines+lines <= maxLines-1 && usedBytes+bytes <= maxBytes-160
	}
	for _, section := range sections {
		headerLines, headerBytes := linesSize(section.header)
		if !fits(headerLines, headerBytes) {
			omittedFiles++
			omittedHunks += len(section.hunks)
			continue
		}
		b.WriteString(strings.Join(section.header, "\n"))
		b.WriteString("\n")
		usedLines += headerLines
		usedBytes += headerBytes
		fileOmitted := false
		for _, hunk := range section.hunks {
			hunkLines, hunkBytes := linesSize(hunk.lines)
			if !fits(hunkLines, hunkBytes) {
				fileOmitted = true
				omittedHunks++
				continue
			}
			b.WriteString(strings.Join(hunk.lines, "\n"))
			b.WriteString("\n")
			usedLines += hunkLines
			usedBytes += hunkBytes
		}
		if fileOmitted {
			omittedFiles++
		}
	}
	if omittedFiles == 0 && omittedHunks == 0 {
		return diff, 0, 0, false
	}
	fmt.Fprintf(&b, "%s: %d of %d files, %d hunks omitted. Re-run with path=<file> or a single commit; `stat` and `files` above are complete.]",
		TruncationTailPrefix, omittedFiles, len(sections), omittedHunks)
	return b.String(), omittedFiles, omittedHunks, true
}

// ShapeDiff builds the diff/show envelope: complete `files`/`stat`/`hunks`, a
// bounded `diff`, and an explicit truncated flag plus hint when it was cut.
func ShapeDiff(action, diff, path, commit string, maxLines, maxBytes int) GitResult {
	files, hunks := SummarizeDiff(diff)
	bounded, omittedFiles, omittedHunks, truncated := BoundDiff(diff, maxLines, maxBytes)
	result := GitResult{
		Action: action, Path: path, Commit: commit,
		Stat: DiffStat(files), Files: files, Hunks: hunks,
		Diff: bounded, Truncated: truncated,
	}
	if truncated {
		result.Hint = fmt.Sprintf("diff truncated (%d of %d files, %d hunks omitted): re-run with path=<file> to see one file in full, or with a single commit via show; `stat` and `files` are complete.",
			omittedFiles, len(files), omittedHunks)
	}
	return result
}

// ParseLogRecords parses git log output formatted with %H\x1f%h\x1f%an\x1f%ad\x1f%s.
func ParseLogRecords(output string) []GitLogEntry {
	entries := []GitLogEntry{}
	for _, line := range splitLines(output) {
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		entries = append(entries, GitLogEntry{SHA: parts[0], ShortSHA: parts[1], Author: parts[2], Date: parts[3], Subject: parts[4]})
	}
	return entries
}

// ShapeLog builds the log envelope: bounded entries, newest first, no diffs.
func ShapeLog(entries []GitLogEntry, maxCommits int) GitResult {
	if maxCommits <= 0 {
		maxCommits = DefaultMaxCommits
	}
	result := GitResult{Action: GitActionLog, Truncated: false}
	if len(entries) > maxCommits {
		entries = entries[:maxCommits]
		result.Truncated = true
		result.Hint = fmt.Sprintf("log limited to the %d newest commits: raise max_commits or pass a ref/path to narrow the history; `show` a sha for its message and patch.", maxCommits)
	}
	result.Entries = entries
	return result
}

// BlameRanges groups consecutive blame rows that share the same commit.
func BlameRanges(entries []GitBlameEntry) []GitBlameRange {
	ranges := []GitBlameRange{}
	for _, entry := range entries {
		last := len(ranges) - 1
		if last >= 0 && ranges[last].ShortSHA == entry.ShortSHA {
			ranges[last].End = entry.Line
			continue
		}
		ranges = append(ranges, GitBlameRange{Start: entry.Line, End: entry.Line, ShortSHA: entry.ShortSHA, Author: entry.Author, Date: entry.Date})
	}
	return ranges
}

// ShapeBlame builds the blame envelope: per-line rows plus pre-grouped ranges.
func ShapeBlame(path string, entries []GitBlameEntry) GitResult {
	return GitResult{Action: GitActionBlame, Path: path, Blame: entries, Ranges: BlameRanges(entries), Truncated: false}
}

// Marshal renders a result as JSON, guaranteeing a parseable envelope.
func Marshal(result GitResult) (string, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// GitToolDescription documents coder_git for the model: one line per action with
// what it returns and when to prefer it over a sibling action.
const GitToolDescription = "Run a git action in the coding workspace. Every read-only result is bounded and carries an explicit `truncated` flag (false when complete) plus an actionable `hint` whenever something was cut.\n" +
	"- diff: worktree vs HEAD (`staged:true` reads the index instead), optionally narrowed with `path`. Returns `stat`, `files[]` (path, status, additions, deletions, hunks) and a bounded `diff`. Use it for uncommitted work, before committing.\n" +
	"- show: exactly one commit (`commit` required). Returns `sha`, `author`, `date`, `subject`, `stat`, `files[]` and that commit's bounded `diff`. Use it after `log`/`blame` gave you a sha.\n" +
	"- log: newest-first history listing bounded by `max_commits`. Returns `entries[]` (sha, short_sha, author, date, subject) and carries no diffs: follow up with `show`.\n" +
	"- blame: per-line last-touching commit for one file (`path` required), optional `line_start`/`line_end`. Returns `blame[]` rows plus pre-grouped `ranges[]` (start, end, short_sha, author, date), so you can name the commit that changed a block and then `show` it.\n" +
	"- status: returns the current branch (`branch`) and the short porcelain dirty state (`status`). Use it first when you do not know whether there is uncommitted work.\n" +
	"- remote: returns the configured remotes with their fetch/push URLs. Use it before push to see which remote and branch a PR would target.\n" +
	"- ls_files: returns the tracked files, optionally filtered by `pattern`. Use it to confirm a path exists before diff/blame instead of guessing.\n" +
	"- rev_parse: returns the sha that `ref` resolves to. Use it to pin a moving ref (branch, HEAD~2, tag) before show/diff. Does not show file contents.\n" +
	"- cat_file: returns whether `path` exists at `ref`, its object type, and with `content:true` the blob content. Use it to read a file as it was at a commit when the worktree copy has changed.\n" +
	"Mutating actions add, commit, branch, checkout, push and reset each require confirmation. Prefer `blame` over `show` when you need the commit that last touched a specific line, `show` over `diff` when you need one commit's message or patch (diff only covers uncommitted work), and `log` first when you still need a sha."

// GitActionDoc returns the description bullet of one action, or "" when the
// action is unknown.
func GitActionDoc(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	for _, line := range strings.Split(GitToolDescription, "\n") {
		if strings.HasPrefix(line, "- "+action+":") {
			return line
		}
	}
	return ""
}

// IsGitTool reports whether the tool name is one of the native git tools whose
// result is a GitResult envelope (and therefore shaped by this package).
func IsGitTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "coder_git", "coder_git_read":
		return true
	}
	return false
}
