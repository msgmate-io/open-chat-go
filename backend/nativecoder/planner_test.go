package nativecoder

import (
	"strings"
	"testing"
	"time"
)

// TestReadyPullRequestSelection is the ready-PR selection test of ci#653: only
// green, non-draft, mergeable PRs targeting `main` from the agent's own
// identity are consolidated; everything else is rejected with a reason.
func TestReadyPullRequestSelection(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()

	cases := []struct {
		name   string
		pr     PullRequest
		ready  bool
		reason string
	}{
		{
			name:  "green non-draft mergeable PR targeting main is ready",
			pr:    PullRequest{Number: 101, Title: "feat: ready", Author: "cur1ousdude", BaseRef: "main", Mergeable: true, MergeStateStatus: MergeStateClean, ChecksState: "success"},
			ready: true,
		},
		{
			name:   "draft is never consolidated",
			pr:     PullRequest{Number: 102, Author: "cur1ousdude", BaseRef: "main", Draft: true, Mergeable: true, ChecksState: "success"},
			reason: "draft",
		},
		{
			name:   "failing checks are never consolidated",
			pr:     PullRequest{Number: 103, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "failure"},
			reason: "checks",
		},
		{
			name:   "checks that have not run yet are not green",
			pr:     PullRequest{Number: 104, Author: "cur1ousdude", BaseRef: "main", Mergeable: true},
			reason: "no reported checks",
		},
		{
			name:   "a PR targeting another base is out of scope",
			pr:     PullRequest{Number: 105, Author: "cur1ousdude", BaseRef: "release/1", Mergeable: true, ChecksState: "success"},
			reason: "targets release/1",
		},
		{
			name:   "a non-mergeable (conflicting) PR is excluded",
			pr:     PullRequest{Number: 106, Author: "cur1ousdude", BaseRef: "main", Mergeable: false, ChecksState: "success"},
			reason: "not mergeable",
		},
		{
			name:   "unknown mergeability waits for the next pass",
			pr:     PullRequest{Number: 107, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, MergeStateStatus: MergeStateUnknown, ChecksState: "success"},
			reason: "mergeability is not known",
		},
		{
			name:   "another author's PR is out of scope (authors: [self])",
			pr:     PullRequest{Number: 108, Author: "someone-else", BaseRef: "main", Mergeable: true, ChecksState: "success"},
			reason: "author",
		},
		{
			name:   "a consolidation PR is never folded into itself",
			pr:     PullRequest{Number: 109, Author: "cur1ousdude", HeadRef: "chore/consolidate-2026-10-18", BaseRef: "main", Mergeable: true, ChecksState: "success"},
			reason: "consolidation PR",
		},
		{
			name:   "a labelled consolidation PR is recognised too",
			pr:     PullRequest{Number: 110, Author: "cur1ousdude", HeadRef: "chore/batch", Labels: []string{"consolidation"}, BaseRef: "main", Mergeable: true, ChecksState: "success"},
			reason: "consolidation PR",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := Plan(policy, ConsolidationFacts{
				Repo:      "msgmate-io/open-chat-go",
				SelfLogin: "cur1ousdude",
				OpenPRs:   []PullRequest{tc.pr},
			})

			var decision *ReadyDecision
			for idx := range plan.Decisions {
				if plan.Decisions[idx].Number == tc.pr.Number {
					decision = &plan.Decisions[idx]
				}
			}
			if decision == nil {
				t.Fatalf("expected a decision for PR #%d", tc.pr.Number)
			}
			if decision.Ready != tc.ready {
				t.Fatalf("PR #%d ready = %v, want %v (reasons: %v)", tc.pr.Number, decision.Ready, tc.ready, decision.Reasons)
			}
			if tc.reason != "" {
				joined := strings.Join(decision.Reasons, "; ")
				if !strings.Contains(joined, tc.reason) {
					t.Fatalf("PR #%d reasons = %q, want it to contain %q", tc.pr.Number, joined, tc.reason)
				}
			}
		})
	}
}

