package nativecoder

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file resolves which status checks a pull request's base branch actually
// *requires* (ci#683).
//
// GitHub derives `mergeable_state: blocked` — and therefore `gh pr merge`'s
// "the base branch policy prohibits the merge" preflight — from **any**
// non-success commit status on the head, whether or not the ruleset requires it.
// The private sandbox suite sets the informational `private-sandbox-tests`
// commit status on every PR head, so a PR whose only *required* context
// (`Run PR CI Tests and Deployments`) was green and which was APPROVED and
// mergeable still reported `blocked`, and the CLI refused the merge while the
// REST merge endpoint succeeded immediately (the r25 incident).
//
// The client therefore reads the base branch's ruleset required contexts
// (`GET /repos/{owner}/{repo}/rules/branches/{branch}`) and evaluates exactly
// those, reporting every other red check/status as *informational*: the planner
// and the runner never treat such a status as a merge blocker, and the reason is
// spelled out instead of surfacing as an unexplained policy veto.
//
// Safety: when the ruleset cannot be read, or when it requires nothing at all,
// the aggregate state of *all* checks stays authoritative (the previous
// behaviour), so a deployment that cannot read its rulesets keeps requiring
// green checks.

// RequiredCheck is one status-check context a base-branch ruleset requires.
type RequiredCheck struct {
	Context string `json:"context"`
	// IntegrationID is the GitHub App id the context must come from (0 = any).
	IntegrationID int64 `json:"integration_id,omitempty"`
}

// BranchCheckStates is the check state of one commit, evaluated against the
// required contexts of its base branch.
type BranchCheckStates struct {
	// Known reports whether the base branch's required-context list was read.
	Known bool
	// Required are the required contexts. An empty list means the ruleset
	// requires nothing, in which case the aggregate state of all checks stays
	// authoritative.
	Required []RequiredCheck
	// RequiredState is the aggregate state over exactly the required contexts
	// ("success", "failure" or "pending"); empty when Required is empty.
	RequiredState string
	// FailingRequired lists the required contexts that are not green.
	FailingRequired []string
	// Informational lists red checks/statuses the ruleset does not require. They
	// are reported for the plan/log, and they are never a merge blocker.
	Informational []string
}

// checkRunFact is the minimal check-run fact the aggregation needs.
type checkRunFact struct {
	Name          string
	Status        string
	Conclusion    string
	IntegrationID int64
	// StartedAt/CompletedAt order the concurrent runs of one context: branch
	// protection evaluates the *newest* instance per (context, app), so the
	// aggregation must too (ci#847).
	StartedAt   time.Time
	CompletedAt time.Time
	// RunID is the workflow-run id parsed from details_url and CheckRunID the
	// check-run id; both are monotonically increasing and act as tiebreakers.
	RunID      int64
	CheckRunID int64
}

// observedAt is the effective ordering timestamp of a check-run. It is the
// later of started_at/completed_at, because a skipped run can report a
// completion *before* its start (observed on the ci#847 repro).
func (r checkRunFact) observedAt() time.Time {
	if r.CompletedAt.After(r.StartedAt) {
		return r.CompletedAt
	}
	return r.StartedAt
}

// newerThan orders two instances of the same (context, app): the later
// observation wins, then the higher workflow-run id, then the higher check-run
// id — the newest-per-context rule GitHub applies (ci#847).
func (r checkRunFact) newerThan(other checkRunFact) bool {
	if a, b := r.observedAt(), other.observedAt(); !a.Equal(b) {
		return a.After(b)
	}
	if r.RunID != other.RunID {
		return r.RunID > other.RunID
	}
	return r.CheckRunID > other.CheckRunID
}

// newestCheckRuns keeps only the newest check-run per (context, app) and
// preserves the first-seen order otherwise. A superseded run that GitHub
// reports as `cancelled` must not veto its green replacement, which is what
// permanently blocked a green consolidation PR (ci#847).
func newestCheckRuns(facts []checkRunFact) []checkRunFact {
	type key struct {
		name string
		app  int64
	}
	out := make([]checkRunFact, 0, len(facts))
	index := map[key]int{}
	for _, fact := range facts {
		factKey := key{name: strings.ToLower(strings.TrimSpace(fact.Name)), app: fact.IntegrationID}
		if at, ok := index[factKey]; ok {
			if fact.newerThan(out[at]) {
				out[at] = fact
			}
			continue
		}
		index[factKey] = len(out)
		out = append(out, fact)
	}
	return out
}

