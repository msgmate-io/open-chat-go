package nativecoder

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeConsolidationClient is an in-memory ConsolidationClient for the runner
// tests of ci#653: it records every action so a test can assert that exactly one
// consolidation PR is opened/refreshed, labelled, approved and merged, and that
// later ready PRs are deferred to the NEXT consolidation (ci#652).
type fakeConsolidationClient struct {
	mu sync.Mutex

	selfLogin string
	openPRs   []PullRequest

	created    bool
	prNumber   int
	nextNumber int
	opened     int
	updated    int
	labels     [][]string
	comments   []string
	approved   []int
	merged     []int
	rollups    []string
}

func newFakeConsolidationClient(selfLogin string, openPRs []PullRequest) *fakeConsolidationClient {
	client := &fakeConsolidationClient{selfLogin: selfLogin, openPRs: openPRs, nextNumber: 900}
	// Seed the already-open consolidation, if any, so a refresh does not look
	// like a new PR to the assertions.
	for _, pr := range openPRs {
		if IsConsolidationPullRequest(DefaultAutoConsolidationPolicy(), pr) {
			client.created = true
			client.prNumber = pr.Number
		}
	}
	return client
}

func (f *fakeConsolidationClient) SelfLogin(context.Context) (string, error) { return f.selfLogin, nil }

func (f *fakeConsolidationClient) ListOpenPullRequests(context.Context) ([]PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PullRequest(nil), f.openPRs...), nil
}

func (f *fakeConsolidationClient) ListOpenConsolidations(_ context.Context, prefix string) ([]ExistingConsolidation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []ExistingConsolidation{}
	for _, pr := range f.openPRs {
		if IsConsolidationPullRequest(AutoConsolidationPolicy{BranchPrefix: prefix}, pr) {
			out = append(out, ExistingConsolidation{
				Number:    pr.Number,
				Branch:    pr.HeadRef,
				Green:     checksGreen(pr.ChecksState),
				CreatedAt: pr.CreatedAt,
			})
		}
	}
	return out, nil
}

func (f *fakeConsolidationClient) EnsureConsolidationPullRequest(_ context.Context, branch, base, title, body string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created {
		f.updated++
		return f.prNumber, false, nil
	}
	f.created = true
	f.opened++
	f.prNumber = f.nextNumber
	f.nextNumber++
	return f.prNumber, true, nil
}

func (f *fakeConsolidationClient) CommentOnPullRequest(_ context.Context, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, fmt.Sprintf("%d:%s", number, body))
	return nil
}

func (f *fakeConsolidationClient) EnsureLabels(_ context.Context, number int, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels = append(f.labels, append([]string(nil), labels...))
	return nil
}

func (f *fakeConsolidationClient) RequestAutoApprove(_ context.Context, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved = append(f.approved, number)
	return nil
}

func (f *fakeConsolidationClient) MergePullRequest(_ context.Context, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merged = append(f.merged, number)
	return nil
}

func (f *fakeConsolidationClient) EnsureRollupIssue(_ context.Context, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollups = append(f.rollups, title)
	return nil
}

func readyPR(number int) PullRequest {
	return PullRequest{
		Number:           number,
		Title:            fmt.Sprintf("feat: ready #%d", number),
		Author:           "cur1ousdude",
		BaseRef:          "main",
		Mergeable:        true,
		MergeStateStatus: MergeStateClean,
		ChecksState:      "success",
	}
}

// TestRunnerOpensExactlyOneConsolidationAndLabelsIt covers open -> labels ->
// roll-up: with no consolidation open, one PR is opened, the standard label set
// is applied, and nothing is merged before it is green.
func TestRunnerOpensExactlyOneConsolidationAndLabelsIt(t *testing.T) {
	client := newFakeConsolidationClient("cur1ousdude", []PullRequest{readyPR(11), readyPR(12)})
	runner := ConsolidationRunner{
		Client: client,
		Now:    func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) },
		Logf:   func(string, ...interface{}) {},
	}

	result, err := runner.Run(context.Background(), DefaultAutoConsolidationPolicy(), "msgmate-io/open-chat-go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !result.Ran || !result.Created {
		t.Fatalf("expected a consolidation PR to be created, got %#v", result)
	}
	if client.opened != 1 {
		t.Fatalf("expected exactly one consolidation PR to be opened, got %d", client.opened)
	}
	if result.Plan.Branch != "chore/consolidate-2026-10-18" {
		t.Fatalf("branch = %q, want chore/consolidate-2026-10-18", result.Plan.Branch)
	}
	if len(result.Labels) != len(DefaultAutoConsolidationLabels) {
		t.Fatalf("expected the standard labels to be applied, got %v", result.Labels)
	}
	if len(client.merged) != 0 || len(client.approved) != 0 {
		t.Fatalf("a freshly opened PR must not be merged: approved=%v merged=%v", client.approved, client.merged)
	}
	if len(client.rollups) != 1 {
		t.Fatalf("expected the roll-up issue to be maintained, got %v", client.rollups)
	}
}