// TestPlanOpensSingleConsolidationAtThreshold verifies the threshold and the
// single-PR output of the default policy (threshold 1, one_open_at_a_time).
func TestPlanOpensSingleConsolidationAtThreshold(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		Now:       now,
		OpenPRs: []PullRequest{
			{Number: 11, Title: "fix: a", Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
			{Number: 12, Title: "fix: b", Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
	})

	if !plan.ShouldOpen {
		t.Fatalf("expected ShouldOpen, got %#v", plan)
	}
	if plan.Branch != "chore/consolidate-2026-10-18" {
		t.Fatalf("branch = %q, want chore/consolidate-2026-10-18", plan.Branch)
	}
	if len(plan.Include) != 2 {
		t.Fatalf("expected both ready PRs folded in, got %d", len(plan.Include))
	}
	if len(plan.Deferred) != 0 {
		t.Fatalf("expected nothing deferred when no consolidation is open, got %d", len(plan.Deferred))
	}
	if len(plan.Labels) != len(DefaultAutoConsolidationLabels) {
		t.Fatalf("expected the standard label set, got %v", plan.Labels)
	}
	if plan.ShouldMerge {
		t.Fatalf("must not merge a PR that was just opened")
	}
}

// TestPlanThresholdDefersBelowMinimum guards the configurable threshold.
func TestPlanThresholdDefersBelowMinimum(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	policy.Threshold = 3

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		OpenPRs: []PullRequest{
			{Number: 11, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
			{Number: 12, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
	})

	if plan.ShouldOpen {
		t.Fatalf("expected no consolidation below the threshold: %#v", plan)
	}
	if !strings.Contains(plan.Reason, "threshold") {
		t.Fatalf("expected the reason to mention the threshold, got %q", plan.Reason)
	}
}

// TestPlanDefersWhenConsolidationOpen is the one-open-at-a-time rolling
// behaviour test of ci#653 (ci#652): while a consolidation PR is open, newly
// ready PRs roll into the NEXT consolidation and the open one is refreshed (and
// merged once green).
func TestPlanDefersWhenConsolidationOpen(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	facts := ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		Now:       now,
		OpenPRs: []PullRequest{
			{Number: 21, Title: "feat: later", Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
		OpenConsolidations: []ExistingConsolidation{
			{Number: 500, Branch: "chore/consolidate-2026-10-17", Green: false, CreatedAt: now.Add(-24 * time.Hour)},
		},
	}

	plan := Plan(policy, facts)
	if plan.ShouldOpen {
		t.Fatalf("a second consolidation PR must never be opened: %#v", plan)
	}
	if plan.Existing == nil || plan.Existing.Number != 500 {
		t.Fatalf("expected the open consolidation PR #500 to be the live one, got %#v", plan.Existing)
	}
	if len(plan.Include) != 0 {
		t.Fatalf("the live consolidation PR must not be re-created with new contents, got %d include(s)", len(plan.Include))
	}
	if len(plan.Deferred) != 1 || plan.Deferred[0].Number != 21 {
		t.Fatalf("expected the newly ready PR to roll into the NEXT consolidation, got %#v", plan.Deferred)
	}
	if !plan.ShouldUpdate {
		t.Fatalf("expected the open consolidation PR to be refreshed")
	}
	if plan.ShouldMerge {
		t.Fatalf("a not-green consolidation PR must never be merged (require_green)")
	}

	// Once every required check is green, the live consolidation PR is merged
	// through the existing auto-approve path (ci#538) and the deferred PR waits
	// for the next pass.
	facts.OpenConsolidations[0].Green = true
	greenPlan := Plan(policy, facts)
	if !greenPlan.ShouldMerge {
		t.Fatalf("expected a green consolidation PR to be merged, got %#v", greenPlan)
	}
	if greenPlan.ShouldOpen {
		t.Fatalf("a green consolidation PR must be merged, not superseded by a new one")
	}
}

// TestPlanSupersedesExtraConsolidations verifies that exactly one consolidation
// PR stays open even if an older run left more than one behind.
func TestPlanSupersedesExtraConsolidations(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		Now:       now,
		OpenPRs: []PullRequest{
			{Number: 31, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
		OpenConsolidations: []ExistingConsolidation{
			{Number: 700, Branch: "chore/consolidate-2026-10-18", CreatedAt: now.Add(-time.Hour)},
			{Number: 690, Branch: "chore/consolidate-2026-10-16", CreatedAt: now.Add(-48 * time.Hour)},
		},
	})

	if plan.Existing == nil || plan.Existing.Number != 690 {
		t.Fatalf("expected the oldest consolidation PR to stay live, got %#v", plan.Existing)
	}
	if len(plan.Superseded) != 1 || plan.Superseded[0].Number != 700 {
		t.Fatalf("expected the extra consolidation PR to be superseded, got %#v", plan.Superseded)
	}
	if len(plan.Deferred) != 1 {
		t.Fatalf("expected the ready PR to roll into the next consolidation, got %d", len(plan.Deferred))
	}
}

// TestPlanRespectsDisabledToggle covers the feature toggle (and the rollback
// path): a disabled policy performs no work at all.
func TestPlanRespectsDisabledToggle(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	policy.Enabled = false

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		OpenPRs: []PullRequest{
			{Number: 41, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
	})

	if plan.ShouldOpen || plan.ShouldUpdate || plan.ShouldMerge {
		t.Fatalf("a disabled policy must not act: %#v", plan)
	}
	if !strings.Contains(plan.Reason, "disabled") {
		t.Fatalf("expected the disabled reason, got %q", plan.Reason)
	}
}

// TestPlanRespectsRepoScope guards the `scope: repo` default and the optional
// repo allow-list.
func TestPlanRespectsRepoScope(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	policy.Repos = []string{"msgmate-io/open-chat-go-ci"}

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		OpenPRs: []PullRequest{
			{Number: 51, Author: "cur1ousdude", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
	})
	if plan.ShouldOpen {
		t.Fatalf("a repository outside the allow-list must not be consolidated: %#v", plan)
	}
	if !strings.Contains(plan.Reason, "scope") {
		t.Fatalf("expected a scope reason, got %q", plan.Reason)
	}
}

// TestIsConsolidationBranch guards the branch/label recognition shared with the
// external loop controller (ci#652) and the ci#538 auto-approve workflow.
func TestIsConsolidationBranch(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	for _, branch := range []string{"chore/consolidate-2026-10-18", "chore/consolidate-fix", "chore/consolidate"} {
		if !IsConsolidationBranch(policy, branch) {
			t.Fatalf("expected %q to be a consolidation branch", branch)
		}
	}
	for _, branch := range []string{"", "feat/ci-653", "chore/consolidatex"} {
		if IsConsolidationBranch(policy, branch) {
			t.Fatalf("expected %q not to be a consolidation branch", branch)
		}
	}
}

// TestAuthorAnyWidensTheScope guards the `authors: ["*"]` escape hatch.
func TestAuthorAnyWidensTheScope(t *testing.T) {
	policy := DefaultAutoConsolidationPolicy()
	policy.Authors = []string{AuthorAny}

	plan := Plan(policy, ConsolidationFacts{
		Repo:      "msgmate-io/open-chat-go",
		SelfLogin: "cur1ousdude",
		OpenPRs: []PullRequest{
			{Number: 61, Author: "someone-else", BaseRef: "main", Mergeable: true, ChecksState: "success"},
		},
	})
	if !plan.ShouldOpen {
		t.Fatalf("authors: [*] must accept any author: %#v", plan)
	}
}
