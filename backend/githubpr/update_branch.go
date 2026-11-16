// Package githubpr implements the typed GitHub pull-request routes that must
// work without org-membership GraphQL resolution (ci#1037, blocked by the
// ci#1035 `read:org` defect). It ships the "update branch" write that un-stales
// a green PR after its base branch moved:
//
//	github_pr_update_branch  ->  PUT /repos/{owner}/{repo}/pulls/{n}/update-branch
//
// The two legitimate non-error answers GitHub returns as HTTP 422 ("already up
// to date" and "merge conflict between base and head") are reported as
// structured states instead of raw failures, and every call re-reads the pull
// request so the answer carries the resulting mergeable / mergeStateStatus /
// head SHA with `verified` semantics like the other GitHub write tools: a call
// whose head did not actually move says so.
package githubpr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// UpdateBranchToolName is the typed tool name, next to the existing
	// github_pr_* typed tools.
	UpdateBranchToolName = "github_pr_update_branch"
	// DefaultBaseURL is the public GitHub REST API base.
	DefaultBaseURL = "https://api.github.com"

	defaultTimeout   = 30 * time.Second
	maxErrorBodySize = 1 << 16
)

// UpdateBranchState is the structured outcome of one update-branch call.
type UpdateBranchState string

const (
	// StateUpdated means GitHub rewrote the branch (202) and the re-read
	// shows a new head SHA.
	StateUpdated UpdateBranchState = "updated"
	// StateAlreadyUpToDate means GitHub answered 422 "already up to date";
	// this is a normal, non-error answer.
	StateAlreadyUpToDate UpdateBranchState = "already_up_to_date"
	// StateMergeConflict means GitHub answered 422 because base and head
	// conflict; this is a normal, non-error answer (a human or a rebase is
	// needed, retrying is pointless).
	StateMergeConflict UpdateBranchState = "merge_conflict"
	// StateFailed means GitHub returned another non-2xx answer; the caller
	// also gets an error describing it.
	StateFailed UpdateBranchState = "failed"
)

// UpdateBranchRequest is one typed update-branch call. Repo is optional and
// falls back to Client.DefaultRepo and then OPEN_CHAT_GITHUB_REPO; Number is
// required.
type UpdateBranchRequest struct {
	Repo string `json:"repo,omitempty"`
	// Number is the pull request number (required, > 0).
	Number int `json:"number"`
	// ExpectedHeadSHA is the optional optimistic-concurrency guard GitHub
	// accepts in the request body: the update only applies when the head is
	// still at that commit.
	ExpectedHeadSHA string `json:"expected_head_sha,omitempty"`
}

// UpdateBranchResult is the typed, self-describing answer of one call.
type UpdateBranchResult struct {
	State  UpdateBranchState `json:"state"`
	Tool   string            `json:"tool"`
	Repo   string            `json:"repo"`
	Number int               `json:"number"`
	// Verified is true only when the re-read confirms the intended effect.
	Verified bool `json:"verified"`
	// Message is GitHub's own message for the call (the 422 text for the
	// already-up-to-date / merge-conflict answers).
	Message string `json:"message,omitempty"`
	// PreviousHeadSHA is the head before the call (the expected head when
	// the caller supplied one).
	PreviousHeadSHA string `json:"previous_head_sha,omitempty"`
	// HeadSHA is the head SHA read back after the call.
	HeadSHA string `json:"head_sha,omitempty"`
	// Mergeable is GitHub's tri-state mergeable flag (nil = not computed).
	Mergeable *bool `json:"mergeable,omitempty"`
	// MergeStateStatus is the normalized REST mergeable_state, upper-cased
	// like the GraphQL mergeStateStatus (BEHIND, CLEAN, DIRTY, BLOCKED,
	// UNKNOWN, ...).
	MergeStateStatus string `json:"merge_state_status,omitempty"`
	// VerificationError explains why Verified is false.
	VerificationError string `json:"verification_error,omitempty"`
}

// PullSnapshot is the re-read half of the write: the pull request state after
// the call.
type PullSnapshot struct {
	Number           int    `json:"number"`
	HeadSHA          string `json:"head_sha"`
	Mergeable        *bool  `json:"mergeable,omitempty"`
	MergeStateStatus string `json:"merge_state_status,omitempty"`
	MergeableState   string `json:"mergeable_state,omitempty"`
}

// Client talks to the GitHub REST API for one typed route.
type Client struct {
	HTTPClient  *http.Client
	BaseURL     string
	Token       string
	DefaultRepo string
}

// Parameter is one typed argument of the tool definition below.
type Parameter struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// ToolSpec is the typed tool surface (name, description, arguments) so the
// route can be registered without a GraphQL org-membership lookup.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  []Parameter
}

