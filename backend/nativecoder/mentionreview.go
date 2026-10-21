package nativecoder

import (
	"fmt"
	"strings"
)

// ci#795: a GitHub-mention-triggered deep PR review used to start with a broken
// workspace bootstrap. The mention trigger never resolved and passed the
// repository identity into the workspace set-up step, so the git integration's
// setup failed with `repository_name missing` - non-fatally. The agent then
// carried on in a checkout that was still on `main` and invented a ref-based
// review (`git show pr1327:<path>`, `git grep ... pr1327`, `git ls-tree`, ...)
// plus a pile of `.rev/*.txt` snapshots, i.e. 164 shell commands for what should
// be an ordinary "read the PR working tree" review.
//
// This file owns the main-repo half of the fix:
//
//  1. resolve the repository of a mention-triggered review from the GitHub event
//     payload (owner/name), falling back to the bot/trigger configuration, so
//     the workspace bootstrap always receives a non-empty `repository_name`;
//  2. turn that event into a PR checkout plan (fetch `refs/pull/<n>/head`, check
//     out the head, init submodules) so the review runs on the working tree
//     instead of through ref plumbing;
//  3. fail visibly (WorkspaceSetupFailed) when a review trigger cannot resolve a
//     repository, instead of letting the agent silently review `main`.
//
// Nothing here carries a credential value; the rendered checkout commands only
// name the PR ref and never a token.

const (
	// ViaTagPrefix prefixes the dispatch tag that encodes the GitHub event a
	// coder interaction was created from (`via:mention`, `via:assign`, ...).
	ViaTagPrefix = "via:"
	// MentionReviewEventTag is the event a mention-triggered review carries.
	MentionReviewEventTag = "mention"
	// ReviewEventConfigKey carries the raw GitHub event payload of a
	// trigger-dispatched review (webhook body, already decoded).
	ReviewEventConfigKey = "github_event"
	// RepositoryNameConfigKey is the setup-step key the git integration requires
	// for its repository/index context; it is the resolved `owner/name` slug.
	RepositoryNameConfigKey = "repository_name"
	// PRCheckoutConfigKey records the resolved PR checkout plan on the config so
	// the workspace bootstrap can act on it (and logs can show it).
	PRCheckoutConfigKey = "github_pr_checkout"
	// WorkspaceSetupFailed is the interaction/dispatch status a workspace
	// bootstrap failure is reported as, instead of reviewing the wrong branch.
	WorkspaceSetupFailed = "workspace_setup_failed"
	// ReviewBranchPrefix names the local branch a PR review checkout uses.
	ReviewBranchPrefix = "pr-review/"
)

// PRCheckoutPlan is the resolved checkout target of a PR review: the repository,
// the pull request number, the base/head refs and the head SHA when the event
// carries one.
type PRCheckoutPlan struct {
	Repo    string
	Number  int
	BaseRef string
	HeadRef string
	HeadSHA string
	// ReferenceBased is true when no pull request number was resolved, ie the
	// review cannot materialize a working tree and must use the typed
	// ref-based read path instead of an ad-hoc `git show`.
	ReferenceBased bool
}

// HeadFetchRef is the ref the review checkout fetches: GitHub's PR head ref, so
// a review never needs a manual `git fetch origin pull/<n>/head:<branch>`.
func (p PRCheckoutPlan) HeadFetchRef() string {
	if p.Number <= 0 {
		return ""
	}
	return fmt.Sprintf("refs/pull/%d/head", p.Number)
}

// ReviewBranch is the local branch a checkout of this plan creates.
func (p PRCheckoutPlan) ReviewBranch() string {
	if p.Number <= 0 {
		return ""
	}
	return fmt.Sprintf("%s%d", ReviewBranchPrefix, p.Number)
}

// IsMentionReviewTrigger reports whether a dispatch's tags mark it as a
// GitHub-mention-triggered review.
func IsMentionReviewTrigger(tags []string) bool {
	for _, tag := range tags {
		value := strings.ToLower(strings.TrimSpace(tag))
		if !strings.HasPrefix(value, ViaTagPrefix) {
			continue
		}
		switch strings.TrimSpace(strings.TrimPrefix(value, ViaTagPrefix)) {
		case MentionReviewEventTag, "review", "pr_review", "pull_request_review":
			return true
		}
	}
	return false
}