// TestRunnerDefersWhileAConsolidationIsOpen is the one-open-at-a-time rolling
// test of ci#653 (ci#652): while a consolidation PR is open, a newly ready PR
// rolls into the NEXT consolidation and no second PR is opened.
func TestRunnerDefersWhileAConsolidationIsOpen(t *testing.T) {
	open := PullRequest{
		Number:      500,
		Title:       "chore(consolidate): earlier batch",
		Author:      "cur1ousdude",
		HeadRef:     "chore/consolidate-2026-10-17",
		BaseRef:     "main",
		Mergeable:   true,
		ChecksState: "pending",
		CreatedAt:   time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
	}
	client := newFakeConsolidationClient("cur1ousdude", []PullRequest{open, readyPR(21)})
	runner := ConsolidationRunner{
		Client: client,
		Now:    func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) },
		Logf:   func(string, ...interface{}) {},
	}

	result, err := runner.Run(context.Background(), DefaultAutoConsolidationPolicy(), "msgmate-io/open-chat-go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if client.opened != 0 {
		t.Fatalf("a second consolidation PR must never be opened, got %d", client.opened)
	}
	if result.Plan.ShouldOpen {
		t.Fatalf("plan must not open a new consolidation: %#v", result.Plan)
	}
	if len(result.Plan.Deferred) != 1 || result.Plan.Deferred[0].Number != 21 {
		t.Fatalf("expected PR #21 to roll into the NEXT consolidation, got %#v", result.Plan.Deferred)
	}
	if result.Plan.Existing == nil || result.Plan.Existing.Number != 500 {
		t.Fatalf("expected PR #500 to stay the live consolidation, got %#v", result.Plan.Existing)
	}
	if len(client.comments) != 1 {
		t.Fatalf("expected one deferral comment on the live PR, got %v", client.comments)
	}
	if result.Merged {
		t.Fatalf("a not-green consolidation must never be merged")
	}
}

// TestRunnerMergesOneGreenConsolidation covers the green -> auto-approve ->
// merge path (ci#538): once the single open consolidation PR is green it is
// approved and merged, and no new PR is opened.
func TestRunnerMergesOneGreenConsolidation(t *testing.T) {
	green := PullRequest{
		Number:      600,
		Title:       "chore(consolidate): ready",
		Author:      "cur1ousdude",
		HeadRef:     "chore/consolidate-2026-10-18",
		BaseRef:     "main",
		Mergeable:   true,
		ChecksState: "success",
		CreatedAt:   time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC),
	}
	client := newFakeConsolidationClient("cur1ousdude", []PullRequest{green})
	runner := ConsolidationRunner{
		Client: client,
		Now:    func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) },
		Logf:   func(string, ...interface{}) {},
	}

	result, err := runner.Run(context.Background(), DefaultAutoConsolidationPolicy(), "msgmate-io/open-chat-go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if client.opened != 0 {
		t.Fatalf("a green consolidation must be merged, not superseded: opened=%d", client.opened)
	}
	if len(client.approved) != 1 || client.approved[0] != 600 {
		t.Fatalf("expected the ci#538 auto-approve for PR #600, got %v", client.approved)
	}
	if len(client.merged) != 1 || client.merged[0] != 600 {
		t.Fatalf("expected PR #600 to be merged, got %v", client.merged)
	}
	if !result.Merged {
		t.Fatalf("expected the run result to report the merge")
	}
}

// TestRunnerDisabledIsANoOp guards the feature toggle: a disabled policy never
// touches GitHub.
func TestRunnerDisabledIsANoOp(t *testing.T) {
	client := newFakeConsolidationClient("cur1ousdude", []PullRequest{readyPR(31)})
	runner := ConsolidationRunner{Client: client, Logf: func(string, ...interface{}) {}}

	policy := DefaultAutoConsolidationPolicy()
	policy.Enabled = false

	result, err := runner.Run(context.Background(), policy, "msgmate-io/open-chat-go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Ran || result.Skipped == "" {
		t.Fatalf("expected a skipped no-op run, got %#v", result)
	}
	if client.opened != 0 || len(client.labels) != 0 || len(client.merged) != 0 {
		t.Fatalf("a disabled run must not act: %#v", client)
	}
}

// TestRunnerNoClientIsSkipped guards the deployment without a GitHub token.
func TestRunnerNoClientIsSkipped(t *testing.T) {
	runner := ConsolidationRunner{Logf: func(string, ...interface{}) {}}
	result, err := runner.Run(context.Background(), DefaultAutoConsolidationPolicy(), "msgmate-io/open-chat-go")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Ran || result.Skipped == "" {
		t.Fatalf("expected a skipped run without a client, got %#v", result)
	}
}
