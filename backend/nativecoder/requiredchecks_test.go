package nativecoder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
