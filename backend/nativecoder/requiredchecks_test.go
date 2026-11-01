package nativecoder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// requiredChecksServer stubs the three endpoints the required-check resolution
// reads: the branch ruleset, the commit's check-runs and its legacy statuses.
func requiredChecksServer(t *testing.T, rulesJSON, checkRunsJSON, statusJSON string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/o/n/rules/branches/"):
			if rulesJSON == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			_, _ = w.Write([]byte(rulesJSON))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(checkRunsJSON))
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(statusJSON))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testClient(server *httptest.Server) *GitHubAPIClient {
	return &GitHubAPIClient{BaseURL: server.URL, Repo: "o/n", Token: "test", HTTP: server.Client()}
}

const requiredRulesJSON = `[{"type":"pull_request","parameters":{}},
	{"type":"required_status_checks","parameters":{"strict_required_status_check_policy":true,
	"required_status_checks":[{"context":"Run PR CI Tests and Deployments","integration_id":15368}]}}]`

// TestRequiredStatusChecksReadsTheRuleset guards the ruleset parsing: only the
// `required_status_checks` rule contributes contexts, de-duplicated.
func TestRequiredStatusChecksReadsTheRuleset(t *testing.T) {
	server := requiredChecksServer(t, requiredRulesJSON, `{"check_runs":[]}`, `{"state":"success","statuses":[]}`)
	checks, err := testClient(server).RequiredStatusChecks(t.Context(), "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 1 || checks[0].Context != "Run PR CI Tests and Deployments" || checks[0].IntegrationID != 15368 {
		t.Fatalf("required checks = %#v", checks)
	}
}

