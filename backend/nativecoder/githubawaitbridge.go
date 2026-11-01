package nativecoder

// ci#514 "GitHub first": retire the coder_bash wait/poll escape hatch behind a
// typed github_await surface. Recognises `gh pr checks [--watch]`, `gh run
// watch`, `gh run list --watch` and hand-rolled poll loops, names the typed
// replacement for each, and measures coverage of observed wait/poll usage.
// Self-contained (stdlib only) so the contract can be reviewed and tested
// independently of the integration that executes the tools.

import (
	"math"
	"strings"
)

const (
	GitHubAwaitToolName     = "github_await"
	GitHubAwaitToolPRChecks = "github_pr_checks"
	GitHubAwaitToolRunWatch = "github_run_watch"
)

const (
	GitHubAwaitPRChecks = "pr_checks"
	GitHubAwaitRunWatch = "run_watch"
	GitHubAwaitPollLoop = "poll_loop"
	GitHubAwaitUnknown  = "unknown"
)

type GitHubAwaitShape struct {
	Kind   string   `json:"kind"`
	Target string   `json:"target"`
	Source string   `json:"source"`
	Args   []string `json:"args,omitempty"`
	Watch  bool     `json:"watch"`
	Poll   bool     `json:"poll"`
}

type GitHubAwaitMetrics struct {
	Samples   int     `json:"samples"`
	Await     int     `json:"await"`
	Covered   int     `json:"covered"`
	PollLoops int     `json:"poll_loops"`
	Coverage  float64 `json:"coverage"`
	Share     float64 `json:"share"`
}

// ClassifyGitHubAwaitSource classifies one shell command string.
func ClassifyGitHubAwaitSource(source string) GitHubAwaitShape {
	return DetectGitHubAwait(githubAwaitWords(source))
}

// DetectGitHubAwait recognises the wait/poll shapes of a shell command.
func DetectGitHubAwait(words []string) GitHubAwaitShape {
	source := strings.TrimSpace(strings.Join(words, " "))
	shape := GitHubAwaitShape{Source: source}
	if source == "" {
		shape.Kind = GitHubAwaitUnknown
		return shape
	}
	clean := make([]string, 0, len(words))
	for _, w := range words {
		if w = strings.Trim(strings.TrimSpace(w), "\"'();"); w != "" {
			clean = append(clean, w)
		}
	}
	if len(clean) < 2 {
		shape.Kind = GitHubAwaitUnknown
		return shape
	}
	if clean[0] == "until" || clean[0] == "while" {
		if githubAwaitLoopTarget(clean) == "" {
			shape.Kind = GitHubAwaitUnknown
			return shape
		}
		shape.Kind, shape.Target, shape.Poll = GitHubAwaitPollLoop, GitHubAwaitToolName, true
		return shape
	}
	if clean[0] != "gh" {
		shape.Kind = GitHubAwaitUnknown
		return shape
	}
	if len(clean) >= 3 && clean[1] == "pr" && clean[2] == "checks" {
		shape.Kind, shape.Target = GitHubAwaitPRChecks, GitHubAwaitToolPRChecks
		shape.Watch = containsGitHubAwaitFlag(clean, "--watch")
		if shape.Watch {
			shape.Target = GitHubAwaitToolName
		}
		shape.Args = githubAwaitArgs(clean[3:])
		return shape
	}
	run := len(clean) >= 3 && clean[1] == "run" &&
		((clean[2] == "watch") || (clean[2] == "list" && containsGitHubAwaitFlag(clean, "--watch")))
	if run {
		shape.Kind, shape.Target, shape.Watch = GitHubAwaitRunWatch, GitHubAwaitToolName, true
		shape.Args = githubAwaitArgs(clean[3:])
		return shape
	}
	shape.Kind = GitHubAwaitUnknown
	return shape
}

func githubAwaitWords(source string) []string {
	fields := strings.Fields(source)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.Trim(strings.TrimSpace(f), "\"'"); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func githubAwaitLoopTarget(words []string) string {
	for i := 0; i+1 < len(words); i++ {
		if words[i] != "gh" {
			continue
		}
		rest := words[i+1:]
		if len(rest) >= 2 && rest[0] == "run" && (rest[1] == "view" || rest[1] == "watch" || rest[1] == "list") {
			return GitHubAwaitToolName
		}
		if len(rest) >= 2 && rest[0] == "pr" && rest[1] == "checks" {
			return GitHubAwaitToolName
		}
	}
	return ""
}

func containsGitHubAwaitFlag(words []string, flag string) bool {
	for _, w := range words {
		if w == flag || strings.HasPrefix(w, flag+"=") {
			return true
		}
	}
	return false
}

func githubAwaitArgs(words []string) []string {
	var out []string
	for _, w := range words {
		if !strings.HasPrefix(w, "-") {
			out = append(out, w)
		}
	}
	return out
}

// MeasureGitHubAwaitCoverage aggregates wait/poll usage over lines of samples.
func MeasureGitHubAwaitCoverage(samples []string) GitHubAwaitMetrics {
	var m GitHubAwaitMetrics
	for _, line := range samples {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.Samples++
		shape := DetectGitHubAwait(githubAwaitWords(line))
		if shape.Kind == GitHubAwaitUnknown {
			continue
		}
		m.Await++
		if shape.Poll {
			m.PollLoops++
		}
		if shape.Target != "" {
			m.Covered++
		}
	}
	if m.Await > 0 {
		m.Coverage = math.Round(float64(m.Covered)/float64(m.Await)*1000) / 1000
	}
	if m.Samples > 0 {
		m.Share = math.Round(float64(m.Await)/float64(m.Samples)*1000) / 1000
	}
	return m
}