// ReviewEventRepo resolves `owner/name` from a GitHub event payload. It accepts
// the shapes a mention trigger delivers: a repository object
// (`repository.full_name`, or `repository.name` plus `repository.owner.login` /
// `organization.login`), a `repository_url`/`repo_url`, or an issue/pull request
// html_url (`https://github.com/<owner>/<name>/pull/<n>`).
func ReviewEventRepo(event map[string]interface{}) string {
	if event == nil {
		return ""
	}
	if repo := repositoryObjectRepo(event); repo != "" {
		return repo
	}
	for _, key := range []string{"repository_url", "repo_url", "clone_url"} {
		if value, ok := event[key].(string); ok {
			if repo := repoFromURL(value); repo != "" {
				return repo
			}
		}
	}
	for _, key := range []string{"pull_request", "issue"} {
		nested, ok := event[key].(map[string]interface{})
		if !ok {
			continue
		}
		for _, urlKey := range []string{"html_url", "url"} {
			text, ok := nested[urlKey].(string)
			if !ok {
				continue
			}
			if repo := repoFromURL(text); repo != "" {
				return repo
			}
		}
	}
	for _, key := range []string{"html_url", "url", "body"} {
		text, ok := event[key].(string)
		if !ok {
			continue
		}
		if repo := repoFromURL(text); repo != "" {
			return repo
		}
	}
	return ""
}

// ReviewEventNumber resolves the pull request number a mention-triggered review
// is about, or 0 when the event carries none.
func ReviewEventNumber(event map[string]interface{}) int {
	if event == nil {
		return 0
	}
	if number, ok := numberFromValue(event["number"]); ok {
		return number
	}
	for _, key := range []string{"pull_request", "issue"} {
		raw, ok := event[key].(map[string]interface{})
		if !ok {
			continue
		}
		if number, ok := numberFromValue(raw["number"]); ok {
			return number
		}
	}
	return 0
}

// ReviewEventHeadSHA returns the PR head SHA the event carries, or "".
func ReviewEventHeadSHA(event map[string]interface{}) string {
	raw, ok := event["pull_request"].(map[string]interface{})
	if !ok {
		return ""
	}
	head, ok := raw["head"].(map[string]interface{})
	if !ok {
		return ""
	}
	sha, _ := head["sha"].(string)
	return strings.TrimSpace(sha)
}

// ReviewEventBaseRef returns the base ref (`main`, ...) the PR targets, or "".
func ReviewEventBaseRef(event map[string]interface{}) string {
	raw, ok := event["pull_request"].(map[string]interface{})
	if !ok {
		return ""
	}
	base, ok := raw["base"].(map[string]interface{})
	if !ok {
		return ""
	}
	ref, _ := base["ref"].(string)
	return strings.TrimSpace(ref)
}

// ReviewEventHeadRef returns the head ref (`feature/x`) of the PR, or "".
func ReviewEventHeadRef(event map[string]interface{}) string {
	raw, ok := event["pull_request"].(map[string]interface{})
	if !ok {
		return ""
	}
	head, ok := raw["head"].(map[string]interface{})
	if !ok {
		return ""
	}
	ref, _ := head["ref"].(string)
	return strings.TrimSpace(ref)
}

// MentionReviewCheckoutForInteraction resolves the PR checkout plan of a
// mention-triggered review, or false when the interaction is not a
// mention-triggered review at all. The repository is resolved from the event
// payload first (acceptance criterion: a mention trigger without an explicit
// repository must resolve it from the event); when the payload carries no
// repository it falls back to what the dispatch pinned (`repo:<owner/name>`
// tag, tool_init/config_overrides repo) and to the bot/trigger configuration.
func MentionReviewCheckoutForInteraction(config map[string]interface{}, tags []string, toolInit, configOverrides map[string]interface{}) (PRCheckoutPlan, bool) {
	event := ReviewEvent(config, toolInit, configOverrides)
	if !IsMentionReviewTrigger(tags) && event == nil {
		return PRCheckoutPlan{}, false
	}
	plan := PRCheckoutPlan{
		Repo:    ReviewEventRepo(event),
		BaseRef: ReviewEventBaseRef(event),
	}
	if plan.Repo == "" {
		plan.Repo = NormalizeRepo(ExplicitAutoSandboxRepo(toolInit, configOverrides))
	}
	if plan.Repo == "" {
		plan.Repo = NormalizeRepo(AutoSandboxRepoTarget(config, tags))
	}
	if event != nil {
		plan.Number = ReviewEventNumber(event)
		plan.HeadSHA = ReviewEventHeadSHA(event)
		plan.HeadRef = ReviewEventHeadRef(event)
	}
	if plan.HeadRef == "" {
		plan.HeadRef = BranchValueFromTags(tags)
	}
	plan.ReferenceBased = plan.Number <= 0
	return plan, true
}

