package nativecoder

// gitshell.go is the phase-3 (ci#514) counterpart of githubshell.go: it maps the
// raw `git ...` shell shapes that still reach coder_bash onto the typed
// `coder_git_*` reads/writes the capability plan already declares for the
// repo_inspection and git_mutation categories (BashCategoryRepoInspection,
// BashCategoryGitMutation). The bridge is pure/advisory: it never executes
// anything, it only classifies and translates so the plan can measure how much
// of the remaining shell is covered by the typed git surface.

import (
	"fmt"
	"sort"
	"strings"
)

// GitReadTool / GitWriteTool naming follows the capability plan's target tools.
const (
	GitShellToolLog    = "coder_git_log"
	GitShellToolShow   = "coder_git_show"
	GitShellToolDiff   = "coder_git_diff"
	GitShellToolBlame  = "coder_git_blame"
	GitShellToolRemote = "coder_git_remote"
	GitShellToolStatus = "coder_git_status"

	GitShellToolBranch   = "coder_git_branch"
	GitShellToolCheckout = "coder_git_checkout"
	GitShellToolAdd      = "coder_git_add"
	GitShellToolCommit   = "coder_git_commit"
	GitShellToolPush     = "coder_git_push"
	GitShellToolFetch    = "coder_git_fetch"
	GitShellToolStash    = "coder_git_stash"
	GitShellToolTag      = "coder_git_tag"
)

// gitShellDefaultBranch is the branch the typed surface refuses to push to.
const gitShellDefaultBranch = "main"

// GitShellTranslation is the typed call a raw git command maps onto.
type GitShellTranslation struct {
	Command string            `json:"command"`
	Tool    string            `json:"tool"`
	Args    map[string]string `json:"args,omitempty"`
	Write   bool              `json:"write"`
	Guarded bool              `json:"guarded,omitempty"`
	Reason  string            `json:"guarded_reason,omitempty"`
}

// GitShellTranslationReport summarises how much of a sample of git commands the
// typed surface already covers.
type GitShellTranslationReport struct {
	Total        int                   `json:"total"`
	Translated   int                   `json:"translated"`
	Guarded      int                   `json:"guarded"`
	Untranslated []string              `json:"untranslated,omitempty"`
	Translations []GitShellTranslation `json:"translations,omitempty"`
}

// Covered reports the covered share in [0,1]; a sample of zero commands counts
// as fully covered so an empty sample never blocks a phase gate.
func (r GitShellTranslationReport) Covered() float64 {
	if r.Total == 0 {
		return 1
	}
	return float64(r.Translated+r.Guarded) / float64(r.Total)
}

// IsGitShellCommand reports whether cmd is a plain `git ...` invocation that is
// not `gh`-driven and not a coder_git call. Any git subcommand counts, so
// commands the typed surface cannot express yet stay visible in the report.
func IsGitShellCommand(cmd string) bool {
	fields := gitShellFields(cmd)
	return len(fields) >= 2 && fields[0] == "git"
}

func gitShellFields(cmd string) []string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	for len(fields) > 0 && (strings.HasPrefix(fields[0], "sudo") || fields[0] == "env") {
		fields = fields[1:]
	}
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
		fields = fields[1:] // drop env assignments like GIT_PAGER=cat
	}
	// git -C <dir> / -c <opt> consume one argument.
	for len(fields) >= 3 && (fields[0] == "git") && (fields[1] == "-C" || fields[1] == "-c") {
		fields = append([]string{"git"}, fields[3:]...)
	}
	return fields
}

// gitShellSubcommand normalises aliases onto the canonical git subcommand.
func gitShellSubcommand(sub string) string {
	switch sub {
	case "log", "show", "diff", "blame", "remote", "status", "branch", "checkout",
		"switch", "add", "commit", "push", "fetch", "pull", "stash", "tag", "restore":
		return sub
	case "cat-file":
		return "show"
	case "co":
		return "checkout"
	default:
		return ""
	}
}