// UpdateBranchToolSpec is the typed definition of github_pr_update_branch.
var UpdateBranchToolSpec = ToolSpec{
	Name: UpdateBranchToolName,
	Description: "Bring a pull request's head branch up to date with its base branch through the GitHub " +
		"REST update-branch endpoint (PUT /repos/{o}/{r}/pulls/{n}/update-branch). Use it when a PR's " +
		"required gate fails only because the head is behind the base; never force-rebase or re-create " +
		"the PR for that. Answers with state updated|already_up_to_date|merge_conflict plus the re-read " +
		"mergeable, merge_state_status and head_sha, and verified=false when the head did not move.",
	Parameters: []Parameter{
		{Name: "repo", Type: "string", Description: "owner/name; defaults to OPEN_CHAT_GITHUB_REPO"},
		{Name: "number", Type: "integer", Description: "pull request number", Required: true},
		{Name: "expected_head_sha", Type: "string", Description: "only update when the head is still at this commit"},
	},
}

// TokenFromEnv resolves the GitHub token the same way the rest of the backend
// does, so the route needs no new secret plumbing.
func TokenFromEnv() string {
	for _, key := range []string{"OPEN_CHAT_GITHUB_POST_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

// RepoFromEnv returns the configured default repository (owner/name).
func RepoFromEnv() string {
	return strings.TrimSpace(os.Getenv("OPEN_CHAT_GITHUB_REPO"))
}

// ParseUpdateBranchArgs decodes and validates the typed tool arguments.
func ParseUpdateBranchArgs(raw []byte) (UpdateBranchRequest, error) {
	var req UpdateBranchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("invalid %s arguments: %w", UpdateBranchToolName, err)
	}
	req.Repo = strings.TrimSpace(req.Repo)
	req.ExpectedHeadSHA = strings.TrimSpace(req.ExpectedHeadSHA)
	if req.Number <= 0 {
		return req, fmt.Errorf("%s requires a positive pull request number", UpdateBranchToolName)
	}
	return req, nil
}

// RunUpdateBranchTool decodes the typed arguments and runs the route.
func (c *Client) RunUpdateBranchTool(ctx context.Context, raw []byte) (*UpdateBranchResult, error) {
	req, err := ParseUpdateBranchArgs(raw)
	if err != nil {
		return nil, err
	}
	return c.UpdateBranch(ctx, req)
}

func (c *Client) baseURL() string {
	if c != nil {
		if base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"); base != "" {
			return base
		}
	}
	return DefaultBaseURL
}

func (c *Client) token() string {
	if c != nil {
		if token := strings.TrimSpace(c.Token); token != "" {
			return token
		}
	}
	return TokenFromEnv()
}

func (c *Client) defaultRepo() string {
	if c != nil {
		if repo := strings.TrimSpace(c.DefaultRepo); repo != "" {
			return repo
		}
	}
	return RepoFromEnv()
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, reader)
	if err != nil {
		return nil, fmt.Errorf("building github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.token(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling github api: %w", err)
	}
	return resp, nil
}

// drainAPIResponse closes the body and returns GitHub's message plus the status.
func drainAPIResponse(resp *http.Response) (string, int) {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = truncate(strings.TrimSpace(string(raw)), 300)
	}
	return message, resp.StatusCode
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

// classifyUnprocessable maps a 422 message onto the two legitimate answers or
// a real failure. "already up to date" is matched first because GitHub's
// conflict text can also mention the base branch.
func classifyUnprocessable(message string) UpdateBranchState {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "already up to date"), strings.Contains(lower, "up-to-date"),
		strings.Contains(lower, "no update"):
		return StateAlreadyUpToDate
	case strings.Contains(lower, "merge conflict"), strings.Contains(lower, "conflict"),
		strings.Contains(lower, "not mergeable"):
		return StateMergeConflict
	}
	return StateFailed
}

