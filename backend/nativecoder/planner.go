package nativecoder

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file is the pure decision core of the auto-consolidation feature
// (ci#653): given the open pull requests of the agent's repository and the
// consolidation PRs already open, it decides which PRs are *ready* and whether
// to open/update exactly one `chore/consolidate-<date>` PR — or, when one is
// already open, to roll the newly ready PRs into the NEXT consolidation
// (ci#652).
//
// It performs no I/O (the caller fetches the facts, see github.go) and no
// mutation, so the behaviour is unit-testable without GitHub.

// GitHub merge-state values used by the planner.
const (
	MergeStateClean   = "CLEAN"
	MergeStateBlocked = "BLOCKED"
	MergeStateBehind  = "BEHIND"
	MergeStateDirty   = "DIRTY"
	MergeStateDraft   = "DRAFT"
	MergeStateUnknown = "UNKNOWN"
)

// ConsolidationLabel is the label the development loop uses to recognise a
// consolidation PR (ci#652). The planner uses it in addition to the
// branch-prefix check so a PR opened by the external controller is recognised.
const ConsolidationLabel = "consolidation"

// PullRequest is the subset of the GitHub pull-request facts the planner needs.
type PullRequest struct {
	Number           int    `json:"number"`
	Title            string `json:"title"`
	Author           string `json:"author"`
	HeadRef          string `json:"head_ref"`
	BaseRef          string `json:"base_ref"`
	HeadSHA          string `json:"head_sha"`
	Draft            bool   `json:"draft"`
	Mergeable        bool   `json:"mergeable"`
	MergeStateStatus string `json:"merge_state_status"`
	// ChecksState is the state of the *required* checks when RequiredChecksKnown
	// is set, and otherwise the aggregate state of every check (ci#683).
	ChecksState string `json:"checks_state"`
	// RequiredChecksKnown reports that the base branch's required contexts were
	// resolved, so ChecksState is the state of exactly those contexts.
	RequiredChecksKnown bool `json:"required_checks_known,omitempty"`
	// RequiredChecksState is the aggregate state over exactly the required
	// contexts of the base branch ("success", "failure" or "pending").
	RequiredChecksState string `json:"required_checks_state,omitempty"`
	// FailingRequiredChecks names the required contexts that are not green.
	FailingRequiredChecks []string `json:"failing_required_checks,omitempty"`
	// InformationalChecks names red checks/statuses the base-branch ruleset does
	// not require (eg the `private-sandbox-tests` commit status). They are
	// reported, and they never block a merge.
	InformationalChecks []string  `json:"informational_checks,omitempty"`
	Labels              []string  `json:"labels,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	// Body is the PR description, used to resolve the leaf issue the PR closes
	// (ci#813).
	Body string `json:"-"`
	// LinkedIssues are the leaf issues named by standalone `Closes
	// msgmate-io/open-chat-go-ci#N` lines in Body (ci#813).
	LinkedIssues []int `json:"linked_issues,omitempty"`
}

// ExistingConsolidation describes an already open consolidation PR.
type ExistingConsolidation struct {
	Number  int    `json:"number"`
	Branch  string `json:"branch"`
	HeadSHA string `json:"head_sha"`
	Author  string `json:"author"`
	// Green reports whether every required check on the consolidation PR is
	// green, ie whether the ci#538 auto-approve path may merge it.
	Green bool `json:"green"`
	// InformationalChecks names red checks/statuses the base-branch ruleset does
	// not require; they are reported and never block the merge (ci#683).
	InformationalChecks []string `json:"informational_checks,omitempty"`
	// CreatedAt orders multiple (unexpected) open consolidation PRs: the
	// oldest stays the live one, the rest are superseded.
	CreatedAt time.Time `json:"created_at,omitempty"`
	// LinkedIssues are the leaf issues already named by the open consolidation
	// PR's `Closes` lines. They are preserved when the body is refreshed and
	// closed once the PR merges (ci#813).
	LinkedIssues []int `json:"linked_issues,omitempty"`
}

// ConsolidationFacts is the input of the planner.
type ConsolidationFacts struct {
	// Repo is the `owner/name` repository the facts belong to.
	Repo string
	// SelfLogin is the agent's own GitHub login (`authors: [self]`).
	SelfLogin string
	// OpenPRs are the repository's open pull requests.
	OpenPRs []PullRequest
	// OpenConsolidations are the open consolidation PRs (branch prefix or
	// label), if any.
	OpenConsolidations []ExistingConsolidation
	// Now is the reference time for the branch date; zero means time.Now().
	Now time.Time
}

// ReadyDecision records why a pull request was accepted or rejected, so the
// plan is explainable in a PR comment or a tracker issue.
type ReadyDecision struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
}

