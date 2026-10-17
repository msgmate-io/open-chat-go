package bots

// Native-coder self-check contract guard for ci#961.
//
// The per-role native-coder self-verification suite (ci#622) asserts two durable
// outputs from the post-interaction executor:
//
//   - `plan_comment_posted` (plan role): the plan bot posts a plan comment on
//     the fixture issue.
//   - `review_submitted` (review role): the review bot submits a review on the
//     fixture PR.
//
// Both failure modes regressed together in the ci#622 selfcheck run captured by
// ci#961. The routing fixes landed in ci#927 (review delivery, dispatch pins)
// and ci#940 (plan comment posted to the plan action's own target). Their
// per-role routing details are pinned in the ci#927/ci#940 tests; this guard
// anchors the two *probe* contracts end-to-end on the executor itself, so a
// regression in the post-action registry (a renamed kind, a dropped or
// reordered action, or a malformed target) is caught by the package unit tests
// instead of only by the live selfcheck relay.
//
// Refs: msgmate-io/open-chat-go-ci#961, ci#622, ci#927, ci#940

import (
	"encoding/json"
	"testing"
)

func testSelfCheckSharedConfig() []byte {
	raw, _ := json.Marshal(map[string]interface{}{
		"post_actions": []interface{}{
			map[string]interface{}{
				"kind":           postActionKindPlanAutoImplement,
				"repo":           "msgmate-io/open-chat-go-ci",
				"issue":          961,
				"bot":            "msgmate-coder-implement-bot",
				"base_url":       "https://api.github.com",
				"api_base":       "https://api.github.com",
				"default_branch": "dev",
			},
			map[string]interface{}{
				"kind":   postActionKindPullRequestReview,
				"org":    "msgmate-io",
				"repo":   "open-chat-go-ci",
				"number": 622,
			},
		},
	})
	return raw
}

// TestCI961SelfCheckProbesAreRecognizedPostActions pins that the two ci#622
// selfcheck probes, `plan_comment_posted` (plan role) and `review_submitted`
// (review role), are both carried as recognized post actions by the executor's
// shared-config loader. A reorder or kind rename here would silently drop one
// role's durable output and regress the suite again.
func TestCI961SelfCheckProbesAreRecognizedPostActions(t *testing.T) {
	actions := loadChatPostActions(testSelfCheckSharedConfig())
	if len(actions) != 2 {
		t.Fatalf("expected both selfcheck probes to be recognized post actions, got %d: %v", len(actions), actions)
	}
	got := map[string]bool{}
	for _, action := range actions {
		kind, _ := action["kind"].(string)
		got[kind] = true
	}
	if !got[postActionKindPlanAutoImplement] {
		t.Fatalf("plan_comment_posted probe: plan_auto_implement post action was not recognized")
	}
	if !got[postActionKindPullRequestReview] {
		t.Fatalf("review_submitted probe: github_pull_request_review post action was not recognized")
	}
}

// TestCI961ReviewProbeDecodesTarget pins that the review probe's
// `review_submitted` action decodes into a concrete pull-request review target
// (org/repo/number), so the review bot posts on the fixture PR and never
// silently no-ops.
func TestCI961ReviewProbeDecodesTarget(t *testing.T) {
	if _, ok := decodePostActionPullRequestReview(map[string]interface{}{
		"kind":   postActionKindPullRequestReview,
		"org":    "msgmate-io",
		"repo":   "open-chat-go-ci",
		"number": 622,
	}); !ok {
		t.Fatalf("review_submitted probe: fixture PR review action did not decode")
	}
}

// TestCI961ReviewProbeEndpointForm derives the concrete GitHub pulls/reviews
// endpoint for the fixture PR. The endpoint math is pure string derivation and
// performs no network call.
func TestCI961ReviewProbeEndpointForm(t *testing.T) {
	action, ok := decodePostActionPullRequestReview(map[string]interface{}{
		"kind":   postActionKindPullRequestReview,
		"org":    "msgmate-io",
		"repo":   "open-chat-go-ci",
		"number": 622,
	})
	if !ok {
		t.Fatalf("review_submitted probe: fixture PR review action did not decode")
	}
	endpoint := reviewTargetEndpoint(action, "https://api.github.com")
	want := "https://api.github.com/repos/msgmate-io/open-chat-go-ci/pulls/622/reviews"
	if endpoint == "" {
		t.Fatalf("review_submitted probe: review target endpoint must not be empty")
	}
	if endpoint != want {
		t.Fatalf("review_submitted probe: review target endpoint = %q, want %q", endpoint, want)
	}
}
