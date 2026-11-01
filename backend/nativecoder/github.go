package nativecoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file implements the GitHub side of auto-consolidation (ci#653): a small
// REST client that reads the repository's open pull requests and performs the
// consolidation actions (create/update the single `chore/consolidate-<date>`
// PR, apply the standard labels, request the ci#538 auto-approval and merge once
// every required check is green).
//
// The client is deliberately narrow and never logs credentials. The token comes
// from the deployment environment (the same credential the post-interaction
// executor uses) and the client stays a no-op when it is absent, so a
// deployment without the token keeps working exactly as before.

// Env keys the GitHub client resolves its credentials, repository and identity
// from. They are read at call time so a test can stub them.
const (
	// GitHubTokenEnv is the primary token env (shared with the post-interaction
	// executor, ci#649).
	GitHubTokenEnv = "OPEN_CHAT_GITHUB_POST_TOKEN"
	// GitHubTokenFallbackEnv / GitHubTokenEnvLegacy are the conventional
	// fall-backs.
	GitHubTokenFallbackEnv = "GITHUB_TOKEN"
	GitHubTokenEnvLegacy   = "GH_TOKEN"
	// GitHubRepoEnv names the `owner/name` repository to consolidate.
	GitHubRepoEnv = "OPEN_CHAT_GITHUB_REPO"
	// GitHubAPITokenEnvOverride / GitHubAPIBaseEnv override the API base URL
	// (tests, GitHub Enterprise).
	GitHubAPIBaseEnv = "OPEN_CHAT_GITHUB_API_BASE"
	// GitHubLoginEnv pins the identity `authors: [self]` resolves to, instead
	// of asking the API.
	GitHubLoginEnv = "OPEN_CHAT_GITHUB_LOGIN"
	// GitHubIssuesRepoEnv names the repository that owns the leaf issues closed
	// by a consolidation PR (ci#813). It defaults to LinkedIssueRepo.
	GitHubIssuesRepoEnv = "OPEN_CHAT_GITHUB_ISSUES_REPO"
)

// GitHubAPIClient is the REST client used by the auto-consolidation runner.
type GitHubAPIClient struct {
	BaseURL string
	Repo    string
	// IssuesRepo is the repository that owns the leaf issues (ci#813); it may
	// differ from Repo (the pull-request repository).
	IssuesRepo string
	Token      string
	HTTP       *http.Client
	// rules caches the base branch's ruleset-required contexts for the lifetime
	// of the client, ie for one consolidation pass over many PRs (ci#683).
	rules rulesetCache
}

// NewGitHubAPIClientFromEnv builds a client from the deployment environment. It
// returns (nil, nil) when no token or repository is configured, which makes
// auto-consolidation a no-op (the caller logs that and moves on).
func NewGitHubAPIClientFromEnv(repo string) (*GitHubAPIClient, error) {
	token := firstNonEmpty(
		os.Getenv(GitHubTokenEnv),
		os.Getenv(GitHubTokenFallbackEnv),
		os.Getenv(GitHubTokenEnvLegacy),
	)
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	if strings.TrimSpace(repo) == "" {
		repo = strings.TrimSpace(os.Getenv(GitHubRepoEnv))
	}
	if strings.TrimSpace(repo) == "" {
		return nil, nil
	}
	base := strings.TrimSpace(os.Getenv(GitHubAPIBaseEnv))
	if base == "" {
		base = "https://api.github.com"
	}
	return &GitHubAPIClient{
		BaseURL:    strings.TrimRight(base, "/"),
		Repo:       strings.TrimSpace(repo),
		IssuesRepo: firstNonEmpty(os.Getenv(GitHubIssuesRepoEnv), LinkedIssueRepo),
		Token:      token,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// RepoFromEnv returns the `owner/name` repository configured by the deployment
// environment (`OPEN_CHAT_GITHUB_REPO`), or an empty string.
func GitHubRepoFromEnv() string {
	return strings.TrimSpace(os.Getenv(GitHubRepoEnv))
}

// ConsolidationClient is the capability set the runner needs. It is an
// interface so the runner is unit-testable without GitHub.
type ConsolidationClient interface {
	// SelfLogin returns the GitHub login the configured token acts as, ie the
	// identity `authors: [self]` resolves to.
	SelfLogin(ctx context.Context) (string, error)
	// ListOpenPullRequests lists the repository's open pull requests with the
	// facts the planner needs.
	ListOpenPullRequests(ctx context.Context) ([]PullRequest, error)
	// ListOpenConsolidations lists the open consolidation PRs, if any.
	ListOpenConsolidations(ctx context.Context, branchPrefix string) ([]ExistingConsolidation, error)
	// EnsureConsolidationPullRequest creates (or refreshes) the single
	// consolidation PR and returns its number and whether it was created.
	EnsureConsolidationPullRequest(ctx context.Context, branch, base, title, body string) (int, bool, error)
	// CommentOnPullRequest adds a comment, used to record the rolled-in PRs.
	CommentOnPullRequest(ctx context.Context, number int, body string) error
	// RequestAutoApprove triggers the ci#538 auto-approve workflow for a green
	// consolidation PR (a `workflow_dispatch` of the auto-approve workflow).
	RequestAutoApprove(ctx context.Context, number int) error
	// MergePullRequest merges a green consolidation PR.
	MergePullRequest(ctx context.Context, number int) error
	// EnsureRollupIssue maintains the consolidation tracker issue (ci#415).
	EnsureRollupIssue(ctx context.Context, title, body string) error
}

// request performs a JSON API call and decodes the response into out (when
// non-nil). It never includes the token in an error message.
func (c *GitHubAPIClient) request(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return fmt.Errorf("github %s %s: read response: %w", method, path, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github %s %s: status %d: %s", method, path, resp.StatusCode, truncateForError(payload))
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("github %s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}

func (c *GitHubAPIClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// truncateForError keeps an API error short and free of anything but the
// GitHub error message.
func truncateForError(payload []byte) string {
	text := strings.TrimSpace(string(payload))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

// SelfLogin returns the login of the configured token's identity.
func (c *GitHubAPIClient) SelfLogin(ctx context.Context) (string, error) {
	if login := strings.TrimSpace(os.Getenv(GitHubLoginEnv)); login != "" {
		return login, nil
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.request(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

// githubPullRequest is the REST representation of a pull request.
type githubPullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Draft     bool   `json:"draft"`
	Mergeable *bool  `json:"mergeable"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	CreatedAt time.Time `json:"created_at"`
	// MergeStateStatus is only returned by GraphQL; the REST listing carries
	// Mergeable, so the client maps it below.
	MergeStateStatus string `json:"merge_state_status"`
}

// ListOpenPullRequests lists the repository's open pull requests (newest first,
// bounded to 100) with the ready-selection facts.
func (c *GitHubAPIClient) ListOpenPullRequests(ctx context.Context) ([]PullRequest, error) {
	var rows []githubPullRequest
	path := fmt.Sprintf("/repos/%s/pulls?state=open&per_page=100&sort=created&direction=desc", c.Repo)
	if err := c.request(ctx, http.MethodGet, path, nil, &rows); err != nil {
		return nil, err
	}
	out := make([]PullRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, c.pullRequestFromREST(ctx, row))
	}
	return out, nil
}

// pullRequestFromREST maps a REST pull request onto the planner's facts,
// resolving mergeability (REST `mergeable` is null while GitHub computes it) and
// the combined checks state.
//
// ci#683: the state is evaluated against the *required* contexts of the base
// branch's ruleset when those can be read, and every other red check/status is
// reported as informational. A red non-required status (the informational
// `private-sandbox-tests` commit status the private suite sets on a PR head) used
// to be indistinguishable from a required failure here, which is what made the
// merge path refuse a fully green PR.
func (c *GitHubAPIClient) pullRequestFromREST(ctx context.Context, row githubPullRequest) PullRequest {
	pr := PullRequest{
		Number:           row.Number,
		Title:            row.Title,
		Author:           row.User.Login,
		HeadRef:          row.Head.Ref,
		BaseRef:          row.Base.Ref,
		HeadSHA:          row.Head.SHA,
		Draft:            row.Draft,
		MergeStateStatus: row.MergeStateStatus,
		CreatedAt:        row.CreatedAt,
		Body:             row.Body,
		LinkedIssues:     LinkedIssueNumbers(row.Body),
	}
	for _, label := range row.Labels {
		pr.Labels = append(pr.Labels, label.Name)
	}
	switch {
	case row.Mergeable == nil:
		pr.Mergeable = false
		pr.MergeStateStatus = MergeStateUnknown
	case *row.Mergeable:
		pr.Mergeable = true
		if pr.MergeStateStatus == "" {
			pr.MergeStateStatus = MergeStateClean
		}
	default:
		pr.Mergeable = false
		if pr.MergeStateStatus == "" {
			pr.MergeStateStatus = MergeStateDirty
		}
	}
	if states, err := c.branchCheckStates(ctx, row.Base.Ref, row.Head.SHA); err == nil && states.Known {
		if len(states.Required) > 0 {
			// The ruleset names required contexts: exactly those gate the merge.
			pr.RequiredChecksKnown = true
			pr.RequiredChecksState = states.RequiredState
			pr.FailingRequiredChecks = states.FailingRequired
			pr.ChecksState = states.RequiredState
		} else if runs, statuses, err := c.commitChecks(ctx, row.Head.SHA); err == nil {
			// The ruleset requires nothing (or is absent): every check stays
			// authoritative, and the red ones are only reported.
			pr.ChecksState = combinedStateFromFacts(runs, statuses)
			// ci#928: the same stale-cache guard as in the combined path — a
			// just-merged commit that has neither runs nor statuses must not
			// read as `pending` (blocked); the planner's require_green gate
			// still rejects an unevaluated PR when the policy demands green.
			pr.ChecksEmpty = len(runs) == 0 && len(statuses) == 0
		}
		pr.InformationalChecks = states.Informational
	} else if state, err := c.combinedChecksState(ctx, row.Head.SHA); err == nil {
		pr.ChecksState = state
		// ci#928: a freshly-created (just-merged) commit can read a stale
		// `pending` with no contexts from GitHub's /status cache. Resolve the
		// empty-commit fingerprint so a bare `pending` never reads as a merge
		// block: the planner's require_green gate still rejects a PR that has
		// no reported checks when the policy demands green.
		if empty, err := c.combinedChecksStateEmpty(ctx, row.Head.SHA); err == nil {
			pr.ChecksEmpty = empty
		}
	}
	return pr
}

// combinedChecksState resolves the aggregate state of the checks on a commit.
// It prefers the check-runs API and falls back to the legacy commit status API,
// so both CI styles are covered.
func (c *GitHubAPIClient) combinedChecksState(ctx context.Context, sha string) (string, error) {
	if strings.TrimSpace(sha) == "" {
		return "", fmt.Errorf("empty head sha")
	}
	runs, statuses, err := c.commitChecks(ctx, sha)
	if err != nil {
		return "", err
	}
	return combinedStateFromFacts(runs, statuses), nil
}

// combinedChecksStateEmpty reports whether a commit carries no checks at all: no check runs
// and no legacy commit statuses. It is the resolver behind the ci#928 stale-cache
// fingerprint — GitHub's /status can momentarily return `pending` with an empty statuses
// list for a freshly-created just-merged commit with no checks, while /check-runs correctly
// returns []; a bare `pending` with no contexts is not a required check.
func (c *GitHubAPIClient) combinedChecksStateEmpty(ctx context.Context, sha string) (bool, error) {
	// Some wrappers short-circuit SHA lookups; never treat a missing sha as a
	// checks-empty commit (that would defeat the cross-check).
	if strings.TrimSpace(sha) == "" {
		return false, nil
	}
	runs, statuses, err := c.commitChecks(ctx, sha)
	if err != nil {
		return false, err
	}
	return len(runs) == 0 && len(statuses) == 0, nil
}

// ListOpenConsolidations lists the open consolidation PRs (branch prefix or the
// consolidation label) with their green state.
func (c *GitHubAPIClient) ListOpenConsolidations(ctx context.Context, branchPrefix string) ([]ExistingConsolidation, error) {
	prs, err := c.ListOpenPullRequests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExistingConsolidation, 0)
	for _, pr := range prs {
		if !IsConsolidationPullRequest(AutoConsolidationPolicy{BranchPrefix: branchPrefix}, pr) {
			continue
		}
		out = append(out, ExistingConsolidation{
			Number:              pr.Number,
			Branch:              pr.HeadRef,
			HeadSHA:             pr.HeadSHA,
			Author:              pr.Author,
			Green:               checksGreen(pr.ChecksState),
			InformationalChecks: append([]string(nil), pr.InformationalChecks...),
			CreatedAt:           pr.CreatedAt,
			LinkedIssues:        append([]int(nil), pr.LinkedIssues...),
		})
	}
	return out, nil
}

// EnsureConsolidationPullRequest creates the consolidation PR when it does not
// exist yet, otherwise refreshes its body. It returns the PR number and whether
// this call created it.
func (c *GitHubAPIClient) EnsureConsolidationPullRequest(ctx context.Context, branch, base, title, body string) (int, bool, error) {
	var existing []githubPullRequest
	path := fmt.Sprintf("/repos/%s/pulls?state=open&head=%s:%s&base=%s&per_page=10", c.Repo, c.repoOwner(), url.QueryEscape(branch), url.QueryEscape(base))
	if err := c.request(ctx, http.MethodGet, path, nil, &existing); err != nil {
		return 0, false, err
	}
	if len(existing) > 0 {
		number := existing[0].Number
		if err := c.request(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", c.Repo, number), map[string]interface{}{"body": body, "title": title}, nil); err != nil {
			return 0, false, err
		}
		return number, false, nil
	}
	var created struct {
		Number int `json:"number"`
	}
	payload := map[string]interface{}{"title": title, "head": branch, "base": base, "body": body, "draft": false}
	if err := c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls", c.Repo), payload, &created); err != nil {
		return 0, false, err
	}
	return created.Number, true, nil
}

// repoOwner returns the owner part of `owner/name`.
func (c *GitHubAPIClient) repoOwner() string {
	parts := strings.SplitN(c.Repo, "/", 2)
	return parts[0]
}

// CommentOnPullRequest posts an issue comment on a pull request.
func (c *GitHubAPIClient) CommentOnPullRequest(ctx context.Context, number int, body string) error {
	return c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", c.Repo, number), map[string]interface{}{"body": body}, nil)
}

// EnsureLabels adds the given labels to an issue/pull request. The endpoint is
// additive and idempotent, so re-applying an already present label is a no-op.
func (c *GitHubAPIClient) EnsureLabels(ctx context.Context, number int, labels []string) error {
	normalized := normalizeStringList(labels)
	if len(normalized) == 0 {
		return nil
	}
	payload := map[string]interface{}{"labels": normalized}
	return c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", c.Repo, number), payload, nil)
}

// RequestAutoApprove dispatches the ci#538 auto-approve workflow for the PR, so
// the required approving review is posted by the reviewer identity once the
// required PR CI run succeeded.
func (c *GitHubAPIClient) RequestAutoApprove(ctx context.Context, number int) error {
	payload := map[string]interface{}{
		"ref": "main",
		"inputs": map[string]string{
			"number": strconv.Itoa(number),
		},
	}
	path := fmt.Sprintf("/repos/%s/actions/workflows/auto-approve-consolidation.yaml/dispatches", c.Repo)
	return c.request(ctx, http.MethodPost, path, payload, nil)
}

// MergePullRequest merges a green consolidation PR with a merge commit, keeping
// the consolidated history reviewable.
func (c *GitHubAPIClient) MergePullRequest(ctx context.Context, number int) error {
	payload := map[string]interface{}{
		"merge_method": "merge",
		"commit_title": fmt.Sprintf("chore(consolidate): fold ready PRs into main (#%d)", number),
	}
	return c.request(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", c.Repo, number), payload, nil)
}

// issuesRepo returns the repository that owns the leaf issues (ci#813).
func (c *GitHubAPIClient) issuesRepo() string {
	if repo := strings.TrimSpace(c.IssuesRepo); repo != "" {
		return repo
	}
	return LinkedIssueRepo
}

// CloseIssue marks a leaf issue completed after its consolidation PR merged.
// It is idempotent: an already-closed issue is left untouched, and the comment
// is only posted on the transition. GitHub cannot auto-close a cross-repository
// issue, so the runner performs the transition explicitly (ci#813).
func (c *GitHubAPIClient) CloseIssue(ctx context.Context, number int, comment string) error {
	if number <= 0 {
		return fmt.Errorf("close issue: invalid issue number %d", number)
	}
	repo := c.issuesRepo()
	var issue struct {
		State string `json:"state"`
	}
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, number), nil, &issue); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(issue.State), "closed") {
		return nil
	}
	if strings.TrimSpace(comment) != "" {
		if err := c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), map[string]interface{}{"body": comment}, nil); err != nil {
			return err
		}
	}
	return c.request(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repo, number), map[string]interface{}{
		"state":        "closed",
		"state_reason": "completed",
	}, nil)
}

// EnsureRollupIssue maintains the single consolidation tracker issue (ci#415).
func (c *GitHubAPIClient) EnsureRollupIssue(ctx context.Context, title, body string) error {
	query := url.QueryEscape(fmt.Sprintf("repo:%s is:issue is:open in:title %q", c.Repo, title))
	var found struct {
		Items []struct {
			Number int `json:"number"`
		} `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, "/search/issues?per_page=1&q="+query, nil, &found); err != nil {
		return err
	}
	if len(found.Items) > 0 {
		return c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", c.Repo, found.Items[0].Number), map[string]interface{}{"body": body}, nil)
	}
	return c.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues", c.Repo), map[string]interface{}{"title": title, "body": body}, nil)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