// ConsolidationPlan is the outcome of one planning pass.
type ConsolidationPlan struct {
	Enabled bool   `json:"enabled"`
	Repo    string `json:"repo"`
	Base    string `json:"base"`
	Scope   string `json:"scope"`
	// Existing is the single live consolidation PR, when one is open.
	Existing *ExistingConsolidation `json:"existing,omitempty"`
	// Include are the ready PRs this consolidation folds in.
	Include []PullRequest `json:"include"`
	// Deferred are ready PRs that roll into the NEXT consolidation (ci#652).
	Deferred []PullRequest `json:"deferred"`
	// LinkedIssues are the leaf issues this consolidation PR closes: the
	// existing PR's links plus every included PR's link (ci#813).
	LinkedIssues []int `json:"linked_issues,omitempty"`
	// Superseded are extra open consolidation PRs to absorb/close so exactly
	// one stays open.
	Superseded []ExistingConsolidation `json:"superseded,omitempty"`
	// Decisions explains the ready-PR selection.
	Decisions []ReadyDecision `json:"decisions"`
	// ShouldOpen reports that a new consolidation PR must be opened.
	ShouldOpen bool `json:"should_open"`
	// ShouldUpdate reports that the existing consolidation PR must be
	// refreshed (main merged back in / checks re-run) rather than replaced.
	ShouldUpdate bool `json:"should_update"`
	// ShouldMerge reports that the live consolidation PR is green and may be
	// merged through the existing auto-approve path (ci#538).
	ShouldMerge bool `json:"should_merge"`
	// Branch is the head branch of the (new) consolidation PR.
	Branch string `json:"branch,omitempty"`
	// Labels is the standard label set to apply.
	Labels []string `json:"labels"`
	// RollupIssue reports whether the consolidation tracker issue (ci#415)
	// should be maintained.
	RollupIssue bool `json:"rollup_issue"`
	// Reason is a short human-readable explanation of the decision.
	Reason string `json:"reason,omitempty"`
}

// Plan consolidates the given facts under the given policy. It is a pure
// function: facts in, plan out.
func Plan(policy AutoConsolidationPolicy, facts ConsolidationFacts) ConsolidationPlan {
	policy = normalizePolicy(policy)
	now := facts.Now
	if now.IsZero() {
		now = time.Now()
	}

	plan := ConsolidationPlan{
		Enabled:     policy.Enabled,
		Repo:        facts.Repo,
		Base:        policy.Base,
		Scope:       policy.Scope,
		Labels:      append([]string(nil), policy.Labels...),
		RollupIssue: policy.RollupIssue,
		Decisions:   []ReadyDecision{},
		Include:     []PullRequest{},
		Deferred:    []PullRequest{},
	}
	if !policy.Enabled {
		plan.Reason = "auto consolidation disabled for this agent"
		return plan
	}
	if !repoInScope(policy, facts.Repo) {
		plan.Reason = fmt.Sprintf("repository %s is outside the configured scope", facts.Repo)
		return plan
	}

	existing := selectLiveConsolidation(policy, facts.OpenConsolidations)
	if existing != nil {
		plan.Existing = existing
	}
	plan.Superseded = supersededConsolidations(policy, facts.OpenConsolidations, existing)

	ready := make([]PullRequest, 0, len(facts.OpenPRs))
	candidates := append([]PullRequest(nil), facts.OpenPRs...)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Number < candidates[j].Number })
	for _, pr := range candidates {
		decision := evaluateReady(policy, facts, pr)
		plan.Decisions = append(plan.Decisions, decision)
		if decision.Ready {
			ready = append(ready, pr)
		}
	}

	if existing != nil {
		if policy.OneOpenAtATime {
			// ci#652: keep developing while the live consolidation PR is
			// verified; later ready PRs roll into the NEXT consolidation.
			plan.Deferred = ready
			plan.ShouldUpdate = true
			plan.ShouldMerge = policy.RequireGreen && existing.Green
			plan.LinkedIssues = mergeLinkedIssues(existing.LinkedIssues, nil)
			plan.Reason = fmt.Sprintf(
				"consolidation PR #%d is open: refreshing it and deferring %d ready PR(s) to the next consolidation",
				existing.Number, len(ready),
			)
			if len(existing.InformationalChecks) > 0 {
				plan.Reason += fmt.Sprintf(
					"; %d non-required status(es) are red (%s) and never block the merge (ci#683)",
					len(existing.InformationalChecks), strings.Join(existing.InformationalChecks, ", "),
				)
			}
			return plan
		}
		plan.Include = ready
		plan.LinkedIssues = mergeLinkedIssues(existing.LinkedIssues, plan.Include)
		plan.ShouldUpdate = len(ready) > 0
		plan.ShouldMerge = policy.RequireGreen && existing.Green && len(ready) == 0
		plan.Reason = fmt.Sprintf("folding %d ready PR(s) into the open consolidation PR #%d", len(ready), existing.Number)
		return plan
	}

	if len(ready) < policy.Threshold {
		plan.Reason = fmt.Sprintf("%d ready PR(s) below the consolidation threshold of %d", len(ready), policy.Threshold)
		return plan
	}

	plan.Include = ready
	plan.LinkedIssues = mergeLinkedIssues(nil, plan.Include)
	plan.ShouldOpen = true
	plan.Branch = consolidationBranch(policy, now)
	plan.Reason = fmt.Sprintf("opening %s with %d ready PR(s)", plan.Branch, len(ready))
	return plan
}

