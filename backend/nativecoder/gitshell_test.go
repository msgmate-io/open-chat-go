package nativecoder

import (
	"strings"
	"testing"
)

func TestIsGitShellCommand(t *testing.T) {
	yes := []string{
		"git status",
		"git log --oneline -10",
		"git diff --stat",
		"git -C /repo status",
		"GIT_PAGER=cat git show HEAD",
		"git push origin feature/x",
	}
	no := []string{
		"gh pr view 1",
		"coder_git_status",
		"gofmt -l ./backend",
		"gitk",
		"",
	}
	for _, cmd := range yes {
		if !IsGitShellCommand(cmd) {
			t.Errorf("IsGitShellCommand(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range no {
		if IsGitShellCommand(cmd) {
			t.Errorf("IsGitShellCommand(%q) = true, want false", cmd)
		}
	}
}

func TestTranslateGitShellCommand(t *testing.T) {
	cases := []struct {
		cmd     string
		tool    string
		write   bool
		guarded bool
		key     string
		val     string
	}{
		{"git status", GitShellToolStatus, false, false, "", ""},
		{"git log --oneline -10", GitShellToolLog, false, false, "", ""},
		{"git diff backend/nativecoder", GitShellToolDiff, false, false, "path", "backend/nativecoder"},
		{"git show HEAD~2", GitShellToolShow, false, false, "rev", "HEAD~2"},
		{"git blame backend/api/handler.go", GitShellToolBlame, false, false, "path", "backend/api/handler.go"},
		{"git add -A", GitShellToolAdd, true, false, "", ""},
		{"git commit -m wip", GitShellToolCommit, true, false, "", ""},
		{"git push origin feat/x", GitShellToolPush, true, false, "branch", "feat/x"},
		{"git push -u origin main", GitShellToolPush, true, true, "branch", "main"},
		{"git checkout -b feat/y", GitShellToolCheckout, true, false, "", ""},
		{"git fetch --all", GitShellToolFetch, true, false, "", ""},
		{"git tag v1.2.3", GitShellToolTag, true, false, "", ""},
	}
	for _, tc := range cases {
		got, ok := TranslateGitShellCommand(tc.cmd)
		if !ok {
			t.Errorf("TranslateGitShellCommand(%q) not translated", tc.cmd)
			continue
		}
		if got.Tool != tc.tool {
			t.Errorf("%q -> %s, want %s", tc.cmd, got.Tool, tc.tool)
		}
		if got.Write != tc.write {
			t.Errorf("%q write = %t, want %t", tc.cmd, got.Write, tc.write)
		}
		if got.Guarded != tc.guarded {
			t.Errorf("%q guarded = %t, want %t", tc.cmd, got.Guarded, tc.guarded)
		}
		if tc.key != "" && got.Args[tc.key] != tc.val {
			t.Errorf("%q args[%s] = %q, want %q", tc.cmd, tc.key, got.Args[tc.key], tc.val)
		}
	}
}

func TestTranslateGitShellCommandUntranslated(t *testing.T) {
	if _, ok := TranslateGitShellCommand("git gc --aggressive"); ok {
		t.Fatal("git gc should stay untranslated (no typed equivalent yet)")
	}
}

func TestMeasureGitShellTranslation(t *testing.T) {
	report := MeasureGitShellTranslation([]string{
		"git status",
		"git push -u origin main",
		"git gc --aggressive",
		"git gc --aggressive",
		"go test ./...",
		"gh pr list",
	})
	if report.Total != 4 {
		t.Fatalf("total = %d, want 4", report.Total)
	}
	if report.Translated != 1 {
		t.Errorf("translated = %d, want 1", report.Translated)
	}
	if report.Guarded != 1 {
		t.Errorf("guarded = %d, want 1", report.Guarded)
	}
	if len(report.Untranslated) != 1 {
		t.Errorf("untranslated = %v, want one entry", report.Untranslated)
	}
	if got := report.Covered(); got != 0.5 {
		t.Errorf("covered = %f, want 0.5", got)
	}
	if empty := MeasureGitShellTranslation(nil); empty.Covered() != 1 {
		t.Errorf("empty sample should be fully covered, got %f", empty.Covered())
	}
}

func TestGitTypedFirstGuidance(t *testing.T) {
	guidance := GitTypedFirstGuidance()
	for _, tool := range []string{GitShellToolStatus, GitShellToolCommit, GitShellToolPush} {
		if !strings.Contains(guidance, tool) {
			t.Errorf("guidance does not mention %s", tool)
		}
	}
	if !strings.Contains(guidance, gitShellDefaultBranch) {
		t.Errorf("guidance does not mention the guarded default branch")
	}
}

func TestGitTypedFirstGuidanceNamesEveryTool(t *testing.T) {
	tools := GitTypedFirstTools()
	if len(tools) == 0 {
		t.Fatal("GitTypedFirstTools returned no tools")
	}
	// The guidance is generated from the same lists, so this is the regression
	// guard against a tool being added to one and forgotten in the other.
	for _, tool := range tools {
		if !strings.Contains(GitTypedFirstGuidance(), tool) {
			t.Errorf("guidance does not name typed git tool %s", tool)
		}
	}
}

func TestGitTypedFirstAppendIsIdempotent(t *testing.T) {
	base := "You are a coder."
	once := GitTypedFirstAppend(base)
	if got := GitTypedFirstAppend(once); got != once {
		t.Errorf("second append changed the prompt:\n%s", got)
	}
	if !strings.Contains(once, base) {
		t.Errorf("append dropped the original prompt")
	}
	if got := GitTypedFirstAppend("  \n"); !strings.Contains(got, gitTypedFirstMarker) {
		t.Errorf("append on a blank prompt lost the guidance: %q", got)
	}
	// A prompt that pre-baked the block must be returned byte-identical, the
	// prompt is paid for on every turn.
	preexisting := base + "\n\n" + GitTypedFirstGuidance()
	if got := GitTypedFirstAppend(preexisting); got != preexisting {
		t.Errorf("pre-baked prompt was mutated:\n%s", got)
	}
}

func TestMeasureGitTypedFirstPrompt(t *testing.T) {
	base := "You are a coder."

	applied := MeasureGitTypedFirstPrompt(GitTypedFirstAppend(base))
	if !applied.Ready || !applied.GuidanceApplied || len(applied.Missing) != 0 {
		t.Errorf("appended prompt not attested: %+v", applied)
	}

	untouched := MeasureGitTypedFirstPrompt(base)
	if untouched.Ready {
		t.Errorf("bare prompt must not be ready: %+v", untouched)
	}

	// Guidance present but a tool missing must not be ready: this is what makes
	// the criterion about the rendered prompt rather than a constant.
	partial := gitTypedFirstMarker
	for _, tool := range GitTypedFirstTools() {
		if tool != GitShellToolPush {
			partial += " " + tool
		}
	}
	gap := MeasureGitTypedFirstPrompt(partial)
	if gap.Ready || len(gap.Missing) != 1 || gap.Missing[0] != GitShellToolPush {
		t.Errorf("partial prompt attested: %+v", gap)
	}
}