// parseCheckTime parses an RFC3339 check-run timestamp, returning the zero time
// when the field is absent or malformed (ordering then falls back to the ids).
func parseCheckTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// parseRunID extracts the workflow-run id from a check-run details_url
// (`.../actions/runs/<id>/job/<job>`); 0 when absent (the legacy commit-status
// URLs share no such structure and deliberately degrade to 0).
func parseRunID(detailsURL string) int64 {
	detailsURL = strings.TrimSpace(detailsURL)
	if detailsURL == "" {
		return 0
	}
	const marker = "/actions/runs/"
	idx := strings.Index(detailsURL, marker)
	if idx < 0 {
		return 0
	}
	rest := detailsURL[idx+len(marker):]
	end := strings.IndexAny(rest, "/?#")
	if end >= 0 {
		rest = rest[:end]
	}
	var id int64
	if _, err := fmt.Sscanf(rest, "%d", &id); err != nil {
		return 0
	}
	return id
}

// statusFact is the minimal legacy commit-status fact the aggregation needs.
type statusFact struct {
	Context string
	State   string
	// CombinedEmpty is true when the combined-status response carried no
	// statuses at all (`statuses:[]`), regardless of its aggregate `state`
	// field. GitHub's combined-status endpoint is momentarily stale for a
	// freshly created commit and can report `pending` with zero contexts (the
	// ci#928 just-squash-merged incident); such a report names no check, so it
	// must never be treated as a required-context failure or a pending block —
	// the check-runs list is the authoritative source for a commit that has no
	// commit statuses.
	CombinedEmpty bool
}

// rulesetCache stores the required contexts per branch: one planning pass reads
// the ruleset of the same base branch for every open PR, so the lookup is cached
// on the client (the cache is per client instance, ie per pass).
type rulesetCache struct {
	// Lock/Unlock guard the two maps below. The mutex is embedded so the call
	// sites read as `c.rules.Lock()`.
	sync.Mutex
	byRef   map[string][]RequiredCheck
	byRefOK map[string]bool
}

// RequiredStatusChecks reads the required status-check contexts of a branch from
// the repository rulesets. A repository whose rulesets cannot be read (a private
// repository without the plan feature, or a branch without a ruleset) is not an
// error for the caller: it falls back to the aggregate state of all checks.
func (c *GitHubAPIClient) RequiredStatusChecks(ctx context.Context, branch string) ([]RequiredCheck, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []RequiredCheck `json:"required_status_checks"`
		} `json:"parameters"`
	}
	path := fmt.Sprintf("/repos/%s/rules/branches/%s", c.Repo, url.PathEscape(branch))
	if err := c.request(ctx, http.MethodGet, path, nil, &rules); err != nil {
		return nil, err
	}

	out := make([]RequiredCheck, 0, len(rules))
	seen := map[string]struct{}{}
	for _, rule := range rules {
		if !strings.EqualFold(strings.TrimSpace(rule.Type), "required_status_checks") {
			continue
		}
		for _, check := range rule.Parameters.RequiredStatusChecks {
			name := strings.TrimSpace(check.Context)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, RequiredCheck{Context: name, IntegrationID: check.IntegrationID})
		}
	}
	return out, nil
}

// requiredStatusChecksCached is RequiredStatusChecks with a per-client cache, so
// a planning pass over many PRs of the same base branch reads the ruleset once.
func (c *GitHubAPIClient) requiredStatusChecksCached(ctx context.Context, branch string) ([]RequiredCheck, bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, false, fmt.Errorf("branch is required")
	}
	c.rules.Lock()
	if c.rules.byRefOK != nil {
		if ok := c.rules.byRefOK[branch]; ok {
			cached := append([]RequiredCheck(nil), c.rules.byRef[branch]...)
			c.rules.Unlock()
			return cached, true, nil
		}
	}
	c.rules.Unlock()

	checks, err := c.RequiredStatusChecks(ctx, branch)
	if err != nil {
		return nil, false, err
	}
	c.rules.Lock()
	if c.rules.byRef == nil {
		c.rules.byRef = map[string][]RequiredCheck{}
		c.rules.byRefOK = map[string]bool{}
	}
	c.rules.byRef[branch] = append([]RequiredCheck(nil), checks...)
	c.rules.byRefOK[branch] = true
	c.rules.Unlock()
	return checks, true, nil
}

// branchCheckStates resolves the required and informational check state of a
// commit against the required contexts of its base branch.
func (c *GitHubAPIClient) branchCheckStates(ctx context.Context, branch, sha string) (BranchCheckStates, error) {
	states := BranchCheckStates{}
	if strings.TrimSpace(sha) == "" {
		return states, fmt.Errorf("empty head sha")
	}
	required, known, err := c.requiredStatusChecksCached(ctx, branch)
	if err != nil {
		return states, err
	}
	if !known {
		return states, fmt.Errorf("required contexts of %s are unknown", branch)
	}
	runs, statuses, err := c.commitChecks(ctx, sha)
	if err != nil {
		return states, err
	}
	states.Known = true
	states.Required = required
	if len(required) == 0 {
		// Nothing is required: the aggregate state of every check stays
		// authoritative, but the red contexts are still reported so the plan and
		// the logs name them.
		states.Informational = redContexts(nil, runs, statuses)
		return states, nil
	}
	states.RequiredState, states.FailingRequired = evaluateRequiredContexts(required, runs, statuses)
	states.Informational = redContexts(required, runs, statuses)
	return states, nil
}