// consolidationBranch renders `chore/consolidate-<date>` for the given day.
func consolidationBranch(policy AutoConsolidationPolicy, now time.Time) string {
	return fmt.Sprintf("%s-%s", policy.BranchPrefix, now.UTC().Format("2006-01-02"))
}

// IsConsolidationBranch reports whether a head branch is a consolidation
// branch of the policy.
func IsConsolidationBranch(policy AutoConsolidationPolicy, branch string) bool {
	prefix := normalizePolicy(policy).BranchPrefix
	branch = strings.TrimSpace(branch)
	if prefix == "" || branch == "" {
		return false
	}
	return branch == prefix || strings.HasPrefix(branch, prefix+"-") || strings.HasPrefix(branch, prefix+"/")
}

// IsConsolidationPullRequest reports whether a pull request is a consolidation
// PR: its head branch carries the prefix, or it carries the consolidation
// label the external loop controller (ci#652) applies.
func IsConsolidationPullRequest(policy AutoConsolidationPolicy, pr PullRequest) bool {
	if IsConsolidationBranch(policy, pr.HeadRef) {
		return true
	}
	for _, label := range pr.Labels {
		if strings.EqualFold(strings.TrimSpace(label), ConsolidationLabel) {
			return true
		}
	}
	return false
}

// evaluateReady applies the ready-PR selection rules to one pull request.
func evaluateReady(policy AutoConsolidationPolicy, facts ConsolidationFacts, pr PullRequest) ReadyDecision {
	decision := ReadyDecision{Number: pr.Number, Title: pr.Title}
	reject := func(reason string) ReadyDecision {
		decision.Ready = false
		decision.Reasons = append(decision.Reasons, reason)
		return decision
	}

	if pr.Number == 0 {
		return reject("not a pull request")
	}
	if IsConsolidationPullRequest(policy, pr) {
		return reject("is itself a consolidation PR")
	}
	if base := strings.TrimSpace(pr.BaseRef); base != "" && !strings.EqualFold(base, policy.Base) {
		return reject(fmt.Sprintf("targets %s, not %s", base, policy.Base))
	}
	if len(policy.Repos) > 0 && !repoInScope(policy, facts.Repo) {
		return reject(fmt.Sprintf("repository %s is outside the configured scope", facts.Repo))
	}
	if !authorAllowed(policy, facts.SelfLogin, pr.Author) {
		return reject(fmt.Sprintf("author %q is not in the configured authors", pr.Author))
	}
	if pr.Draft {
		return reject("is a draft")
	}
	if !pr.Mergeable {
		return reject("is not mergeable")
	}
	if state := strings.ToUpper(strings.TrimSpace(pr.MergeStateStatus)); state != "" {
		switch state {
		case MergeStateClean, "HAS_HOOKS", "UNSTABLE":
			// UNSTABLE means checks are failing but GitHub still allows a
			// merge; require_green below is what rejects it.
		case MergeStateBlocked:
			// ci#683: GitHub reports `blocked` for a head carrying **any**
			// non-success commit status, required or not — the informational
			// `private-sandbox-tests` status the private suite sets on every PR
			// head made a fully green PR un-mergeable through the CLI. It is a
			// legitimate block when a required context is red, which the
			// require_green gate below rejects; when every required context is
			// green and the only red checks are informational, accept it.
			if !informationalStatusOnlyBlock(pr) {
				return reject(fmt.Sprintf("merge state is %s", state))
			}
		case MergeStateUnknown:
			return reject("mergeability is not known yet")
		default:
			return reject(fmt.Sprintf("merge state is %s", state))
		}
	}
	if policy.RequireGreen && !checksGreen(pr.ChecksState) {
		if strings.TrimSpace(pr.ChecksState) == "" {
			return reject("has no reported checks yet")
		}
		return reject(fmt.Sprintf("checks are %s", pr.ChecksState))
	}
	if policy.RequireIssueLink {
		switch len(pr.LinkedIssues) {
		case 0:
			return reject("has no `Closes msgmate-io/open-chat-go-ci#N` leaf-issue link")
		case 1:
		default:
			return reject(fmt.Sprintf("links %d issues; one leaf issue per PR", len(pr.LinkedIssues)))
		}
	}
	decision.Ready = true
	return decision
}

