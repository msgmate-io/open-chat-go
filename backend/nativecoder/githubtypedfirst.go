package nativecoder

import (
	"sort"
	"strings"
)

// GitHubTypedFirstPromptMarker is the stable first line of the typed-first
// GitHub prompt block. It makes the block idempotent when appended and
// machine-checkable ("did the prompt actually go typed-first?"), which is the
// phase 1 exit criterion "the coder system prompt prefers the typed tools for
// every GitHub workflow".
const GitHubTypedFirstPromptMarker = "GitHub typed tools (use these instead of `gh`):"

// GitHubTypedFirstPurpose is the one-line purpose of every typed GitHub tool as
// the coder system prompt presents it. It is the prompt-side twin of the plan's
// `github_cli` category target surface, so prompt and plan cannot drift apart
// silently (githubtypedfirst_test.go checks every plan tool is covered).
var GitHubTypedFirstPurpose = map[string]string{
	GitHubToolIssueList:    "list and filter issues (`gh issue list`)",
	GitHubToolIssueView:    "read one issue with its comments (`gh issue view`)",
	GitHubToolIssueCreate:  "open an issue (`gh issue create`), mutating",
	GitHubToolIssueComment: "comment on an issue (`gh issue comment`), mutating",
	GitHubToolPRList:       "list and filter pull requests (`gh pr list`)",
	GitHubToolPRView:       "read one pull request, its diff and checks (`gh pr view`, `gh pr diff`)",
	GitHubToolPRCreate:     "open a pull request (`gh pr create`), mutating",
	GitHubToolPRComment:    "comment on a pull request (`gh pr comment`), mutating",
	GitHubToolPRReview:     "submit a review (`gh pr review`), mutating",
	GitHubToolPRMerge:      "merge a pull request (`gh pr merge`), admin",
	GitHubToolAuthStatus:   "check the GitHub auth and token scope (`gh auth status`)",
	GitHubToolAPI:          "read-only REST/GraphQL call (`gh api`)",
	GitHubToolRunList:      "list workflow runs (`gh run list`), read-only",
	GitHubToolRunView:      "read one workflow run and its jobs (`gh run view`), read-only",
}

// GitHubTypedFirstTools returns the typed GitHub tool names in a stable order.
func GitHubTypedFirstTools() []string {
	tools := make([]string, 0, len(GitHubTypedFirstPurpose))
	for tool := range GitHubTypedFirstPurpose {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// GitHubTypedFirstGuidanceLines is the prompt block that makes GitHub work
// typed-first: every recurring `gh` workflow names the typed tool replacing it,
// and the mutating ones state that the write is re-read and reported as verified
// instead of claimed.
func GitHubTypedFirstGuidanceLines() []string {
	lines := []string{
		GitHubTypedFirstPromptMarker,
		"- use them instead of `gh ...` inside coder_bash: structured results, fewer confirmation round-trips, auditable calls.",
	}
	for _, tool := range GitHubTypedFirstTools() {
		lines = append(lines, "- "+tool+": "+GitHubTypedFirstPurpose[tool])
	}
	lines = append(lines,
		"- a mutating GitHub write is re-read with its typed read tool and reported as verified, never claimed: "+strings.Join(GitHubTypedFirstVerifiedWrites(), ", "),
		"- fall back to `gh` in coder_bash only for a workflow no typed tool covers, and say which one it is.",
	)
	return lines
}

// GitHubTypedFirstGuidance is the typed-first prompt block as one string.
func GitHubTypedFirstGuidance() string {
	return strings.Join(GitHubTypedFirstGuidanceLines(), "\n")
}

// MissingGitHubTypedTools lists the typed GitHub tools a system prompt does not
// name. An empty result means the prompt prefers every typed GitHub tool.
func MissingGitHubTypedTools(prompt string) []string {
	missing := []string{}
	for _, tool := range GitHubTypedFirstTools() {
		if !strings.Contains(prompt, tool) {
			missing = append(missing, tool)
		}
	}
	return missing
}

// GitHubTypedFirstPromptEvidence is the machine-checked state of the typed-first
// prompt half of phase 1.
type GitHubTypedFirstPromptEvidence struct {
	// Tools are the typed GitHub tools the prompt has to name.
	Tools []string `json:"tools"`
	// Missing are the typed tools the measured prompt does not name.
	Missing []string `json:"missing,omitempty"`
	// GuidanceApplied reports whether the prompt carries the typed-first block.
	GuidanceApplied bool `json:"guidance_applied"`
	// Ready reports whether the prompt prefers every typed GitHub tool.
	Ready bool `json:"ready"`
}

// MeasureGitHubTypedFirstPrompt measures a rendered coder system prompt against
// the typed-first requirement, so the criterion is attested from the prompt the
// run really uses rather than from the existence of a constant.
func MeasureGitHubTypedFirstPrompt(prompt string) GitHubTypedFirstPromptEvidence {
	missing := MissingGitHubTypedTools(prompt)
	applied := strings.Contains(prompt, GitHubTypedFirstPromptMarker)
	return GitHubTypedFirstPromptEvidence{
		Tools:           GitHubTypedFirstTools(),
		Missing:         missing,
		GuidanceApplied: applied,
		Ready:           applied && len(missing) == 0,
	}
}

// GitHubTypedFirstVerifiedWrites names the typed GitHub tools whose writes are
// re-read with a typed read tool and reported as verified (rather than claimed).
// It is derived from the shipped write-verification plans, so the prompt cannot
// promise verification for a tool that has none.
func GitHubTypedFirstVerifiedWrites() []string {
	seen := map[string]bool{}
	verified := []string{}
	for _, tool := range GitHubTypedFirstTools() {
		// Mutating is set only to get past the read-only guard; a read-only
		// tool still falls through the plan switch and reports no read-back.
		plan, ok := GitHubWriteVerificationFor(GitHubToolCall{Tool: tool, Mutating: true}, 0)
		if !ok || plan.ReadTool == "" {
			continue
		}
		if !seen[plan.Tool] {
			seen[plan.Tool] = true
			verified = append(verified, plan.Tool)
		}
	}
	sort.Strings(verified)
	return verified
}

// GitHubTypedFirstAppend returns prompt with the typed-first GitHub block
// appended. It is idempotent (the marker guards a second append) and never
// rewrites what the operator configured, so the composition is safe to run on
// every request.
func GitHubTypedFirstAppend(prompt string) string {
	block := GitHubTypedFirstGuidance()
	if block == "" {
		return prompt
	}
	if strings.Contains(prompt, GitHubTypedFirstPromptMarker) {
		return prompt
	}
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}
