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
)

// GitHubAPIClient is the REST client used by the auto-consolidation runner.
type GitHubAPIClient struct {
	BaseURL string
	Repo    string
	Token   string
	HTTP    *http.Client
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
		BaseURL: strings.TrimRight(base, "/"),
		Repo:    strings.TrimSpace(repo),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
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
// resolving mergeability (REST `mergeable` is null while GitHub computes it)
// and the combined checks state.
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
	if state, err := c.combinedChecksState(ctx, row.Head.SHA); err == nil {
		pr.ChecksState = state
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
	var runs struct {
		CheckRuns []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", c.Repo, url.PathEscape(sha))
	if err := c.request(ctx, http.MethodGet, path, nil, &runs); err != nil {
		return "", err
	}
	if len(runs.CheckRuns) > 0 {
		state := "success"
		for _, run := range runs.CheckRuns {
			if !strings.EqualFold(run.Status, "completed") {
				return "pending", nil
			}
			switch strings.ToLower(strings.TrimSpace(run.Conclusion)) {
			case "success", "neutral", "skipped":
			case "failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale":
				return "failure", nil
			default:
				state = "pending"
			}
		}
		return state, nil
	}

	var status struct {
		State string `json:"state"`
	}
	legacyPath := fmt.Sprintf("/repos/%s/commits/%s/status", c.Repo, url.PathEscape(sha))
	if err := c.request(ctx, http.MethodGet, legacyPath, nil, &status); err != nil {
		return "", err
	}
	return strings.TrimSpace(status.State), nil
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
			Number:    pr.Number,
			Branch:    pr.HeadRef,
			HeadSHA:   pr.HeadSHA,
			Author:    pr.Author,
			Green:     checksGreen(pr.ChecksState),
			CreatedAt: pr.CreatedAt,
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
			"pr_number": strconv.Itoa(number),
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
