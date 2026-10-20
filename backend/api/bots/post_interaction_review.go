package bots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// This file adds the missing post-interaction capability of the review role
// (ci#622): the native coder self-verification suite asserts `review_submitted`
// — the review bot's durable output is a *review* on the fixture pull request.
//
// The post-interaction executor (ci#649) could only turn a finished interaction
// into a GitHub *issue comment* (`github_issue_comment`) or into a plan comment
// plus implementation dispatch (`plan_auto_implement`). A review bot therefore
// finished its turn with the review text living only inside the chat and the
// fixture PR kept zero reviews, so the check could never pass no matter how well
// the agent behaved.
//
// `github_pull_request_review` closes that gap: it submits the interaction's
// final message as a pull-request review (`POST /repos/{owner}/{repo}/pulls/{n}/reviews`).
// The verdict is explicit config, never inferred from the reply text: a review
// that silently approved a pull request because the body happened to sound
// positive would be a correctness bug, so an unset/invalid event normalizes to
// `COMMENT` and only an explicit `APPROVE`/`REQUEST_CHANGES` changes the PR's
// review state.
//
// The action is intentionally part of the same registry as the other post
// actions: it is attached by the dispatch/trigger that scoped the interaction to
// a pull request, so the core needs no per-role knowledge of the review bot.

const (
	// postActionKindPullRequestReview submits the final message as a PR review.
	postActionKindPullRequestReview = "github_pull_request_review"

	// reviewEventComment is the default verdict: it publishes the review body
	// without approving or blocking the pull request.
	reviewEventComment = "COMMENT"
	// reviewEventApprove approves the pull request.
	reviewEventApprove = "APPROVE"
	// reviewEventRequestChanges requests changes (blocks the merge).
	reviewEventRequestChanges = "REQUEST_CHANGES"

	postActionReviewHeader = "<!-- open-chat-post-interaction -->\n## Code review"
)

// postActionPullRequestReview is the decoded github_pull_request_review action.
type postActionPullRequestReview struct {
	Repo string `json:"repo"`
	// Number is the pull request number.
	Number int `json:"number"`
	// Pull is an optional full pull-request API/web URL; it is used instead of
	// repo+number, mirroring the issue_url alternative of a comment action.
	Pull *string `json:"pull_url,omitempty"`
	// Event is the review verdict: APPROVE, REQUEST_CHANGES or COMMENT.
	Event *string `json:"event,omitempty"`
	// CommitID optionally pins the review to a specific head commit, so a review
	// that raced a push is not silently attributed to a newer commit.
	CommitID string  `json:"commit_id,omitempty"`
	Header   *string `json:"header,omitempty"`
	Footer   *string `json:"footer,omitempty"`
	MaxLen   *int    `json:"max_len,omitempty"`
}

// NormalizeReviewEvent coerces a raw verdict to one of the three GitHub review
// events. Anything that is not an explicit approval or change request — an empty
// value included — normalizes to COMMENT, so a review never changes the pull
// request's merge state by accident.
func NormalizeReviewEvent(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case reviewEventApprove:
		return reviewEventApprove
	case reviewEventRequestChanges:
		return reviewEventRequestChanges
	default:
		return reviewEventComment
	}
}

// decodePostActionPullRequestReview decodes a review action and validates that
// it names a pull request target.
func decodePostActionPullRequestReview(raw map[string]interface{}) (postActionPullRequestReview, bool) {
	blob, err := json.Marshal(raw)
	if err != nil {
		return postActionPullRequestReview{}, false
	}
	var action postActionPullRequestReview
	if err := json.Unmarshal(blob, &action); err != nil {
		return postActionPullRequestReview{}, false
	}
	if reviewTargetEndpoint(action, "") == "" {
		return postActionPullRequestReview{}, false
	}
	return action, true
}