// TranslateGitShellCommand maps one raw git command onto its typed counterpart.
// ok is false when there is no typed equivalent yet (it stays plain shell and
// must show up in the report's untranslated list).
func TranslateGitShellCommand(cmd string) (GitShellTranslation, bool) {
	fields := gitShellFields(cmd)
	if len(fields) == 0 || fields[0] != "git" {
		return GitShellTranslation{}, false
	}
	args := fields[1:]
	sub := ""
	if len(args) > 0 {
		sub = gitShellSubcommand(args[0])
	} else {
		sub = "status"
	}
	if sub == "" {
		return GitShellTranslation{}, false
	}
	rest := args[1:]
	if len(args) == 0 {
		rest = nil
	}

	t := GitShellTranslation{Command: strings.TrimSpace(cmd), Args: map[string]string{}}
	switch sub {
	case "status":
		t.Tool = GitShellToolStatus
	case "log":
		t.Tool = GitShellToolLog
	case "show":
		t.Tool = GitShellToolShow
	case "diff":
		t.Tool = GitShellToolDiff
	case "blame":
		t.Tool = GitShellToolBlame
	case "remote":
		t.Tool = GitShellToolRemote
	case "branch":
		t.Tool = GitShellToolBranch
		t.Args["list"] = "true"
	case "tag":
		t.Tool = GitShellToolTag
		if gitShellRevArg(rest) == "" {
			t.Args["list"] = "true"
		} else {
			t.Write = true
		}
	case "add":
		t.Tool, t.Write = GitShellToolAdd, true
	case "commit":
		t.Tool, t.Write = GitShellToolCommit, true
	case "push":
		t.Tool, t.Write = GitShellToolPush, true
	case "fetch", "pull":
		t.Tool, t.Write = GitShellToolFetch, true
	case "stash":
		t.Tool, t.Write = GitShellToolStash, true
	case "checkout", "switch", "restore":
		t.Tool, t.Write = GitShellToolCheckout, true
	}

	if path := gitShellPathArg(rest); path != "" {
		t.Args["path"] = path
	}
	if rev := gitShellRevArg(rest); rev != "" {
		t.Args["rev"] = rev
	}
	if t.Write && sub == "push" {
		branch := gitShellPushBranch(rest)
		t.Args["branch"] = branch
		if branch == "" || branch == gitShellDefaultBranch {
			t.Guarded = true
			t.Reason = "typed git surface never pushes the default branch (" + gitShellDefaultBranch + ")"
		}
	}
	return t, true
}

// gitShellRevArg picks the first non-flag argument that is not a path.
func gitShellRevArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.Contains(a, "/") {
			continue
		}
		return a
	}
	return ""
}

// gitShellPathArg picks the first argument that looks like a path/rev range.
func gitShellPathArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "/") || strings.Contains(a, "*") {
			return a
		}
	}
	return ""
}

// gitShellPushBranch reads the branch of `git push -u origin <branch>` / `git
// push origin <branch>` and returns "" when the command relies on the current
// branch (which the typed surface resolves itself and guards).
func gitShellPushBranch(args []string) string {
	positional := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) >= 2 {
		return positional[1]
	}
	return ""
}

// MeasureGitShellTranslation reports how much of a git-command sample the typed
// coder_git surface already covers. Non-git commands are ignored.
func MeasureGitShellTranslation(commands []string) GitShellTranslationReport {
	report := GitShellTranslationReport{}
	seenUntranslated := map[string]struct{}{}
	for _, cmd := range commands {
		if !IsGitShellCommand(cmd) {
			continue
		}
		report.Total++
		t, ok := TranslateGitShellCommand(cmd)
		if !ok {
			if _, dup := seenUntranslated[cmd]; !dup {
				seenUntranslated[cmd] = struct{}{}
				report.Untranslated = append(report.Untranslated, strings.TrimSpace(cmd))
			}
			continue
		}
		if t.Guarded {
			report.Guarded++
		} else {
			report.Translated++
		}
		report.Translations = append(report.Translations, t)
	}
	sort.Strings(report.Untranslated)
	return report
}

