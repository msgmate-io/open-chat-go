package nativecoder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend/logging"
)

// This file implements the auto-consolidation runner (ci#653): it turns a
// planning pass into actions — open/refresh exactly one `chore/consolidate-<date>`
// PR, apply the standard labels, request the ci#538 auto-approval and merge once
// every required check is green, and keep the tracker issue (ci#415) updated.
//
// The runner is deliberately conservative:
//
//   - it is a no-op when the policy is disabled;
//   - it never merges a PR whose checks are not green (require_green);
//   - it keeps exactly one consolidation PR open (one_open_at_a_time): later
//     ready PRs are recorded as "next consolidation" (ci#652);
//   - every error is returned, never swallowed, so the caller can log it and
//     leave the loop to the next turn.

// BranchPreparer prepares the consolidation head branch (git side) so the PR can
// be opened: it creates/updates `branch` off `base` and folds `include` in. It is
// optional — when nil, the runner only manages a branch that already exists (eg
// pushed by the consolidation agent itself).
type BranchPreparer func(ctx context.Context, branch, base string, include []PullRequest) error

// ConsolidationDispatcher hands the plan to a native consolidation agent (the
// agent folds the ready PRs in, resolves conflicts and fixes test failures). It
// is optional: without it the runner still opens/refreshes the PR and keeps the
// roll-up issue, which is enough for a branch that is prepared elsewhere.
type ConsolidationDispatcher func(ctx context.Context, plan ConsolidationPlan) error

// ConsolidationRunner executes consolidation plans.
type ConsolidationRunner struct {
	Client ConsolidationClient
	// PrepareBranch creates/refreshes the consolidation branch (optional).
	PrepareBranch BranchPreparer
	// Dispatch hands the plan to a consolidation agent (optional).
	Dispatch ConsolidationDispatcher
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logf receives progress lines (defaults to the standard logger).
	Logf func(format string, args ...interface{})
}

// ConsolidationRunResult is the outcome of one run, suitable for logging and for
// the roll-up issue comment.
type ConsolidationRunResult struct {
	Ran         bool              `json:"ran"`
	Skipped     string            `json:"skipped,omitempty"`
	Plan        ConsolidationPlan `json:"plan"`
	SelfLogin   string            `json:"self_login,omitempty"`
	PullRequest int               `json:"pull_request,omitempty"`
	Created     bool              `json:"created,omitempty"`
	Merged      bool              `json:"merged,omitempty"`
	Dispatched  bool              `json:"dispatched,omitempty"`
	Labels      []string          `json:"labels,omitempty"`
	// InformationalChecks names red checks/statuses the base-branch ruleset does
	// not require that the merged consolidation PR carried (ci#683).
	InformationalChecks []string `json:"informational_checks,omitempty"`
	// ClosedIssues are the leaf issues closed after the consolidation merged
	// (ci#813).
	ClosedIssues []int `json:"closed_issues,omitempty"`
}