// ReviewEvent returns the GitHub event payload a dispatch carried, checking the
// effective config, the explicit config overrides and tool_init in that order.
func ReviewEvent(config, toolInit, configOverrides map[string]interface{}) map[string]interface{} {
	for _, source := range []map[string]interface{}{configOverrides, config, toolInit} {
		if source == nil {
			continue
		}
		switch typed := source[ReviewEventConfigKey].(type) {
		case map[string]interface{}:
			return typed
		case string:
			if parsed, ok := decodeReviewEvent(typed); ok {
				return parsed
			}
		}
	}
	return nil
}

// ValidateMentionReviewWorkspace fails a mention-triggered review whose
// workspace bootstrap cannot be prepared, so the interaction is marked
// WorkspaceSetupFailed with a reason instead of silently reviewing `main` - the
// silent, non-fatal failure this ticket is about.
func ValidateMentionReviewWorkspace(plan PRCheckoutPlan) error {
	if NormalizeRepo(plan.Repo) == "" {
		return fmt.Errorf(
			"%s: the review trigger resolved no repository, so the workspace bootstrap cannot pass a repository_name "+
				"to the setup step; configure the trigger with a repository or dispatch a repo:<owner/name> tag",
			WorkspaceSetupFailed,
		)
	}
	return nil
}

// BindMentionReviewWorkspace returns config prepared for a mention-triggered
// review: the resolved repository is exported as the setup-step
// `repository_name`, bound to the automatic per-chat sandbox checkout, and the
// PR head checkout commands are appended to the sandbox set-up script, so the
// review runs on the PR working tree. A repository the caller pinned itself is
// preserved (the binding helpers never overwrite an explicit pin).
func BindMentionReviewWorkspace(config map[string]interface{}, plan PRCheckoutPlan) (map[string]interface{}, error) {
	if err := ValidateMentionReviewWorkspace(plan); err != nil {
		return config, err
	}
	bound := WithAutoSandboxRepo(config, plan.Repo)
	bound = WithAutoSandboxBranch(bound, plan.HeadRef)
	bound = withReviewCheckoutPlan(bound, plan)
	return bound, nil
}

// ReviewCheckoutCommands are the shell lines a review workspace runs to land on
// the PR head: fetch GitHub's PR head ref, check the review branch out at it and
// initialize the submodules the review needs. A plan without a PR number yields
// no commands (the typed ref-based read path is used instead).
func ReviewCheckoutCommands(plan PRCheckoutPlan) []string {
	ref := plan.HeadFetchRef()
	if ref == "" {
		return nil
	}
	branch := plan.ReviewBranch()
	return []string{
		fmt.Sprintf("[ci#795 review checkout] git fetch origin %s", ref),
		fmt.Sprintf("[ci#795 review checkout] git checkout -B %s FETCH_HEAD", branch),
		"[ci#795 review checkout] git submodule update --init --recursive",
	}
}

// RenderReviewCheckoutScript renders the checkout commands as the shell script
// stored in SandboxSetupCommandsConfigKey.
func RenderReviewCheckoutScript(plan PRCheckoutPlan) string {
	commands := ReviewCheckoutCommands(plan)
	if len(commands) == 0 {
		return ""
	}
	lines := make([]string, 0, len(commands))
	for _, line := range commands {
		lines = append(lines, stripReviewCheckoutMarker(line))
	}
	return strings.Join(lines, "\n") + "\n"
}

func stripReviewCheckoutMarker(line string) string {
	if _, rest, ok := strings.Cut(line, "] "); ok {
		return rest
	}
	return line
}

// withReviewCheckoutPlan records the plan and appends its checkout script to the
// sandbox set-up commands without clobbering a profile's own set-up script.
func withReviewCheckoutPlan(config map[string]interface{}, plan PRCheckoutPlan) map[string]interface{} {
	if config == nil {
		return config
	}
	bound := make(map[string]interface{}, len(config)+3)
	for key, value := range config {
		bound[key] = value
	}
	bound[RepositoryNameConfigKey] = plan.Repo
	record := map[string]interface{}{
		"repo":            plan.Repo,
		"number":          plan.Number,
		"base_ref":        plan.BaseRef,
		"head_ref":        plan.HeadRef,
		"head_sha":        plan.HeadSHA,
		"fetch_ref":       plan.HeadFetchRef(),
		"branch":          plan.ReviewBranch(),
		"reference_based": plan.ReferenceBased,
	}
	bound[PRCheckoutConfigKey] = record
	if script := RenderReviewCheckoutScript(plan); script != "" {
		if existing, ok := config[SandboxSetupCommandsConfigKey].(string); ok && strings.TrimSpace(existing) != "" {
			bound[SandboxSetupCommandsConfigKey] = strings.TrimRight(existing, "\n") + "\n" + script
		} else {
			bound[SandboxSetupCommandsConfigKey] = script
		}
	}
	return bound
}