// gitTypedFirstMarker is the leading line of GitTypedFirstGuidance(). It is the
// idempotency token of GitTypedFirstAppend and the attestation marker of
// MeasureGitTypedFirstPrompt, so the two can never drift apart.
const gitTypedFirstMarker = "Prefer the typed git tools over coder_bash for every git workflow:"

// gitTypedFirstReadTools / gitTypedFirstWriteTools are the single source of the
// typed git tool lists: the guidance and the prompt measurement both derive from
// them, so a tool can never be named in one and missing from the other.
func gitTypedFirstReadTools() []string {
	return []string{GitShellToolStatus, GitShellToolLog, GitShellToolShow, GitShellToolDiff, GitShellToolBlame, GitShellToolRemote}
}

func gitTypedFirstWriteTools() []string {
	return []string{GitShellToolBranch, GitShellToolCheckout, GitShellToolAdd, GitShellToolCommit, GitShellToolPush, GitShellToolFetch, GitShellToolStash, GitShellToolTag}
}

// GitTypedFirstTools is the flat list of every typed git tool the typed-first
// block names, reads first.
func GitTypedFirstTools() []string {
	tools := append([]string(nil), gitTypedFirstReadTools()...)
	return append(tools, gitTypedFirstWriteTools()...)
}

// GitTypedFirstGuidance is appended to the coder system prompt so the dev agent
// reaches for the typed git surface instead of shelling out (phase-3 exit
// criterion: "no dev-agent prompt instructs a raw git command").
func GitTypedFirstGuidance() string {
	lines := []string{
		gitTypedFirstMarker,
		"  - reads: " + strings.Join(gitTypedFirstReadTools(), ", "),
		"  - writes: " + strings.Join(gitTypedFirstWriteTools(), ", "),
		fmt.Sprintf("The typed surface refuses to push %s and refuses to create an empty commit; never work around that with coder_bash.", gitShellDefaultBranch),
	}
	return strings.Join(lines, "\n")
}

// GitTypedFirstPromptMeasurement attests that a rendered coder system prompt
// carries the typed-first git block and names every typed git tool.
type GitTypedFirstPromptMeasurement struct {
	Ready           bool     `json:"ready"`
	GuidanceApplied bool     `json:"guidance_applied"`
	Tools           []string `json:"tools"`
	Missing         []string `json:"missing,omitempty"`
}

// GitTypedFirstAppend returns systemPrompt with the typed-first git guidance
// appended. It is idempotent: a prompt that already carries the block (a config
// that pre-baked the guidance) is returned byte-identical, because the prompt is
// paid for on every turn.
func GitTypedFirstAppend(systemPrompt string) string {
	if strings.Contains(systemPrompt, gitTypedFirstMarker) {
		return systemPrompt
	}
	guidance := GitTypedFirstGuidance()
	trimmed := strings.TrimSpace(systemPrompt)
	if trimmed == "" {
		return guidance
	}
	return trimmed + "\n\n" + guidance
}

// MeasureGitTypedFirstPrompt attests the phase-3 prompt criterion from the
// prompt the run really uses, so the criterion cannot be met by the mere
// existence of a constant: Ready requires the block to be present AND every
// typed git tool to be named.
func MeasureGitTypedFirstPrompt(systemPrompt string) GitTypedFirstPromptMeasurement {
	tools := GitTypedFirstTools()
	measurement := GitTypedFirstPromptMeasurement{Tools: tools}
	measurement.GuidanceApplied = strings.Contains(systemPrompt, gitTypedFirstMarker)
	for _, tool := range tools {
		if !strings.Contains(systemPrompt, tool) {
			measurement.Missing = append(measurement.Missing, tool)
		}
	}
	measurement.Ready = measurement.GuidanceApplied && len(measurement.Missing) == 0
	return measurement
}