// UpdateBranch calls PUT /repos/{o}/{r}/pulls/{n}/update-branch and re-reads the
// pull request. A 422 already-up-to-date or merge-conflict answer is returned
// as a structured state with a nil error; other non-2xx answers return an error.
func (c *Client) UpdateBranch(ctx context.Context, req UpdateBranchRequest) (*UpdateBranchResult, error) {
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		repo = c.defaultRepo()
	}
	if repo == "" {
		return nil, fmt.Errorf("%s requires repo (owner/name)", UpdateBranchToolName)
	}
	if req.Number <= 0 {
		return nil, fmt.Errorf("%s requires a positive pull request number", UpdateBranchToolName)
	}

	result := &UpdateBranchResult{
		Tool:            UpdateBranchToolName,
		Repo:            repo,
		Number:          req.Number,
		PreviousHeadSHA: strings.TrimSpace(req.ExpectedHeadSHA),
	}

	payload := map[string]string{}
	if result.PreviousHeadSHA != "" {
		payload["expected_head_sha"] = result.PreviousHeadSHA
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s body: %w", UpdateBranchToolName, err)
	}

	resp, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/update-branch", repo, req.Number), body)
	if err != nil {
		return nil, err
	}
	message, status := drainAPIResponse(resp)
	result.Message = message
	switch {
	case status == http.StatusAccepted, status == http.StatusOK:
		result.State = StateUpdated
	case status == http.StatusUnprocessableEntity:
		result.State = classifyUnprocessable(message)
		if result.State == StateFailed {
			return result, fmt.Errorf("github %s returned status %d: %s", UpdateBranchToolName, status, truncate(message, 300))
		}
	default:
		result.State = StateFailed
		return result, fmt.Errorf("github %s returned status %d: %s", UpdateBranchToolName, status, truncate(message, 300))
	}

	snapshot, readErr := c.PullSnapshot(ctx, repo, req.Number)
	if readErr != nil {
		result.Verified = false
		if result.State == StateUpdated || result.State == StateAlreadyUpToDate {
			result.VerificationError = fmt.Sprintf("the call was accepted but the pull request could not be re-read: %s", readErr.Error())
		}
		return result, nil
	}
	result.HeadSHA = snapshot.HeadSHA
	result.Mergeable = snapshot.Mergeable
	result.MergeStateStatus = snapshot.MergeStateStatus

	switch result.State {
	case StateUpdated:
		switch {
		case result.HeadSHA == "":
			result.Verified = false
			result.VerificationError = "re-read returned no head sha; the branch update could not be verified"
		case result.PreviousHeadSHA != "" && result.HeadSHA == result.PreviousHeadSHA:
			result.Verified = false
			result.VerificationError = fmt.Sprintf("head sha did not move (%s); the update-branch call did not actually rewrite the branch", result.HeadSHA)
		default:
			result.Verified = true
		}
	case StateAlreadyUpToDate:
		switch {
		case result.HeadSHA == "":
			result.Verified = false
			result.VerificationError = "re-read returned no head sha; the up-to-date answer could not be verified"
		case result.PreviousHeadSHA != "" && result.HeadSHA != result.PreviousHeadSHA:
			result.Verified = false
			result.VerificationError = fmt.Sprintf("github reported already up to date but the head moved from %s to %s", result.PreviousHeadSHA, result.HeadSHA)
		case strings.EqualFold(result.MergeStateStatus, "BEHIND"):
			result.Verified = false
			result.VerificationError = "github reported already up to date but the pull request still reads merge_state_status=BEHIND"
		default:
			result.Verified = true
		}
	case StateMergeConflict:
		result.Verified = false
		result.VerificationError = "base and head conflict; the server cannot update this branch, it needs a rebase or a human"
	}

	return result, nil
}

// PullSnapshot re-reads one pull request (GET /repos/{o}/{r}/pulls/{n}).
func (c *Client) PullSnapshot(ctx context.Context, repo string, number int) (*PullSnapshot, error) {
	if number <= 0 {
		return nil, fmt.Errorf("pull request number must be positive")
	}
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	if resp.StatusCode != http.StatusOK {
		message, _ := drainBytesMessage(raw)
		return nil, fmt.Errorf("re-reading pull request returned status %d: %s", resp.StatusCode, truncate(message, 300))
	}
	var payload struct {
		Number         int    `json:"number"`
		Mergeable      *bool  `json:"mergeable"`
		MergeableState string `json:"mergeable_state"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading pull request body: %w", readErr)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decoding pull request body: %w", err)
	}
	snapshot := &PullSnapshot{
		Number:         payload.Number,
		HeadSHA:        strings.TrimSpace(payload.Head.SHA),
		Mergeable:      payload.Mergeable,
		MergeableState: payload.MergeableState,
	}
	snapshot.MergeStateStatus = normalizeMergeStateStatus(payload.MergeableState, payload.Mergeable)
	return snapshot, nil
}

func drainBytesMessage(raw []byte) (string, error) {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw)), nil
	}
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = strings.TrimSpace(string(raw))
	}
	return message, nil
}

// normalizeMergeStateStatus upper-cases the REST mergeable_state the way the
// GraphQL mergeStateStatus reads, deriving CLEAN/DIRTY from mergeable when the
// state is not computed yet.
func normalizeMergeStateStatus(mergeableState string, mergeable *bool) string {
	state := strings.ToUpper(strings.TrimSpace(mergeableState))
	if state == "" && mergeable != nil {
		if *mergeable {
			return "CLEAN"
		}
		return "DIRTY"
	}
	if state == "" {
		return "UNKNOWN"
	}
	return state
}

// JSON renders the result for the tool payload.
func (r *UpdateBranchResult) JSON() (string, error) {
	encoded, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding %s result: %w", UpdateBranchToolName, err)
	}
	return string(encoded), nil
}