// Run performs one consolidation pass for the given policy and repository.
func (r ConsolidationRunner) Run(ctx context.Context, policy AutoConsolidationPolicy, repo string) (ConsolidationRunResult, error) {
	policy = normalizePolicy(policy)
	result := ConsolidationRunResult{Plan: ConsolidationPlan{Enabled: policy.Enabled}}
	if !policy.Enabled {
		result.Skipped = "auto consolidation disabled for this agent"
		return result, nil
	}
	if r.Client == nil {
		result.Skipped = "no GitHub client configured"
		return result, nil
	}

	self, err := resolveSelfLogin(ctx, r.Client)
	if err != nil {
		return result, fmt.Errorf("resolve self login: %w", err)
	}
	result.SelfLogin = self

	openPRs, err := r.Client.ListOpenPullRequests(ctx)
	if err != nil {
		return result, fmt.Errorf("list open pull requests: %w", err)
	}
	openConsolidations, err := r.Client.ListOpenConsolidations(ctx, policy.BranchPrefix)
	if err != nil {
		return result, fmt.Errorf("list open consolidation pull requests: %w", err)
	}

	plan := Plan(policy, ConsolidationFacts{
		Repo:               repo,
		SelfLogin:          self,
		OpenPRs:            openPRs,
		OpenConsolidations: openConsolidations,
		Now:                r.now(),
	})
	result.Plan = plan
	result.Ran = true
	r.logf("auto consolidation: %s", plan.Reason)

	if plan.ShouldOpen {
		if r.Dispatch != nil {
			if err := r.Dispatch(ctx, plan); err != nil {
				return result, fmt.Errorf("dispatch consolidation agent: %w", err)
			}
			result.Dispatched = true
		}
		if r.PrepareBranch != nil {
			if err := r.PrepareBranch(ctx, plan.Branch, plan.Base, plan.Include); err != nil {
				return result, fmt.Errorf("prepare consolidation branch %s: %w", plan.Branch, err)
			}
		}
		number, created, err := r.Client.EnsureConsolidationPullRequest(
			ctx, plan.Branch, plan.Base, consolidationTitle(plan), consolidationBody(plan),
		)
		if err != nil {
			return result, fmt.Errorf("open consolidation pull request: %w", err)
		}
		result.PullRequest = number
		result.Created = created
		if err := r.applyLabels(ctx, policy, number, &result); err != nil {
			return result, err
		}
	} else if plan.ShouldUpdate && plan.Existing != nil {
		// Refresh the open consolidation PR's description so the live state (and
		// the PRs deferred to the NEXT consolidation, ci#652) is reviewable.
		number, _, err := r.Client.EnsureConsolidationPullRequest(
			ctx, plan.Existing.Branch, plan.Base, consolidationTitle(plan), consolidationBody(plan),
		)
		if err != nil {
			return result, fmt.Errorf("refresh consolidation pull request: %w", err)
		}
		result.PullRequest = number
		if err := r.applyLabels(ctx, policy, number, &result); err != nil {
			return result, err
		}
		if len(plan.Deferred) > 0 {
			if err := r.Client.CommentOnPullRequest(ctx, number, deferredComment(plan.Deferred)); err != nil {
				return result, fmt.Errorf("comment deferred pull requests: %w", err)
			}
		}
	}

	if plan.RollupIssue {
		if err := r.Client.EnsureRollupIssue(ctx, "Consolidation tracker", rollupIssueBody(plan)); err != nil {
			return result, fmt.Errorf("update consolidation tracker issue: %w", err)
		}
	}

	if plan.ShouldMerge && plan.Existing != nil {
		// ci#538: request the auto-approval first (idempotent), then merge. Both
		// only run for a green consolidation PR because require_green gates
		// ShouldMerge.
		//
		// ci#683: a red *non-required* status (the informational
		// `private-sandbox-tests` commit status) must not be mistaken for a
		// base-branch-policy veto: the merge is gated on the ruleset's required
		// contexts, and the informational ones are reported instead.
		if len(plan.Existing.InformationalChecks) > 0 {
			r.logf(
				"auto consolidation: #%d carries %d red non-required status(es) the base ruleset does not require (%s); they are informational and never block the merge (ci#683)",
				plan.Existing.Number, len(plan.Existing.InformationalChecks),
				strings.Join(plan.Existing.InformationalChecks, ", "),
			)
			result.InformationalChecks = append([]string(nil), plan.Existing.InformationalChecks...)
		}
		if err := r.Client.RequestAutoApprove(ctx, plan.Existing.Number); err != nil {
			return result, fmt.Errorf("request auto approval for #%d: %w", plan.Existing.Number, err)
		}
		// A PR just approved cannot merge in the same pass (the review lands
		// asynchronously); the merge is retried on the next pass once GitHub
		// reports the PR mergeable again.
		if err := r.Client.MergePullRequest(ctx, plan.Existing.Number); err != nil {
			r.logf(
				"auto consolidation: merge of #%d pending: %v (every required context is green; a red non-required status such as `private-sandbox-tests` is informational, ci#683)",
				plan.Existing.Number, err,
			)
		} else {
			result.Merged = true
			r.closeLinkedIssues(ctx, plan, &result)
		}
	}

	return result, nil
}

// issueCloser is the optional capability that lets the runner close the leaf
// issues a merged consolidation PR named (ci#813). Clients without it still
// consolidate; the caller/human then closes the issues.
type issueCloser interface {
	CloseIssue(ctx context.Context, number int, comment string) error
}

// closeLinkedIssues closes every leaf issue the merged consolidation PR named.
// A failure is logged and does not undo the merge: the PR is already on main,
// and the issue can be closed manually from the transcript.
func (r ConsolidationRunner) closeLinkedIssues(ctx context.Context, plan ConsolidationPlan, result *ConsolidationRunResult) {
	if len(plan.LinkedIssues) == 0 {
		return
	}
	closer, ok := r.Client.(issueCloser)
	if !ok {
		r.logf("auto consolidation: client cannot close the %d linked issue(s); close them manually", len(plan.LinkedIssues))
		return
	}
	for _, number := range plan.LinkedIssues {
		comment := fmt.Sprintf("Closed by consolidation PR #%d (merged into `%s`).", plan.Existing.Number, plan.Base)
		if err := closer.CloseIssue(ctx, number, comment); err != nil {
			r.logf("auto consolidation: close `%s#%d` after merging #%d: %v", LinkedIssueRepo, number, plan.Existing.Number, err)
			continue
		}
		result.ClosedIssues = append(result.ClosedIssues, number)
	}
}

// applyLabels ensures the standard label set is present on the consolidation PR.
func (r ConsolidationRunner) applyLabels(ctx context.Context, policy AutoConsolidationPolicy, number int, result *ConsolidationRunResult) error {
	labels := policy.Labels
	if len(labels) == 0 {
		return nil
	}
	applier, ok := r.Client.(interface {
		EnsureLabels(ctx context.Context, number int, labels []string) error
	})
	if !ok {
		// A client without label support still consolidates; the caller can
		// apply labels itself.
		return nil
	}
	if err := applier.EnsureLabels(ctx, number, labels); err != nil {
		return fmt.Errorf("apply consolidation labels to #%d: %w", number, err)
	}
	result.Labels = append([]string(nil), labels...)
	return nil
}

