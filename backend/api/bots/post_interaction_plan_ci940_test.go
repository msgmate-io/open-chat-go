package bots

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression pins for the ci#940 native-coder self-check suite (ci#622). The
// `plan` role of that suite failed with `plan_comment_posted` — the check
// asserts the plan bot posts a plan comment on the fixture issue. The core
// contract the check depends on is the plan post-interaction action
// (ci#649), the same executor that turns the `review` role's finished turn
// into a review on the fixture PR (post_interaction_review.go, ci#622).
//
// The default `plan_auto_implement` plan comment body — header + finished
// plan text — must be what runPlanAutoImplement posts (via the shared
// github_issue_comment path), so the plan self-check sees the plan comment
// land on the fixture issue.

// TestCI940PlanCommentBodyMatchesPlanImplementHeader pins the plan comment
// header the plan role's `plan_auto_implement` action posts: it must be the
// dedicated `## Implementation plan` header, not the generic interaction
// report header.
func TestCI940PlanCommentBodyMatchesPlanImplementHeader(t *testing.T) {
	body := composePostCommentBody(postActionGithubComment{}, "step 1: do the thing", postActionPlanImplementHeader)
	if !strings.Contains(body, postActionPlanImplementHeader) {
		t.Fatalf("plan comment body must carry the plan header: %q", body)
	}
	if !strings.Contains(body, "step 1: do the thing") {
		t.Fatalf("plan comment body must carry the plan text: %q", body)
	}
	if strings.Contains(body, postActionDefaultReportHeader) {
		t.Fatalf("plan comment body must not use the generic report header: %q", body)
	}
}

// TestCI940PlanAutoImplementPostsThePlanComment pins the delivery half of the
// check: the plan role's `plan_auto_implement` action posts the finished plan
// as a comment on the fixture issue, exactly like the shared github_issue_comment
// path (same OPEN_CHAT_GITHUB_POST_TOKEN credential, same
// /repos/{owner}/{repo}/issues/{n}/comments endpoint).
func TestCI940PlanAutoImplementPostsThePlanComment(t *testing.T) {
	posted := false
	plan := "implement the self-check fixture"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted = true
		if r.Method != http.MethodPost {
			t.Errorf("plan comment request method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/repos/msgmate-io/ci-940-fixture/issues/940/comments") {
			t.Errorf("plan comment request path = %s, want the fixture issue comments endpoint", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("plan comment authorization header = %q, want the post token", auth)
		}
		decoded := map[string]string{}
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode plan comment request body: %v", err)
			return
		}
		if !strings.Contains(decoded["body"], plan) {
			t.Errorf("plan comment body %q must carry the finished plan", decoded["body"])
		}
		if !strings.Contains(decoded["body"], postActionPlanImplementHeader) {
			t.Errorf("plan comment body %q must carry the plan header", decoded["body"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 1}`))
	}))
	defer server.Close()

	action := postActionPlanAutoImplement{
		Repo:   "msgmate-io/ci-940-fixture",
		Number: 940,
		Bot:    "msgmate-coder-plan-bot",
	}
	if err := postPlanAutoImplementCommentTo(server.URL, "test-token", action, plan); err != nil {
		t.Fatalf("plan comment post failed: %v", err)
	}
	if !posted {
		t.Fatal("the plan comment endpoint was never hit")
	}
}

// TestCI940PlanCommentBodyCarriesThePlanAndPlanHeader pins the composed body
// the plan post-action submits: the finished plan text plus the plan header,
// never the generic report header. This is the exact body the plan role's
// plan comment carries on the fixture issue.
func TestCI940PlanCommentBodyCarriesThePlanAndPlanHeader(t *testing.T) {
	body := composePostCommentBody(postActionGithubComment{}, "the finished plan", postActionPlanImplementHeader)
	if !strings.Contains(body, postActionPlanImplementHeader) || !strings.Contains(body, "the finished plan") {
		t.Fatalf("unexpected plan comment body: %q", body)
	}
}

// TestCI940SelfCheckActionKindIsPlanAutoImplement pins that the self-check's
// plan-role post action (the configuration surface the dispatch attaches) is a
// plan_auto_implement action, which the executor's loadChatPostActions keeps.
func TestCI940SelfCheckActionKindIsPlanAutoImplement(t *testing.T) {
	encoded, err := json.Marshal(map[string]interface{}{
		"post_action": map[string]interface{}{
			"kind":   "plan_auto_implement",
			"repo":   "msgmate-io/ci-940-fixture",
			"number": 940,
			"bot":    "msgmate-coder-plan-bot",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	actions := loadChatPostActions(encoded)
	if len(actions) != 1 {
		t.Fatalf("self-check plan action must be kept, got %#v", actions)
	}
	if kind, _ := actions[0]["kind"].(string); kind != "plan_auto_implement" {
		t.Fatalf("self-check plan action kind = %q, want plan_auto_implement", kind)
	}
}
