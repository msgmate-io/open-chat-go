package bots

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ci#622: the review role's self-check (review_submitted) needs a post action
// that submits a pull request review, not an issue comment.

func TestNormalizeReviewEventDefaultsToComment(t *testing.T) {
	cases := map[string]string{
		"":                 reviewEventComment,
		"   ":              reviewEventComment,
		"comment":          reviewEventComment,
		"something else":   reviewEventComment,
		"approve":          reviewEventApprove,
		"APPROVE":          reviewEventApprove,
		" request_changes": reviewEventRequestChanges,
		"Request_Changes":  reviewEventRequestChanges,
	}
	for in, want := range cases {
		if got := NormalizeReviewEvent(in); got != want {
			t.Fatalf("NormalizeReviewEvent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodePostActionPullRequestReview(t *testing.T) {
	raw := map[string]interface{}{
		"kind":   postActionKindPullRequestReview,
		"repo":   "msgmate-io/open-chat-go",
		"number": float64(42),
		"event":  "request_changes",
	}
	action, ok := decodePostActionPullRequestReview(raw)
	if !ok {
		t.Fatal("expected the review action to decode")
	}
	if got := reviewTargetEndpoint(action, ""); got != "https://api.github.com/repos/msgmate-io/open-chat-go/pulls/42/reviews" {
		t.Fatalf("unexpected endpoint %q", got)
	}

	// A review without any pull request target is rejected.
	if _, ok := decodePostActionPullRequestReview(map[string]interface{}{"kind": postActionKindPullRequestReview}); ok {
		t.Fatal("a target-less review action must be rejected")
	}
}

func TestReviewTargetEndpointPrefersPullURLAndHonoursAPIBase(t *testing.T) {
	pull := "https://github.com/msgmate-io/open-chat-go/pull/42/"
	action := postActionPullRequestReview{Repo: "msgmate-io/open-chat-go", Pull: &pull}
	if got := reviewTargetEndpoint(action, "http://127.0.0.1:9999"); got != "http://127.0.0.1:9999/repos/msgmate-io/open-chat-go/pulls/42/reviews" {
		t.Fatalf("unexpected endpoint with api base %q", got)
	}
	// A URL that is not a pull request URL is ignored in favour of repo+number.
	junk := "https://github.com/msgmate-io/open-chat-go/issues/7"
	action = postActionPullRequestReview{Repo: "o/n", Number: 3, Pull: &junk}
	if got := reviewTargetEndpoint(action, ""); got != "https://api.github.com/repos/o/n/pulls/3/reviews" {
		t.Fatalf("unexpected endpoint %q", got)
	}
}

func TestSubmitPullRequestReviewPostsBodyAndEvent(t *testing.T) {
	var got map[string]string
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	header := "## My review"
	event := "APPROVE"
	action := postActionPullRequestReview{
		Repo:   "msgmate-io/open-chat-go",
		Number: 42,
		Event:  &event,
		Header: &header,
	}
	if err := submitPullRequestReviewTo(server.URL, "token", action, "looks good"); err != nil {
		t.Fatalf("submitPullRequestReviewTo failed: %v", err)
	}
	if path != "/repos/msgmate-io/open-chat-go/pulls/42/reviews" {
		t.Fatalf("unexpected path %q", path)
	}
	if got["event"] != reviewEventApprove {
		t.Fatalf("event = %q, want APPROVE", got["event"])
	}
	if got["body"] != "## My review\n\nlooks good" {
		t.Fatalf("unexpected body %q", got["body"])
	}
}

func TestRunPullRequestReviewNeedsToken(t *testing.T) {
	previous, had := os.LookupEnv(postGithubTokenEnv)
	_ = os.Unsetenv(postGithubTokenEnv)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(postGithubTokenEnv, previous)
		}
	})
	if err := runPullRequestReview(postActionPullRequestReview{Repo: "o/n", Number: 1}, "text"); err == nil {
		t.Fatal("expected an error when no github token is configured")
	}
}

// TestCI927ReviewPublishesAsPullRequestReview is the ci#927 (parent ci#622)
// self-check pin: the review bot's durable output is a pull request review on
// the fixture PR, never an issue comment. The self-check dispatch for the
// `review` role must attach a `github_pull_request_review` post action with an
// explicit verdict; this test pins the endpoint and the explicit-event
// contract that dispatch depends on.
func TestCI927ReviewPublishesAsPullRequestReview(t *testing.T) {
	if got := NormalizeReviewEvent(""); got != reviewEventComment {
		t.Fatalf("NormalizeReviewEvent(\"\") = %q, want %q", got, reviewEventComment)
	}

	raw := map[string]interface{}{
		"kind":   postActionKindPullRequestReview,
		"repo":   "msgmate-io/open-chat-go",
		"number": float64(927),
		"event":  "APPROVE",
	}
	action, ok := decodePostActionPullRequestReview(raw)
	if !ok {
		t.Fatal("the self-check review action must decode as a pull request review")
	}
	if got := reviewTargetEndpoint(action, ""); got != "https://api.github.com/repos/msgmate-io/open-chat-go/pulls/927/reviews" {
		t.Fatalf("review endpoint = %q, want the /pulls/927/reviews endpoint", got)
	}
}
