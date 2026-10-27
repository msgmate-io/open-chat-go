// githubrun.go is the ci#514 phase-1 companion of githubshell.go: it maps the
// `gh run` CI-watching workflow — `gh run list` and `gh run view` — onto the
// typed github_run_list and github_run_view calls, so an agent reads pipeline
// status with typed tools instead of a shell, and never needs `gh run watch`
// loops (the retirement plan's "GitHub first" surface).

package nativecoder

import (
	"strconv"
	"strings"
)

// The typed GitHub CI tools of the ci#514 phase 1 target surface.
const (
	GitHubToolRunList = "github_run_list"
	GitHubToolRunView = "github_run_view"
)

// GitHubRunListCall is the typed equivalent of `gh run list [flags]`.
type GitHubRunListCall struct {
	Repo     string // --repo / -R
	Workflow string // --workflow / -w
	Branch   string // --branch / -b
	Status   string // --status / -s (queued|in_progress|completed|...)
	Limit    int    // --limit / -L (gh default 10)
}

// GitHubRunViewCall is the typed equivalent of `gh run view <ref> [flags]`,
// where ref is a workflow run id, a branch/commit, or "latest".
type GitHubRunViewCall struct {
	Repo       string // --repo / -R
	RunID      string // positional ref ("latest" when omitted)
	JSON       bool   // --json
	JSONFields string // the --json field list
	ExitStatus bool   // --exit-status
	Failed     bool   // --log-failed
}

// GitHubRunTranslation is one successful `gh run` -> typed-tool translation.
type GitHubRunTranslation struct {
	Tool     string
	ListCall GitHubRunListCall
	ViewCall GitHubRunViewCall
}

// GitHubRunMetrics is the ci#514 coverage measurement restricted to `gh run`.
type GitHubRunMetrics struct {
	Total       int
	Expressible int
	Residual    []string
}

// IsGitHubRunCommand reports whether line commands the `gh run` subcommand
// (optionally behind leading environment assignments, e.g. GH_REPO=x gh run ...).
func IsGitHubRunCommand(line string) bool {
	w := ghRunWords(line)
	return len(w) >= 2 && w[0] == "gh" && w[1] == "run"
}

// GitHubRunToolName returns the typed tool for a `gh run` command, or "" when
// the verb is not part of the typed surface (rerun, download, watch, ...).
func GitHubRunToolName(line string) string {
	t, ok := TranslateGitHubRunCommand(line)
	if !ok {
		return ""
	}
	return t.Tool
}

// TranslateGitHubRunCommand translates a `gh run` command into its typed call.
// The second return reports whether the typed surface covers the verb; list
// and view are covered today, the rest stays residual shell.
func TranslateGitHubRunCommand(line string) (GitHubRunTranslation, bool) {
	if !IsGitHubRunCommand(line) {
		return GitHubRunTranslation{}, false
	}
	w := ghRunWords(line)
	if len(w) < 3 {
		return GitHubRunTranslation{}, false // naked `gh run` stays residual
	}
	args := w[3:]
	switch w[2] {
	case "list":
		return GitHubRunTranslation{Tool: GitHubToolRunList, ListCall: translateGhRunList(args)}, true
	case "view":
		return GitHubRunTranslation{Tool: GitHubToolRunView, ViewCall: translateGhRunView(args)}, true
	default:
		return GitHubRunTranslation{}, false // rerun/download/watch/... remain residual
	}
}

// MeasureGitHubRunTranslation counts how much of a `gh run` sample the typed
// surface already expresses (the per-niche input of the ci#514 coverage).
func MeasureGitHubRunTranslation(commands []string) GitHubRunMetrics {
	m := GitHubRunMetrics{}
	for _, c := range commands {
		if !IsGitHubRunCommand(c) {
			continue
		}
		m.Total++
		if _, ok := TranslateGitHubRunCommand(c); ok {
			m.Expressible++
		} else {
			m.Residual = append(m.Residual, c)
		}
	}
	return m
}

func translateGhRunList(args []string) GitHubRunListCall {
	call := GitHubRunListCall{Limit: 10}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if i+1 < len(args) {
			switch a {
			case "--repo", "-R":
				call.Repo = args[i+1]
				i++
				continue
			case "--workflow", "-w":
				call.Workflow = args[i+1]
				i++
				continue
			case "--branch", "-b":
				call.Branch = args[i+1]
				i++
				continue
			case "--status", "-s":
				call.Status = args[i+1]
				i++
				continue
			case "--limit", "-L":
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					call.Limit = n
				}
				i++
				continue
			}
		}
	}
	return call
}

func translateGhRunView(args []string) GitHubRunViewCall {
	call := GitHubRunViewCall{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-R" || a == "--repo" {
			if i+1 < len(args) {
				call.Repo = args[i+1]
				i++
			}
			continue
		}
		switch a {
		case "--json":
			if i+1 < len(args) {
				call.JSON = true
				call.JSONFields = strings.TrimSpace(args[i+1])
				i++
			}
		case "--log-failed":
			call.Failed = true
		case "--exit-status":
			call.ExitStatus = true
		default:
			if call.RunID == "" && !strings.HasPrefix(a, "-") {
				call.RunID = a
			}
		}
	}
	if call.RunID == "" {
		call.RunID = "latest"
	}
	return call
}

// ghRunWords splits a shell line into tokens, dropping a leading run of
// environment assignments (GH_REPO=x ...) so the command name is token[0].
func ghRunWords(line string) []string {
	fields := strings.Fields(line)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") {
		i++
	}
	return fields[i:]
}}
