package gittooling

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// diffFile builds one complete unified-diff section with `hunks` hunks and
// `bodyLines` body lines per hunk, so tests can control the exact size. The @@
// header counts are derived from the body, i.e. the diff is well formed.
func diffFile(name string, hunks, bodyLines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", name, name)
	b.WriteString("index 1111111..2222222 100644\n")
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
	for h := 0; h < hunks; h++ {
		body := make([]string, 0, bodyLines)
		added, removed, context := 0, 0, 0
		for i := 0; i < bodyLines; i++ {
			switch i % 3 {
			case 0:
				body = append(body, fmt.Sprintf("+added %d-%d", h, i))
				added++
			case 1:
				body = append(body, fmt.Sprintf("-removed %d-%d", h, i))
				removed++
			default:
				body = append(body, fmt.Sprintf(" context %d-%d", h, i))
				context++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@ func demo%d()\n",
			h*10+1, removed+context, h*10+1, added+context, h)
		for _, line := range body {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// assertWholeHunks fails when any hunk in the bounded diff is cut: the old/new
// line counts physically present must match the counts declared in its @@ header.
func assertWholeHunks(t *testing.T, diff string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	seenHunks := 0
	for i := 0; i < len(lines); i++ {
		match := hunkHeaderRe.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		seenHunks++
		wantOld := countOrOne(match[2], t)
		wantNew := countOrOne(match[4], t)
		oldConsumed, newConsumed := 0, 0
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "@@") || strings.HasPrefix(lines[j], "diff --git ") ||
				strings.HasPrefix(lines[j], TruncationTailPrefix) {
				break
			}
			switch {
			case strings.HasPrefix(lines[j], "\\"):
				// "\ No newline at end of file" belongs to neither side.
			case strings.HasPrefix(lines[j], "+"):
				newConsumed++
			case strings.HasPrefix(lines[j], "-"):
				oldConsumed++
			default:
				oldConsumed++
				newConsumed++
			}
		}
		if oldConsumed != wantOld || newConsumed != wantNew {
			t.Fatalf("hunk %q was cut mid-way: header declares %d old/%d new, body has %d old/%d new",
				lines[i], wantOld, wantNew, oldConsumed, newConsumed)
		}
	}
	if seenHunks == 0 {
		t.Fatalf("bounded diff contains no complete hunk:\n%s", diff)
	}
}

func countOrOne(raw string, t *testing.T) int {
	t.Helper()
	if raw == "" {
		return 1
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("bad hunk count %q: %v", raw, err)
	}
	return parsed
}

// TestBoundDiffKeepsWholeHunksAndFlagsTruncation is the ci#1030
// regression: a diff too large for the budget is cut at hunk boundaries only,
// reports how much was dropped, and is explicitly flagged as truncated.
func TestBoundDiffKeepsWholeHunksAndFlagsTruncation(t *testing.T) {
	diff := diffFile("alpha.go", 3, 6) + diffFile("beta.go", 3, 6) + diffFile("gamma.go", 3, 6)

	bounded, omittedFiles, omittedHunks, truncated := BoundDiff(diff, 20, 1<<20)
	if !truncated {
		t.Fatalf("expected a 54+ line diff to be truncated under a 20 line budget")
	}
	if omittedFiles == 0 && omittedHunks == 0 {
		t.Fatalf("expected omitted files/hunks to be reported, got %d/%d", omittedFiles, omittedHunks)
	}
	if lines := strings.Count(bounded, "\n"); lines > 20 {
		t.Fatalf("bounded diff has %d lines, budget is 20", lines)
	}
	if !strings.Contains(bounded, TruncationTailPrefix) {
		t.Fatalf("bounded diff is missing the truncation tail marker:\n%s", bounded)
	}
	assertWholeHunks(t, bounded)
}

// TestBoundDiffRespectsByteBudget covers the byte bound: a diff under the line
// cap but over the byte cap is still truncated, at a hunk boundary.
func TestBoundDiffRespectsByteBudget(t *testing.T) {
	diff := diffFile("big.go", 4, 8)
	bounded, _, _, truncated := BoundDiff(diff, 10000, 400)
	if !truncated {
		t.Fatalf("expected the 400 byte budget to truncate a %d byte diff", len(diff))
	}
	if len(bounded) > 400 {
		t.Fatalf("bounded diff is %d bytes, budget is 400", len(bounded))
	}
	assertWholeHunks(t, bounded)
}

// TestShapeDiffCompleteResultCarriesExplicitFalseFlag pins the shape of a diff
// that fits: complete `files`/`stat`/`hunks`, no hint, and `"truncated":false`
// which must be present in the JSON (never omitted).
func TestShapeDiffCompleteResultCarriesExplicitFalseFlag(t *testing.T) {
	diff := diffFile("alpha.go", 1, 3)
	result := ShapeDiff(GitActionDiff, diff, "", "", 400, 20000)

	if result.Truncated {
		t.Fatalf("small diff must not be flagged truncated")
	}
	if result.Hint != "" {
		t.Fatalf("complete result must carry no hint, got %q", result.Hint)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "alpha.go" {
		t.Fatalf("unexpected files summary: %+v", result.Files)
	}
	if result.Files[0].Additions != 1 || result.Files[0].Deletions != 1 {
		t.Fatalf("unexpected add/del counts: %+v", result.Files[0])
	}
	if result.Hunks != 1 {
		t.Fatalf("expected 1 hunk, got %d", result.Hunks)
	}
	if !strings.Contains(result.Stat, "1 file changed, 1 insertion(+), 1 deletion(-)") {
		t.Fatalf("unexpected stat: %q", result.Stat)
	}

	raw, err := Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(raw, `"truncated":false`) {
		t.Fatalf("envelope must always emit the truncated flag, got %s", raw)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
}

// TestShapeDiffTruncatedCarriesActionableHint pins that a cut diff tells the
// model exactly how to get an untruncated answer.
func TestShapeDiffTruncatedCarriesActionableHint(t *testing.T) {
	diff := diffFile("alpha.go", 4, 8) + diffFile("beta.go", 4, 8)
	result := ShapeDiff(GitActionShow, diff, "", "abc1234", 24, 1<<20)

	if !result.Truncated {
		t.Fatalf("expected truncation under a 24 line budget")
	}
	if result.Commit != "abc1234" {
		t.Fatalf("show envelope must echo the commit, got %q", result.Commit)
	}
	for _, want := range []string{"truncated", "path=<file>", "show"} {
		if !strings.Contains(result.Hint, want) {
			t.Fatalf("hint %q is missing %q", result.Hint, want)
		}
	}
	// The summary survives truncation: it is computed from the whole diff.
	if len(result.Files) != 2 {
		t.Fatalf("expected the complete files summary, got %+v", result.Files)
	}
}

// TestShapeLogBoundedAndFlagged covers the log envelope: newest-first entries,
// capped by max_commits, with an explicit truncated flag and hint.
func TestShapeLogBoundedAndFlagged(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&raw, "sha%03d\x1fsha%03d\x1fAuthor %d\x1f2026-01-01\x1fsubject %d\n", i, i, i, i)
	}
	entries := ParseLogRecords(raw.String())
	if len(entries) != 30 {
		t.Fatalf("expected 30 parsed log entries, got %d", len(entries))
	}

	bounded := ShapeLog(entries, 5)
	if !bounded.Truncated || bounded.Hint == "" {
		t.Fatalf("expected a truncated, hinted log result, got %+v", bounded)
	}
	if len(bounded.Entries) != 5 || bounded.Entries[0].Subject != "subject 0" {
		t.Fatalf("expected the 5 newest entries, got %+v", bounded.Entries)
	}
	if !strings.Contains(bounded.Hint, "show") {
		t.Fatalf("log hint should point at show for a single commit, got %q", bounded.Hint)
	}

	complete := ShapeLog(entries[:3], 20)
	if complete.Truncated || complete.Hint != "" || len(complete.Entries) != 3 {
		t.Fatalf("expected a complete, unhinted log result, got %+v", complete)
	}
}

// TestShapeBlameGroupsRanges pins the blame envelope: per-line rows plus
// pre-grouped consecutive ranges so the model can name a block's commit.
func TestShapeBlameGroupsRanges(t *testing.T) {
	entries := []GitBlameEntry{
		{Line: 1, ShortSHA: "aaaa", Author: "alice", Date: "2026-01-01", Content: "a"},
		{Line: 2, ShortSHA: "aaaa", Author: "alice", Date: "2026-01-01", Content: "b"},
		{Line: 3, ShortSHA: "bbbb", Author: "bob", Date: "2026-01-02", Content: "c"},
		{Line: 4, ShortSHA: "aaaa", Author: "alice", Date: "2026-01-01", Content: "d"},
	}
	result := ShapeBlame("backend/main.go", entries)

	if result.Truncated {
		t.Fatalf("a 4-line blame must not be flagged truncated")
	}
	if result.Path != "backend/main.go" {
		t.Fatalf("blame envelope must echo the path, got %q", result.Path)
	}
	if len(result.Ranges) != 3 {
		t.Fatalf("expected 3 grouped ranges, got %+v", result.Ranges)
	}
	if result.Ranges[0].Start != 1 || result.Ranges[0].End != 2 || result.Ranges[0].ShortSHA != "aaaa" {
		t.Fatalf("unexpected first range: %+v", result.Ranges[0])
	}
	if result.Ranges[1].Start != 3 || result.Ranges[1].End != 3 {
		t.Fatalf("unexpected second range: %+v", result.Ranges[1])
	}
}

// TestValidateReadInputNamesMissingParameter pins the actionable errors for the
// two actions that are ambiguous without a parameter.
func TestValidateReadInputNamesMissingParameter(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		commit  string
		path    string
		wantErr string
	}{
		{name: "show without commit", action: GitActionShow, wantErr: "show requires the `commit` parameter"},
		{name: "blame without path", action: GitActionBlame, wantErr: "blame requires the `path` parameter"},
		{name: "unknown action", action: "blame_everything", wantErr: "action must be one of:"},
		{name: "empty action", action: "", wantErr: "action is required"},
		{name: "show with commit", action: GitActionShow, commit: "HEAD~1"},
		{name: "blame with path", action: GitActionBlame, path: "backend/main.go"},
		{name: "diff needs nothing", action: GitActionDiff},
		{name: "log needs nothing", action: GitActionLog},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateReadInput(testCase.action, testCase.commit, testCase.path)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("expected error containing %q, got %v", testCase.wantErr, err)
			}
		})
	}
}

// TestGitToolDescriptionCoversEveryReadOnlyAction pins the description: every
// read-only action has a bullet that says what it returns, and the
// blame-vs-show-vs-diff guidance plus the truncation contract are present.
func TestGitToolDescriptionCoversEveryReadOnlyAction(t *testing.T) {
	for _, action := range GitReadOnlyActions() {
		doc := GitActionDoc(action)
		if doc == "" {
			t.Fatalf("action %q has no description bullet", action)
		}
		if !strings.Contains(doc, "Returns") && !strings.Contains(doc, "return") {
			t.Fatalf("action %q does not say what it returns: %q", action, doc)
		}
	}
	if GitActionDoc("nope") != "" {
		t.Fatalf("unknown action must have no doc")
	}

	pins := []string{
		"`truncated` flag",
		"worktree vs HEAD",
		"exactly one commit (`commit` required)",
		"`max_commits`",
		"`path` required",
		"Prefer `blame` over `show`",
		"`show` over `diff`",
		"`log` first",
	}
	for _, pin := range pins {
		if !strings.Contains(GitToolDescription, pin) {
			t.Fatalf("coder_git description is missing %q", pin)
		}
	}
}