// commitChecks reads the check-runs and the (latest-per-context) legacy commit
// statuses of a commit.
//
// ci#928: the combined-status response for a commit that has no commit statuses
// can be momentarily stale right after a squash merge and report `state:
// pending` with `statuses:[]` (no status contexts at all). The empty list is
// recorded on the returned facts (CombinedEmpty) so the aggregation treats it
// as "no legacy statuses" instead of a pending block: a commit that has no
// statuses cannot have a pending one, and the check-runs list is authoritative
// for whether any check is running.
func (c *GitHubAPIClient) commitChecks(ctx context.Context, sha string) ([]checkRunFact, []statusFact, error) {
	var runs struct {
		CheckRuns []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Status      string `json:"status"`
			Conclusion  string `json:"conclusion"`
			StartedAt   string `json:"started_at"`
			CompletedAt string `json:"completed_at"`
			DetailsURL  string `json:"details_url"`
			App         struct {
				ID int64 `json:"id"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", c.Repo, url.PathEscape(sha))
	if err := c.request(ctx, http.MethodGet, path, nil, &runs); err != nil {
		return nil, nil, err
	}
	facts := make([]checkRunFact, 0, len(runs.CheckRuns))
	for _, run := range runs.CheckRuns {
		facts = append(facts, checkRunFact{
			Name:          strings.TrimSpace(run.Name),
			Status:        run.Status,
			Conclusion:    run.Conclusion,
			IntegrationID: run.App.ID,
			StartedAt:     parseCheckTime(run.StartedAt),
			CompletedAt:   parseCheckTime(run.CompletedAt),
			RunID:         parseRunID(run.DetailsURL),
			CheckRunID:    run.ID,
		})
	}
	// ci#847: branch protection evaluates the newest instance per
	// (context, app); collapse the rollup onto that instance before anything
	// aggregates it, so a superseded `cancelled` run cannot veto its green
	// replacement.
	facts = newestCheckRuns(facts)

	var status struct {
		State    string `json:"state"`
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	legacyPath := fmt.Sprintf("/repos/%s/commits/%s/status", c.Repo, url.PathEscape(sha))
	if err := c.request(ctx, http.MethodGet, legacyPath, nil, &status); err != nil {
		return nil, nil, err
	}
	out := make([]statusFact, 0, len(status.Statuses))
	for _, item := range status.Statuses {
		out = append(out, statusFact{Context: strings.TrimSpace(item.Context), State: item.State})
	}
	if len(status.Statuses) == 0 {
		// The stale `pending`/`statuses:[]` report (ci#928) is represented as
		// "no legacy statuses", never as a pending aggregate that could veto
		// a merge of a commit that has no pending contexts. The aggregate
		// `state` field of an empty list carries no information the facts
		// cannot already derive.
		out = append(out, statusFact{CombinedEmpty: true})
	}
	return facts, out, nil
}

// combinedStateFromFacts aggregates a commit's checks the way the legacy
// "combined status" API did: check-runs win when there are any (a non-completed
// run is pending, a red conclusion is a failure), otherwise the commit statuses
// decide. It mirrors combinedChecksState so both paths stay consistent.
//
// ci#928: a combined-status report of `pending` with no statuses is not a
// pending check — GitHub's endpoint can report it momentarily for a freshly
// created commit even though nothing is running. The empty report is therefore
// overridden by the check-runs list (authoritative when present) and otherwise
// reads as "unknown" rather than "pending", so a just-squash-merged or no-PR
// commit with no checks cannot be held as pending.
func combinedStateFromFacts(runs []checkRunFact, statuses []statusFact) string {
	if len(runs) > 0 {
		state := "success"
		for _, run := range runs {
			switch runState(run) {
			case "failure":
				return "failure"
			case "pending":
				state = "pending"
			}
		}
		return state
	}
	// No check runs: the legacy statuses (if any) decide. A commit with no
	// statuses at all reports "unknown" — never "pending" — because nothing is
	// actually running and the aggregate `state` field of an empty list is a
	// stale cache artifact (ci#928).
	statuses = dropCombinedEmpty(statuses)
	if len(statuses) == 0 {
		return "unknown"
	}
	state := "success"
	for _, status := range statuses {
		switch statusFactState(status) {
		case "failure":
			return "failure"
		case "pending":
			state = "pending"
		}
	}
	return state
}

// dropCombinedEmpty removes the empty-status sentinel (ci#928) and any status
// entry with no state (an artifact of a combined-status response that carried
// no contexts at all) from a fact list, leaving only real statuses. A commit
// whose entire report was empty therefore yields no statuses, which the
// aggregation reads as "unknown" rather than the stale report's `pending`.
func dropCombinedEmpty(statuses []statusFact) []statusFact {
	real := statuses[:0]
	for _, status := range statuses {
		if status.CombinedEmpty {
			continue
		}
		if status.State == "" {
			continue
		}
		real = append(real, status)
	}
	return real
}

// evaluateRequiredContexts aggregates exactly the required contexts of a commit:
// a required context that has not reported yet is *pending* (never assumed
// green), a red one is a failure, and any red required context fails the whole
// evaluation.
//
// ci#928: a context that has **not reported at all** (no check-run, no commit
// status, or a commit whose combined-status list is empty) is not a pending
// required check — GitHub's combined-status endpoint can report `pending` with
// zero statuses for a freshly created commit. A required context that has never
// reported therefore cannot hold a merge; it stays un-reported (not-found,
// which resolves to pending only when the ruleset names it AND a check actually
// exists to be pending).
func evaluateRequiredContexts(required []RequiredCheck, runs []checkRunFact, statuses []statusFact) (string, []string) {
	hasFailure := false
	hasPending := false
	failing := make([]string, 0, len(required))
	for _, check := range required {
		state, found := requiredContextState(check, runs, statuses)
		switch {
		case !found || state == "pending":
			hasPending = true
			failing = append(failing, check.Context)
		case state == "failure":
			hasFailure = true
			failing = append(failing, check.Context)
		}
	}
	switch {
	case hasFailure:
		return "failure", failing
	case hasPending:
		return "pending", failing
	default:
		return "success", nil
	}
}

// requiredContextState resolves the state of one required context from the
// check-runs (preferring the app the ruleset names) and, when no check-run
// carries it, from the legacy commit statuses.
func requiredContextState(check RequiredCheck, runs []checkRunFact, statuses []statusFact) (string, bool) {
	candidates := make([]checkRunFact, 0, 2)
	appMatched := check.IntegrationID == 0
	for _, run := range runs {
		if !strings.EqualFold(run.Name, check.Context) {
			continue
		}
		if check.IntegrationID != 0 && run.IntegrationID == check.IntegrationID {
			appMatched = true
			candidates = append(candidates, run)
			continue
		}
		if check.IntegrationID == 0 {
			candidates = append(candidates, run)
		}
	}
	if !appMatched {
		// Same name from another app (or an app id the API did not report): fall
		// back to a name match rather than treating the required context as
		// missing.
		candidates = candidates[:0]
		for _, run := range runs {
			if strings.EqualFold(run.Name, check.Context) {
				candidates = append(candidates, run)
			}
		}
	}
	if len(candidates) > 0 {
		state := "success"
		for _, run := range candidates {
			switch runState(run) {
			case "failure":
				return "failure", true
			case "pending":
				state = "pending"
			}
		}
		return state, true
	}
	for _, status := range statuses {
		if strings.EqualFold(status.Context, check.Context) {
			return statusFactState(status), true
		}
	}
	// ci#928: a commit with no commit statuses at all (the stale
	// `pending`/`statuses:[]` combined-status report right after a squash merge)
	// carries no status for this required context, however the aggregate `state`
	// field read. The check-runs list is the authoritative source for a commit
	// that has no statuses, so the context has simply not reported.
	for _, status := range statuses {
		if status.CombinedEmpty {
			return "", false
		}
	}
	return "", false
}

// redContexts lists the red (non-success) contexts of a commit that the ruleset
// does not require, de-duplicated and sorted, so the plan and the logs can name
// exactly what is informational.
func redContexts(required []RequiredCheck, runs []checkRunFact, statuses []statusFact) []string {
	requiredNames := map[string]struct{}{}
	for _, check := range required {
		requiredNames[strings.ToLower(strings.TrimSpace(check.Context))] = struct{}{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, isRequired := requiredNames[key]; isRequired {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	for _, run := range runs {
		if runState(run) == "failure" {
			add(run.Name)
		}
	}
	for _, status := range statuses {
		if statusFactState(status) == "failure" {
			add(status.Context)
		}
	}
	sort.Strings(out)
	return out
}

// runState maps a check-run onto "success", "failure" or "pending".
func runState(run checkRunFact) string {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "completed") {
		return "pending"
	}
	switch strings.ToLower(strings.TrimSpace(run.Conclusion)) {
	case "success", "neutral", "skipped":
		return "success"
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
		return "failure"
	default:
		return "pending"
	}
}

// statusFactState maps a legacy commit status onto "success", "failure" or
// "pending".
func statusFactState(status statusFact) string {
	switch strings.ToLower(strings.TrimSpace(status.State)) {
	case "success":
		return "success"
	case "failure", "error":
		return "failure"
	default:
		return "pending"
	}
}
