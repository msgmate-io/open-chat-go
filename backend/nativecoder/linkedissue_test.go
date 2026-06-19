package nativecoder

import (
	"reflect"
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
	}, "\n")

	got := LinkedIssueNumbers(body)
	want := []int{794, 795, 796}
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
		"",
	} {
		if got := LinkedIssueNumbers(body); len(got) != 0 {
			t.Errorf("LinkedIssueNumbers(%q) = %v, want none", body, got)
		}
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