// reviewTargetEndpoint derives the review collection endpoint of a review
// action. An explicit `pull_url` wins; otherwise repo+number is used. A non-empty
// apiBase overrides the public GitHub API host (used by tests).
func reviewTargetEndpoint(action postActionPullRequestReview, apiBase string) string {
	base := strings.TrimSuffix(strings.TrimSpace(apiBase), "/")
	repo := strings.Trim(strings.TrimSpace(action.Repo), "/")
	if number := pullNumber(action.Pull); number != 0 {
		if repo == "" {
			// A bare pull URL carries the repository itself (web or API form).
			return reviewEndpointFromPullURL(*action.Pull, base, number)
		}
		return reviewsEndpoint(base, repo, number)
	}
	if repo == "" || action.Number <= 0 {
		return ""
	}
	return reviewsEndpoint(base, repo, action.Number)
}

// reviewsEndpoint renders the review collection endpoint of one pull request.
func reviewsEndpoint(apiBase, repo string, number int) string {
	base := strings.TrimSuffix(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	return fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", base, repo, number)
}

// reviewEndpointFromPullURL derives the endpoint from a self-contained pull
// request URL: an API URL is used as-is, a web URL is rebased onto the API host.
func reviewEndpointFromPullURL(rawURL, apiBase string, number int) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(rawURL), "/")
	if strings.Contains(trimmed, "api.github.com/repos/") {
		return trimmed + "/reviews"
	}
	base := strings.TrimSuffix(strings.TrimSpace(apiBase), "/")
	repo := repoFromPullURL(trimmed)
	if base == "" || repo == "" {
		if base == "" {
			base = "https://api.github.com"
		}
		if repo == "" {
			return ""
		}
	}
	return fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", base, repo, number)
}

// repoFromPullURL extracts owner/repo from a GitHub web pull request URL.
func repoFromPullURL(rawURL string) string {
	marker := "github.com/"
	idx := strings.Index(rawURL, marker)
	if idx < 0 {
		return ""
	}
	parts := strings.Split(strings.Trim(rawURL[idx+len(marker):], "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// pullNumber reports the pull request number encoded in a pull request URL
// (either the web `/pull/N` or the API `/pulls/N` form). It returns 0 when the
// value does not identify a single pull request.
func pullNumber(raw *string) int {
	if raw == nil {
		return 0
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return 0
	}
	for _, marker := range []string{"/pulls/", "/pull/"} {
		idx := strings.Index(value, marker)
		if idx < 0 {
			continue
		}
		rest := strings.Trim(value[idx+len(marker):], "/")
		number := 0
		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}
			number = number*10 + int(r-'0')
		}
		if number != 0 {
			return number
		}
	}
	return 0
}

// runPullRequestReview submits the finished interaction as a pull request review.
func runPullRequestReview(action postActionPullRequestReview, replyText string) error {
	token := strings.TrimSpace(os.Getenv(postGithubTokenEnv))
	if token == "" {
		return fmt.Errorf("%s is not configured; skipping pull request review post action", postGithubTokenEnv)
	}
	return submitPullRequestReview(token, action, replyText)
}

// submitPullRequestReview posts the review to GitHub.
func submitPullRequestReview(token string, action postActionPullRequestReview, replyText string) error {
	return submitPullRequestReviewTo("", token, action, replyText)
}

// submitPullRequestReviewTo posts the review to GitHub, letting tests point the
// call at a local API base instead of the public host.
func submitPullRequestReviewTo(apiBase, token string, action postActionPullRequestReview, replyText string) error {
	endpoint := reviewTargetEndpoint(action, apiBase)
	if endpoint == "" {
		return fmt.Errorf("no github pull request could be derived from the post action")
	}
	body := composePostCommentBody(postActionGithubComment{
		Header: action.Header,
		Footer: action.Footer,
		MaxLen: action.MaxLen,
	}, replyText, postActionReviewHeader)

	payload := map[string]string{
		"body":  body,
		"event": NormalizeReviewEvent(stringValuePtr(action.Event)),
	}
	if commit := strings.TrimSpace(action.CommitID); commit != "" {
		payload["commit_id"] = commit
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: postGithubCommentTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github api returned status %d submitting pull request review", resp.StatusCode)
	}
	return nil
}

// stringValuePtr dereferences an optional string.
func stringValuePtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
