package nativecoder

import (
	"encoding/json"
	"testing"
)

func TestDetectGitHubAwaitShapes(t *testing.T) {
	cases := []struct {
		source string
		kind   string
		target string
		watch  bool
		poll   bool
		args   []string
	}{
		{"gh pr checks 123 --watch", GitHubAwaitPRChecks, GitHubAwaitToolName, true, false, []string{"123"}},
		{"gh pr checks 123", GitHubAwaitPRChecks, GitHubAwaitToolPRChecks, false, false, []string{"123"}},
		{"gh run watch 456 --exit-status", GitHubAwaitRunWatch, GitHubAwaitToolName, true, false, []string{"456"}},
		{"gh run list --watch main", GitHubAwaitRunWatch, GitHubAwaitToolName, true, false, []string{"main"}},
		{"until gh run view 3; do sleep 5; done", GitHubAwaitPollLoop, GitHubAwaitToolName, false, true, nil},
		{"while ! gh pr checks 7; do sleep 3; done", GitHubAwaitPollLoop, GitHubAwaitToolName, false, true, nil},
		{"gh pr view 123", GitHubAwaitUnknown, "", false, false, nil},
		{"gh run view 456", GitHubAwaitUnknown, "", false, false, nil},
		{"gh run list", GitHubAwaitUnknown, "", false, false, nil},
		{"until curl -s localhost:8080/health; do sleep 1; done", GitHubAwaitUnknown, "", false, false, nil},
		{"go test ./backend/nativecoder/...", GitHubAwaitUnknown, "", false, false, nil},
		{"", GitHubAwaitUnknown, "", false, false, nil},
	}
	for _, c := range cases {
		shape := ClassifyGitHubAwaitSource(c.source)
		if shape.Kind != c.kind || shape.Target != c.target || shape.Watch != c.watch || shape.Poll != c.poll {
			t.Errorf("%q: got kind=%q target=%q watch=%v poll=%v", c.source, shape.Kind, shape.Target, shape.Watch, shape.Poll)
		}
		if len(shape.Args) != len(c.args) {
			t.Errorf("%q: args=%v want %v", c.source, shape.Args, c.args)
		}
	}
}

func TestMeasureGitHubAwaitCoverage(t *testing.T) {
	m := MeasureGitHubAwaitCoverage([]string{
		"gh pr checks 1 --watch",
		"gh run watch 2",
		"until gh run view 3; do sleep 5; done",
		"gh pr view 4",
		"",
		"# comment",
	})
	if m.Samples != 4 || m.Await != 3 || m.Covered != 3 || m.PollLoops != 1 {
		t.Fatalf("metrics = %+v", m)
	}
	if m.Coverage != 1.0 || m.Share != 0.75 {
		t.Fatalf("coverage=%v share=%v want 1.0/0.75", m.Coverage, m.Share)
	}
}

func TestGitHubAwaitDeterministicJSON(t *testing.T) {
	a := DetectGitHubAwait(githubAwaitWords("gh run watch 9"))
	b := DetectGitHubAwait(githubAwaitWords("gh run watch 9"))
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ja) != string(jb) {
		t.Fatalf("nondeterministic: %s vs %s", ja, jb)
	}
}