func (r ConsolidationRunner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r ConsolidationRunner) logf(format string, args ...interface{}) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	logging.Debugf(format, args...)
}

// consolidationTitle renders the consolidation PR title for a plan.
func consolidationTitle(plan ConsolidationPlan) string {
	return fmt.Sprintf("chore(consolidate): fold %d ready PR(s) into %s", len(plan.Include), plan.Base)
}

// consolidationBody renders the consolidation PR description: what is folded in,
// what rolls into the NEXT consolidation (ci#652) and the label/verification
// policy (ci#653).
func consolidationBody(plan ConsolidationPlan) string {
	var builder strings.Builder
	builder.WriteString("Automated consolidation PR (ci#653).\n\n")
	builder.WriteString("This is the single batch-verification PR against `")
	builder.WriteString(plan.Base)
	builder.WriteString("`; the individual feature PRs are folded in here and verified as a whole.\n\n")

	builder.WriteString("### Folded in\n\n")
	if len(plan.Include) == 0 {
		builder.WriteString("_none yet_\n\n")
	} else {
		for _, pr := range plan.Include {
			fmt.Fprintf(&builder, "- #%d %s\n", pr.Number, strings.TrimSpace(pr.Title))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("### Closes\n\n")
	if len(plan.LinkedIssues) == 0 {
		builder.WriteString("_no linked leaf issue (the merge gate rejects this)_\n\n")
	} else {
		for _, number := range plan.LinkedIssues {
			builder.WriteString(LinkedIssueLine(number))
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	if len(plan.Deferred) > 0 {
		builder.WriteString("### Rolls into the NEXT consolidation (ci#652)\n\n")
		for _, pr := range plan.Deferred {
			fmt.Fprintf(&builder, "- #%d %s\n", pr.Number, strings.TrimSpace(pr.Title))
		}
		builder.WriteString("\n")
	}

	builder.WriteString("### Verification\n\n")
	builder.WriteString("The standard label set (`test`, `build`, `e2e`, `test-core-only`, `ai-test-mock`, `ai-test-real`, `sandbox-test`, `deploy`) is applied, so this PR runs the full suite.\n")
	builder.WriteString("It is merged through the existing auto-approve path (ci#538) once every required check is green; `require_green` never includes a failing, conflicting or draft PR.\n\n")
	builder.WriteString("The merge is gated on the **required** contexts of the `main` ruleset. A red *non-required* status — typically the informational `private-sandbox-tests` commit status the private suite sets on a PR head — is reported as informational and never blocks the merge (ci#683).\n\n")
	builder.WriteString("`deploy` and `sandbox-test` are used only on consolidation PRs targeting `main` (DEVELOPMENT.md, \"Pull-request CI labels and deployment policy\").\n")
	return builder.String()
}

// deferredComment records the PRs that roll into the NEXT consolidation.
func deferredComment(deferred []PullRequest) string {
	var builder strings.Builder
	builder.WriteString("Rolled into the NEXT consolidation (ci#652): this PR is the single open consolidation, so these ready PRs are deferred.\n\n")
	for _, pr := range deferred {
		fmt.Fprintf(&builder, "- #%d %s\n", pr.Number, strings.TrimSpace(pr.Title))
	}
	return builder.String()
}

// rollupIssueBody renders the consolidation tracker issue body (ci#415).
func rollupIssueBody(plan ConsolidationPlan) string {
	var builder strings.Builder
	builder.WriteString("Consolidation tracker (ci#415, maintained by auto consolidation ci#653).\n\n")
	fmt.Fprintf(&builder, "- repository: `%s`\n", plan.Repo)
	fmt.Fprintf(&builder, "- base: `%s`\n", plan.Base)
	if plan.Existing != nil {
		fmt.Fprintf(&builder, "- open consolidation PR: #%d\n", plan.Existing.Number)
	} else if plan.Branch != "" {
		fmt.Fprintf(&builder, "- new consolidation PR branch: `%s`\n", plan.Branch)
	} else {
		builder.WriteString("- open consolidation PR: _none_\n")
	}
	fmt.Fprintf(&builder, "- status: %s\n\n", plan.Reason)
	if len(plan.Include) > 0 {
		builder.WriteString("Folded in:\n\n")
		for _, pr := range plan.Include {
			fmt.Fprintf(&builder, "- #%d %s\n", pr.Number, strings.TrimSpace(pr.Title))
		}
		builder.WriteString("\n")
	}
	if len(plan.Deferred) > 0 {
		builder.WriteString("Next consolidation (ci#652):\n\n")
		for _, pr := range plan.Deferred {
			fmt.Fprintf(&builder, "- #%d %s\n", pr.Number, strings.TrimSpace(pr.Title))
		}
	}
	return builder.String()
}
