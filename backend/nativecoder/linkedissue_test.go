package nativecoder

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestLinkedIssueNumbers(t *testing.T) {
	body := strings.Join([]string{
		"## Summary",
		"",
		"This PR fixes the flush ordering. It mentions ci#1 in prose but that is not a closing link.",
		"",
		"Closes msgmate-io/open-chat-go-ci#794",
		"Fixes ci#794",
		"Resolves: open-chat-go-ci#795",
		"closes MSGMATE-IO/OPEN-CHAT-GO-CI#796",
		"Closes ci#0",
		// Trailing horizontal whitespace is tolerated: the PR CI grep and this
		// parser must agree, and a body assembled from a template can easily
		// carry a stray space or tab.
		"Closes ci#797   ",
		"\tFixes ci#798\t",
	}, "\n")

	got := LinkedIssueNumbers(body)
	want := []int{794, 795, 796, 797, 798}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LinkedIssueNumbers() = %v, want %v", got, want)
	}
}

func TestLinkedIssueNumbersRequiresStandaloneLine(t *testing.T) {
	for _, body := range []string{
		"This closes ci#123.",
		"- Closes ci#123",
		"Closes #123",
		"Closes msgmate-io/open-chat-go#123",
		// Prose that merely starts with a closing keyword must not become a
		// closing link. The PR CI grep used to accept these (its pattern was
		// not anchored at the end of the line) while this parser rejected them,
		// so the gate and the post-merge auto-closer disagreed: e.g. merged PRs
		// #943 ("Closes msgmate-io/open-chat-go-ci#739.") and #836
		// ("Closes ci#740 (after a green re-run ...)") drifted this way.
		"Closes ci#824 and also ci#999",
		"Closes msgmate-io/open-chat-go-ci#739.",
		"Closes ci#740 (after a green re-run confirms the provider cause).",
		"Fix ci#824: crash on empty config",
		"",
	} {
		if got := LinkedIssueNumbers(body); len(got) != 0 {
			t.Errorf("LinkedIssueNumbers(%q) = %v, want none", body, got)
		}
	}
}

// ci#824: the PR CI gate and the post-merge auto-closer inline their own copy
// of the closing-line pattern in the workflow (they cannot call Go code), and
// that copy had lost the end-of-line anchor, so it matched prose the Go parser
// ignored. This test reads the workflow, requires both copies to be present and
// pins them to the exact same accept/reject decisions as linkedIssueLine.
func TestWorkflowLinkedIssueGrepStaysAnchored(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/run-pr-ci-tests-and-deployments.yaml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	// The inlined copy is a `grep -oiE '<pattern>'` invocation; the closing-line
	// pattern is the one carrying the closing-keyword alternation.
	grepPattern := regexp.MustCompile(`grep -oiE '([^']*close\[sd\][^']*)'`)
	var inlined []*regexp.Regexp
	for _, match := range grepPattern.FindAllStringSubmatch(string(raw), -1) {
		re, err := regexp.Compile("(?i)" + match[1])
		if err != nil {
			t.Fatalf("workflow pattern %q does not compile: %v", match[1], err)
		}
		inlined = append(inlined, re)
	}
	// The merge gate and the post-merge auto-close job each carry a copy; a
	// missing copy means one of the two stopped recognising closing links.
	if len(inlined) != 2 {
		t.Fatalf("found %d inlined closing-line greps, want 2 (gate + auto-close)", len(inlined))
	}

	corpus := []string{
		// Standalone closing lines: accepted by both.
		"Closes msgmate-io/open-chat-go-ci#794",
		"Fixes ci#795",
		"Resolves: open-chat-go-ci#796",
		"closes MSGMATE-IO/OPEN-CHAT-GO-CI#796",
		"Closes ci#797   ",
		"\tFixes ci#798\t",
		// Prose, lists and lookalikes: rejected by both.
		"This closes ci#123.",
		"- Closes ci#123",
		"Closes #123",
		"Closes msgmate-io/open-chat-go#123",
		"Closes ci#824 and also ci#999",
		"Closes msgmate-io/open-chat-go-ci#739.",
		"Closes ci#740 (after a green re-run confirms the provider cause).",
		"Fix ci#824: crash on empty config",
		"Closes ci#0",
		"",
	}
	for _, line := range corpus {
		want := linkedIssueLine.MatchString(strings.TrimSuffix(line, "\r"))
		for i, re := range inlined {
			if got := re.MatchString(line); got != want {
				t.Errorf("workflow copy %d on %q = %v, linkedIssueLine = %v", i, line, got, want)
			}
		}
	}
}

// The workflow extracts the number with `sed -E 's/.*#([0-9]+).*/\1/'` after the
// grep, so a match must end at the number for the two stages to agree.
func TestWorkflowLinkedIssueSedExtractsNumber(t *testing.T) {
	line := "Closes msgmate-io/open-chat-go-ci#813"
	match := regexp.MustCompile(`(?i)^[ \t]*(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?[ \t]+(?:msgmate-io/open-chat-go-ci#|open-chat-go-ci#|ci#)([0-9]+)[ \t]*$`).FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("pattern did not match %q", line)
	}
	trimmed := regexp.MustCompile(`.*#([0-9]+).*`).ReplaceAllString(match[0], "$1")
	if trimmed != "813" {
		t.Fatalf("sed-equivalent extraction = %q, want %q", trimmed, "813")
	}
}

func TestLinkedIssueLine(t *testing.T) {
	if got, want := LinkedIssueLine(813), "Closes msgmate-io/open-chat-go-ci#813"; got != want {
		t.Fatalf("LinkedIssueLine() = %q, want %q", got, want)
	}
}

func TestMergeLinkedIssues(t *testing.T) {
	got := mergeLinkedIssues(
		[]int{2, 1},
		[]PullRequest{{LinkedIssues: []int{3, 1}}, {LinkedIssues: []int{2}}},
	)
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeLinkedIssues() = %v, want %v", got, want)
	}
}