// checksGreen reports whether a checks state means "all required checks green".
// An empty state is *not* green: the planner never consolidates a PR whose
// checks have not been evaluated (require_green).
func checksGreen(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "success", "green", "passing", "passed", "clean":
		return true
	default:
		return false
	}
}

// informationalStatusOnlyBlock reports whether a `BLOCKED` merge state is fully
// explained by checks the base-branch ruleset does *not* require (ci#683).
//
// GitHub derives `mergeable_state: blocked` from any non-success status on the
// head, so the informational `private-sandbox-tests` commit status blocked the
// agents' merge path although every required context was green and the REST merge
// endpoint succeeded. Being blocked by an informational status is accepted only
// with positive evidence: at least one red check must be known to be
// non-required, and the required checks must be green.
func informationalStatusOnlyBlock(pr PullRequest) bool {
	if len(pr.InformationalChecks) == 0 {
		return false
	}
	state := effectiveChecksState(pr)
	return checksGreen(state)
}

// effectiveChecksState is the state the merge decision is based on: the required
// contexts when they are known, otherwise the aggregate state of every check.
func effectiveChecksState(pr PullRequest) string {
	if pr.RequiredChecksKnown && strings.TrimSpace(pr.RequiredChecksState) != "" {
		return pr.RequiredChecksState
	}
	return pr.ChecksState
}

// authorAllowed applies the `authors` allow-list. `self` matches the agent's
// own login, `*` (or `any`) matches everyone.
func authorAllowed(policy AutoConsolidationPolicy, selfLogin, author string) bool {
	authors := policy.Authors
	if len(authors) == 0 {
		authors = []string{AuthorSelf}
	}
	author = strings.TrimSpace(author)
	for _, allowed := range authors {
		switch strings.ToLower(strings.TrimSpace(allowed)) {
		case AuthorAny, "any", "all":
			return true
		case AuthorSelf:
			if selfLogin != "" && strings.EqualFold(strings.TrimSpace(selfLogin), author) {
				return true
			}
		default:
			if strings.EqualFold(strings.TrimSpace(allowed), author) {
				return true
			}
		}
	}
	return false
}

// repoInScope reports whether a repository is covered by the policy. `scope:
// repo` (the default) covers the repository the facts belong to; an explicit
// `repos` allow-list narrows it further; `scope: any`/`all` covers everything.
func repoInScope(policy AutoConsolidationPolicy, repo string) bool {
	if len(policy.Repos) > 0 {
		repo = strings.TrimSpace(repo)
		for _, allowed := range policy.Repos {
			allowed = strings.TrimSpace(allowed)
			if strings.EqualFold(allowed, repo) {
				return true
			}
			if strings.EqualFold(allowed, "*") {
				return true
			}
			// A bare repository name matches any owner.
			if !strings.Contains(allowed, "/") && strings.EqualFold(allowed, lastPathSegment(repo)) {
				return true
			}
		}
		return false
	}
	switch policy.Scope {
	case "any", "all", "*":
		return true
	default:
		return true
	}
}

func lastPathSegment(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	return parts[len(parts)-1]
}

// selectLiveConsolidation picks the single consolidation PR that stays open:
// the oldest one when several exist.
func selectLiveConsolidation(policy AutoConsolidationPolicy, open []ExistingConsolidation) *ExistingConsolidation {
	candidates := make([]ExistingConsolidation, 0, len(open))
	for _, item := range open {
		if IsConsolidationBranch(policy, item.Branch) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].Number < candidates[j].Number
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	live := candidates[0]
	return &live
}

// supersededConsolidations returns the extra open consolidation PRs that must be
// absorbed or closed so exactly one stays open (one_open_at_a_time).
func supersededConsolidations(policy AutoConsolidationPolicy, open []ExistingConsolidation, live *ExistingConsolidation) []ExistingConsolidation {
	if !policy.OneOpenAtATime || live == nil {
		return nil
	}
	out := make([]ExistingConsolidation, 0, len(open))
	for _, item := range open {
		if !IsConsolidationBranch(policy, item.Branch) {
			continue
		}
		if item.Number == live.Number {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// ConsolidationLabels returns the label set a consolidation PR carries.
func (p ConsolidationPlan) ConsolidationLabels() []string {
	return append([]string(nil), p.Labels...)
}