// TestBranchCheckStatesSeparatesRequiredFromInformational is the ci#683 core
// case: the head carries a red informational legacy commit status
// (`private-sandbox-tests`) and red non-required check runs, while the single
// *required* context is green. The required state must be success and the red
// contexts must be reported as informational — not as a failure.
func TestBranchCheckStatesSeparatesRequiredFromInformational(t *testing.T) {
	checkRuns := `{"check_runs":[
		{"name":"Run PR CI Tests and Deployments","status":"completed","conclusion":"success","app":{"id":15368}},
		{"name":"CodeQL","status":"completed","conclusion":"success","app":{"id":57789}},
		{"name":"Pin Freshness Gate","status":"completed","conclusion":"failure","app":{"id":15368}},
		{"name":"Full Suite Gate","status":"completed","conclusion":"cancelled","app":{"id":15368}}]}`
	statuses := `{"state":"failure","statuses":[{"context":"private-sandbox-tests","state":"failure"}]}`

	client := testClient(requiredChecksServer(t, requiredRulesJSON, checkRuns, statuses))
	states, err := client.branchCheckStates(t.Context(), "main", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !states.Known {
		t.Fatal("expected the ruleset to be known")
	}
	if states.RequiredState != "success" {
		t.Fatalf("required state = %q, want success (the required context is green: %v)", states.RequiredState, states.FailingRequired)
	}
	if len(states.FailingRequired) != 0 {
		t.Fatalf("no required context is red, got %v", states.FailingRequired)
	}
	informational := strings.Join(states.Informational, ",")
	for _, want := range []string{"private-sandbox-tests", "Pin Freshness Gate", "Full Suite Gate"} {
		if !strings.Contains(informational, want) {
			t.Errorf("expected %q to be reported as informational, got %v", want, states.Informational)
		}
	}
	// The aggregate state of every check is still red — which is exactly why the
	// informational split matters.
	runs, facts, err := client.commitChecks(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if combinedStateFromFacts(runs, facts) != "failure" {
		t.Fatalf("expected the aggregate of all checks to stay failure")
	}
}

// TestBranchCheckStatesRequiresGreenRequiredContexts guards the safety side: a
// red *required* context is a failure, and a required context that has not
// reported yet is pending (never assumed green).
func TestBranchCheckStatesRequiresGreenRequiredContexts(t *testing.T) {
	red := `{"check_runs":[{"name":"Run PR CI Tests and Deployments","status":"completed","conclusion":"failure","app":{"id":15368}}]}`
	client := testClient(requiredChecksServer(t, requiredRulesJSON, red, `{"state":"failure","statuses":[]}`))
	states, err := client.branchCheckStates(t.Context(), "main", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if states.RequiredState != "failure" {
		t.Fatalf("required state = %q, want failure", states.RequiredState)
	}
	if len(states.FailingRequired) != 1 {
		t.Fatalf("expected the required context to be listed as failing, got %v", states.FailingRequired)
	}

	missing := `{"check_runs":[{"name":"CodeQL","status":"completed","conclusion":"success","app":{"id":57789}}]}`
	client = testClient(requiredChecksServer(t, requiredRulesJSON, missing, `{"state":"success","statuses":[]}`))
	states, err = client.branchCheckStates(t.Context(), "main", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if states.RequiredState != "pending" {
		t.Fatalf("a required context that has not reported must be pending, got %q", states.RequiredState)
	}
}

// TestRequiredContextFromLegacyStatus guards the mixed CI styles: a required
// context delivered as a legacy commit status (not a check run) still counts.
func TestRequiredContextFromLegacyStatus(t *testing.T) {
	statuses := `{"state":"success","statuses":[{"context":"Run PR CI Tests and Deployments","state":"success"},{"context":"private-sandbox-tests","state":"failure"}]}`
	client := testClient(requiredChecksServer(t, requiredRulesJSON, `{"check_runs":[]}`, statuses))
	states, err := client.branchCheckStates(t.Context(), "main", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if states.RequiredState != "success" {
		t.Fatalf("required state = %q, want success (via the legacy status)", states.RequiredState)
	}
	if len(states.Informational) != 1 || states.Informational[0] != "private-sandbox-tests" {
		t.Fatalf("informational = %v, want [private-sandbox-tests]", states.Informational)
	}
}

// TestBranchCheckStatesFallsBackWhenRulesetIsUnreadable guards the rollback
// story: a repository whose rulesets cannot be read keeps the aggregate
// all-checks state authoritative (the required list stays empty).
func TestBranchCheckStatesFallsBackWhenRulesetIsUnreadable(t *testing.T) {
	client := testClient(requiredChecksServer(t, "", `{"check_runs":[]}`, `{"state":"success","statuses":[]}`))
	if _, err := client.branchCheckStates(t.Context(), "main", "abc123"); err == nil {
		t.Fatal("expected an error when the ruleset cannot be read")
	}
}

// TestPullRequestFromRESTIsRulesetAware is the end-to-end mapping test: a fully
// green required context plus the red informational commit status must yield
// ChecksState=success (so the planner may merge), with the informational status
// named — despite the combined commit status being `failure`.
func TestPullRequestFromRESTIsRulesetAware(t *testing.T) {
	checkRuns := `{"check_runs":[{"name":"Run PR CI Tests and Deployments","status":"completed","conclusion":"success","app":{"id":15368}}]}`
	statuses := `{"state":"failure","statuses":[{"context":"private-sandbox-tests","state":"failure"}]}`
	client := testClient(requiredChecksServer(t, requiredRulesJSON, checkRuns, statuses))

	mergeable := true
	pr := client.pullRequestFromREST(t.Context(), githubPullRequest{
		Number:    705,
		Title:     "chore(consolidate): r25",
		Mergeable: &mergeable,
		Head: struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}{Ref: "chore/consolidate-2026-10-18-r25", SHA: "fc0161ba"},
		Base: struct {
			Ref string `json:"ref"`
		}{Ref: "main"},
		User: struct {
			Login string `json:"login"`
		}{Login: "cur1ousdude"},
	})

	if pr.ChecksState != "success" {
		t.Fatalf("ChecksState = %q, want success (only the required context gates the merge)", pr.ChecksState)
	}
	if !pr.RequiredChecksKnown || pr.RequiredChecksState != "success" {
		t.Fatalf("required checks = known:%v state:%q", pr.RequiredChecksKnown, pr.RequiredChecksState)
	}
	if len(pr.FailingRequiredChecks) != 0 {
		t.Fatalf("no required check is red, got %v", pr.FailingRequiredChecks)
	}
	if len(pr.InformationalChecks) != 1 || pr.InformationalChecks[0] != "private-sandbox-tests" {
		t.Fatalf("informational checks = %v, want [private-sandbox-tests]", pr.InformationalChecks)
	}
	if !checksGreen(pr.ChecksState) {
		t.Fatal("the planner must see this PR as green")
	}
}

// TestPullRequestFromRESTFallsBackToAllChecks guards the case where the ruleset
// requires nothing: every check stays authoritative (the previous behaviour).
func TestPullRequestFromRESTFallsBackToAllChecks(t *testing.T) {
	rules := `[{"type":"required_status_checks","parameters":{"required_status_checks":[]}}]`
	statuses := `{"state":"failure","statuses":[{"context":"private-sandbox-tests","state":"failure"}]}`
	client := testClient(requiredChecksServer(t, rules, `{"check_runs":[]}`, statuses))

	mergeable := true
	pr := client.pullRequestFromREST(t.Context(), githubPullRequest{
		Number:    706,
		Mergeable: &mergeable,
		Head: struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}{Ref: "feat/x", SHA: "deadbeef"},
		Base: struct {
			Ref string `json:"ref"`
		}{Ref: "main"},
	})

	if pr.ChecksState != "failure" {
		t.Fatalf("ChecksState = %q, want failure (no required contexts: every check stays authoritative)", pr.ChecksState)
	}
	if pr.RequiredChecksKnown {
		t.Fatal("no required context means the required check state must stay unknown")
	}
	if len(pr.InformationalChecks) != 1 {
		t.Fatalf("informational checks = %v, want the red status to be named", pr.InformationalChecks)
	}
}

// TestRequiredStatusChecksCached guards that a pass over many PRs of the same
// base branch reads the ruleset once.
func TestRequiredStatusChecksCached(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requests++
		_, _ = w.Write([]byte(requiredRulesJSON))
	}))
	t.Cleanup(server.Close)

	client := testClient(server)
	for i := 0; i < 3; i++ {
		if _, _, err := client.requiredStatusChecksCached(t.Context(), "main"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("expected one ruleset read, got %d", requests)
	}
}

// TestParseRunIDAndCheckTime guards the ordering inputs: the workflow-run id is
// parsed out of `details_url` and a malformed/absent timestamp degrades to the
// zero time instead of failing the resolution.
func TestParseRunIDAndCheckTime(t *testing.T) {
	if got := parseRunID("https://github.com/o/n/actions/runs/39892668623/job/120216647618"); got != 39892668623 {
		t.Fatalf("run id = %d, want 39892668623", got)
	}
	for _, url := range []string{"", "https://github.com/o/n/runs/120215871330"} {
		if got := parseRunID(url); got != 0 {
			t.Fatalf("parseRunID(%q) = %d, want 0", url, got)
		}
	}
	if !parseCheckTime("2026-10-23T03:03:29Z").Equal(time.Date(2026, 10, 23, 3, 3, 29, 0, time.UTC)) {
		t.Fatal("RFC3339 timestamp was not parsed")
	}
	if !parseCheckTime("not-a-time").IsZero() || !parseCheckTime("").IsZero() {
		t.Fatal("an unusable timestamp must degrade to the zero time")
	}
}

// TestNewestCheckRunsKeepsTheNewestInstancePerContext is the ci#847 core case:
// after a supersede, GitHub reports the dead run as CANCELLED *next to* the green
// replacement of the same context. Branch protection evaluates only the newest
// instance, so the resolver must collapse the rollup the same way - otherwise a
// green consolidation PR is vetoed forever by its own superseded run.
func TestNewestCheckRunsKeepsTheNewestInstancePerContext(t *testing.T) {
	// Verbatim shape observed on PR #1057: the superseded run finished (in wall
	// time) before the successful replacement even started.
	facts := []checkRunFact{
		{Name: "Run PR CI Tests and Deployments", Status: "completed", Conclusion: "cancelled", IntegrationID: 15368,
			StartedAt: parseCheckTime("2026-10-23T03:00:14Z"), CompletedAt: parseCheckTime("2026-10-23T03:00:34Z"), RunID: 39892624806, CheckRunID: 120215677969},
		{Name: "Run PR CI Tests and Deployments", Status: "completed", Conclusion: "success", IntegrationID: 15368,
			StartedAt: parseCheckTime("2026-10-23T03:03:29Z"), CompletedAt: parseCheckTime("2026-10-23T03:03:49Z"), RunID: 39892668623, CheckRunID: 120216647618},
	}
	kept := newestCheckRuns(facts)
	if len(kept) != 1 {
		t.Fatalf("expected one instance per context, got %d: %#v", len(kept), kept)
	}
	if kept[0].Conclusion != "success" {
		t.Fatalf("kept the superseded run: %#v", kept[0])
	}
}

// TestNewestCheckRunsIgnoresCompleteBeforeStartAndFallsBackToRunID guards the
// two ordering subtleties: a skipped run reports completed_at *before*
// started_at, so the later of the two orders it, and when the timestamps are
// identical the workflow-run / check-run id breaks the tie.
func TestNewestCheckRunsIgnoresCompleteBeforeStartAndFallsBackToRunID(t *testing.T) {
	skipped := checkRunFact{Name: "Update PR Comment After Release", Status: "completed", Conclusion: "skipped",
		StartedAt: parseCheckTime("2026-10-23T03:00:12Z"), CompletedAt: parseCheckTime("2026-10-23T03:00:11Z"), RunID: 39892624806}
	replacement := checkRunFact{Name: "Update PR Comment After Release", Status: "completed", Conclusion: "success",
		StartedAt: parseCheckTime("2026-10-23T03:03:26Z"), CompletedAt: parseCheckTime("2026-10-23T03:03:26Z"), RunID: 39892668623}
	if kept := newestCheckRuns([]checkRunFact{skipped, replacement}); len(kept) != 1 || kept[0].Conclusion != "success" {
		t.Fatalf("a complete-before-start run was mis-ranked: %#v", kept)
	}

	stamp := parseCheckTime("2026-10-23T03:00:00Z")
	older := checkRunFact{Name: "CodeQL", Status: "completed", Conclusion: "failure", StartedAt: stamp, CompletedAt: stamp, RunID: 100, CheckRunID: 7}
	newer := checkRunFact{Name: "CodeQL", Status: "completed", Conclusion: "success", StartedAt: stamp, CompletedAt: stamp, RunID: 200, CheckRunID: 8}
	if kept := newestCheckRuns([]checkRunFact{older, newer}); len(kept) != 1 || kept[0].RunID != 200 {
		t.Fatalf("identical timestamps must fall back to the run id: %#v", kept)
	}

	// Distinct contexts and distinct apps of the same name stay separate.
	mixed := newestCheckRuns([]checkRunFact{
		{Name: "CodeQL", IntegrationID: 1, Status: "completed", Conclusion: "success"},
		{Name: "CodeQL", IntegrationID: 2, Status: "completed", Conclusion: "failure"},
		{Name: "Pin Freshness Gate", IntegrationID: 1, Status: "completed", Conclusion: "success"},
	})
	if len(mixed) != 3 {
		t.Fatalf("distinct context/app pairs must not be merged: %#v", mixed)
	}
}

// TestBranchCheckStatesPrefersNewestRequiredContextInstance is the same ci#847
// case end to end: the required context is present twice (superseded CANCELLED
// + green replacement) and the branch must resolve to success with no failing
// required context named.
func TestBranchCheckStatesPrefersNewestRequiredContextInstance(t *testing.T) {
	checkRuns := `{"check_runs":[
		{"id":11,"name":"Run PR CI Tests and Deployments","status":"completed","conclusion":"cancelled","started_at":"2026-10-23T03:00:14Z","completed_at":"2026-10-23T03:00:34Z","details_url":"https://github.com/o/n/actions/runs/39892624806/job/120215677969","app":{"id":15368}},
		{"id":12,"name":"Run PR CI Tests and Deployments","status":"completed","conclusion":"success","started_at":"2026-10-23T03:03:29Z","completed_at":"2026-10-23T03:03:49Z","details_url":"https://github.com/o/n/actions/runs/39892668623/job/120216647618","app":{"id":15368}}]}`

	client := testClient(requiredChecksServer(t, requiredRulesJSON, checkRuns, `{"state":"failure","statuses":[]}`))
	states, err := client.branchCheckStates(t.Context(), "main", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if states.RequiredState != "success" {
		t.Fatalf("required state = %q, want success: the superseded CANCELLED run must not veto its green replacement (%v)", states.RequiredState, states.FailingRequired)
	}
	if len(states.FailingRequired) != 0 {
		t.Fatalf("no required context is red, got %v", states.FailingRequired)
	}
	if len(states.Informational) != 0 {
		t.Fatalf("the superseded instance is not a context of its own, got %v", states.Informational)
	}

	runs, statuses, err := client.commitChecks(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := combinedStateFromFacts(runs, statuses); got != "success" {
		t.Fatalf("combined state = %q, want success once the rollup is normalized", got)
	}
}

// TestNewestCheckRunsKeepsARealCancelledFailure guards the safety side of
// ci#847: only a *superseded* instance is dropped. When the newest instance of a
// context is itself cancelled/failed, the context stays red.
func TestNewestCheckRunsKeepsARealCancelledFailure(t *testing.T) {
	facts := []checkRunFact{
		{Name: "Full Suite Gate", Status: "completed", Conclusion: "success",
			StartedAt: parseCheckTime("2026-10-23T03:00:00Z"), RunID: 1},
		{Name: "Full Suite Gate", Status: "completed", Conclusion: "cancelled",
			StartedAt: parseCheckTime("2026-10-23T04:00:00Z"), RunID: 2},
	}
	kept := newestCheckRuns(facts)
	if len(kept) != 1 || kept[0].Conclusion != "cancelled" {
		t.Fatalf("the newest instance is the cancelled one: %#v", kept)
	}
	if got := combinedStateFromFacts(kept, nil); got != "failure" {
		t.Fatalf("combined state = %q, want failure", got)
	}
}

// TestStalePendingCombinedStatusIsNotABlock is the ci#928 incident: right after
// a squash merge the combined-status endpoint can report `state: pending` with
// an empty `statuses` list for a commit that has no commit statuses at all,
// while `/check-runs` correctly returns no runs. A required context that has
// never reported via a legacy status is simply un-reported — it stays pending
// until a check actually exists to be pending — and the stale aggregate
// `pending` must never read as a *pending check* or a pending required context.
func TestStalePendingCombinedStatusIsNotABlock(t *testing.T) {
	// The exact ci#928 shape: `pending` with no statuses, no check runs.
	client := testClient(requiredChecksServer(t, requiredRulesJSON, `{"check_runs":[]}`, `{"state":"pending","statuses":[]}`))
	runs, facts, err := client.commitChecks(t.Context(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want none", len(runs))
	}
	// The aggregate over all checks must read as unknown (no checks at all),
	// never pending.
	if got := combinedStateFromFacts(runs, facts); got != "unknown" {
		t.Fatalf("combined state = %q, want unknown (a commit with no checks is not pending)", got)
	}
	// The required-context resolution must not turn the stale pending report
	// into a pending required check: a context that has never reported is
	// un-reported (not-found).
	required := []RequiredCheck{{Context: "files-sync"}}
	if _, found := requiredContextState(required[0], runs, facts); found {
		t.Fatal("required context resolved from the stale empty status list; want not-found")
	}
}

// TestCombinedEmptyNeverVetoesARedContext guards the safety side of ci#928: the
// empty-status sentinel is dropped only when there is genuinely nothing else. A
// real red legacy status still wins, and a commit whose combined-status list is
// empty but that has real statuses constructed by hand is unaffected.
func TestCombinedEmptyNeverVetoesARedContext(t *testing.T) {
	// A real red legacy status next to the empty report: the red status decides.
	statuses := []statusFact{
		{Context: "private-sandbox-tests", State: "failure"},
		{CombinedEmpty: true},
	}
	if got := combinedStateFromFacts(nil, statuses); got != "failure" {
		t.Fatalf("combined state = %q, want failure", got)
	}
	if got := combinedStateFromFacts(nil, []statusFact{{CombinedEmpty: true}}); got != "unknown" {
		t.Fatalf("combined state = %q, want unknown", got)
	}
	// A hand-built empty fact (no context, no state, no sentinel) is not a real
	// status either: the aggregate collapses to unknown, never pending.
	if got := combinedStateFromFacts(nil, []statusFact{{Context: "", State: ""}}); got != "unknown" {
		t.Fatalf("combined state = %q, want unknown", got)
	}
}
