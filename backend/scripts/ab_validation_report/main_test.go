package main

import (
	"strings"
	"testing"
)

func TestFlagsFirstAcceptsDocumentedInvocation(t *testing.T) {
	got := flagsFirst([]string{"runs.jsonl", "--format=json"})
	want := []string{"--format=json", "runs.jsonl"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}

	got = flagsFirst([]string{"runs.jsonl", "--format", "text", "--umbrella"})
	want = []string{"--format", "text", "--umbrella", "runs.jsonl"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}

	// A lone "-" means stdin and must stay positional.
	got = flagsFirst([]string{"-", "--umbrella"})
	if strings.Join(got, " ") != "--umbrella -" {
		t.Fatalf("got %v, want --umbrella -", got)
	}
}

func TestReadCasesParsesJSONLAndSkipsComments(t *testing.T) {
	input := `# paired runs recorded from ci#812
{"stage":"plan","legacy_bot":"issue-plan-bot","native_runtime":"msgmate-coder-plan-bot","ticket":"ci#812","legacy":{"task_completed":true},"native":{"task_completed":true}}

{"stage":"lead","legacy_bot":"open-chat-development-lead","native_runtime":"msgmate-coder-development-lead","ticket":"ci#813","legacy":{},"native":{}}
`
	cases, err := readCases(strings.NewReader(input))
	if err != nil {
		t.Fatalf("read cases: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	if cases[0].Stage != "plan" || !cases[0].Native["task_completed"] {
		t.Fatalf("unexpected first case %+v", cases[0])
	}
	if cases[1].Ticket != "ci#813" {
		t.Fatalf("unexpected second case %+v", cases[1])
	}
}

func TestReadCasesRejectsMalformedLine(t *testing.T) {
	if _, err := readCases(strings.NewReader("{\"stage\":\n")); err == nil {
		t.Fatal("expected an error for a malformed line")
	}
}
