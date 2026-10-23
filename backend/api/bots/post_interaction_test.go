package bots

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadChatPostActions(t *testing.T) {
	config := map[string]interface{}{
		"post_action": map[string]interface{}{
			"kind":   "github_issue_comment",
			"repo":   "msgmate-io/open-chat-go-ci",
			"number": 649,
		},
		"post_actions": []interface{}{
			map[string]interface{}{"kind": "plan_auto_implement", "repo": "msgmate-io/open-chat-go-ci", "number": 649, "bot": "msgmate-coder-repl-bot"},
			map[string]interface{}{"kind": "something_else", "repo": "o/n", "number": 1},
		},
	}
	encoded, _ := json.Marshal(config)
	actions := loadChatPostActions(encoded)
	if len(actions) != 2 {
		t.Fatalf("expected the two known post-actions, got %#v", actions)
	}
	if actions[0]["kind"] != "github_issue_comment" || actions[1]["kind"] != "plan_auto_implement" {
		t.Fatalf("unexpected order/kinds: %#v", actions)
	}
}

func TestLoadChatPostActionsIgnoresMalformedConfig(t *testing.T) {
	if got := loadChatPostActions([]byte("{not json")); got != nil {
		t.Fatalf("malformed config must yield no actions, got %#v", got)
	}
	if got := loadChatPostActions([]byte(`{"post_actions":[{"kind":"unknown"}]}`)); len(got) != 0 {
		t.Fatalf("unknown kinds must be filtered, got %#v", got)
	}
}

func TestDecodePostActionGithubComment(t *testing.T) {
	if _, ok := decodePostActionGithubComment(map[string]interface{}{"kind": "github_issue_comment"}); ok {
		t.Fatal("an action without a repo/number or issue_url must be rejected")
	}
	issue := "https://github.com/o/n/issues/7"
	if _, ok := decodePostActionGithubComment(map[string]interface{}{"issue_url": issue}); !ok {
		t.Fatal("an issue_url-only action must be accepted")
	}
	action, ok := decodePostActionGithubComment(map[string]interface{}{"repo": "o/n", "number": float64(3)})
	if !ok || action.Repo != "o/n" || action.Number != 3 {
		t.Fatalf("unexpected decode: %#v ok=%v", action, ok)
	}
}

func TestComposePostCommentBody(t *testing.T) {
	action := postActionGithubComment{}
	body := composePostCommentBody(action, "the plan", postActionDefaultReportHeader)
	if !strings.Contains(body, postActionDefaultReportHeader) || !strings.Contains(body, "the plan") {
		t.Fatalf("unexpected body: %q", body)
	}

	header := "## Implementation plan"
	footer := "_posted by open-chat_"
	action = postActionGithubComment{Header: &header, Footer: &footer}
	body = composePostCommentBody(action, "plan text", postActionDefaultReportHeader)
	if !strings.HasPrefix(body, "## Implementation plan\n\nplan text") || !strings.HasSuffix(body, footer) {
		t.Fatalf("header/footer not applied: %q", body)
	}

	max := 5
	action = postActionGithubComment{MaxLen: &max}
	body = composePostCommentBody(action, "0123456789", postActionDefaultReportHeader)
	if !strings.Contains(body, "01234") || !strings.Contains(body, postActionTruncationNoticeSuffix) {
		t.Fatalf("max_len truncation not applied: %q", body)
	}
}

func TestDecodePostActionPlanAutoImplement(t *testing.T) {
	raw := map[string]interface{}{
		"kind":                 "plan_auto_implement",
		"repo":                 "msgmate-io/open-chat-go-ci",
		"number":               float64(649),
		"bot":                  "msgmate-coder-repl-bot",
		"require_confirmation": false,
	}
	action, ok := decodePostActionPlanAutoImplement(raw)
	if !ok || action.Bot != "msgmate-coder-repl-bot" || action.Number != 649 {
		t.Fatalf("unexpected decode: %#v ok=%v", action, ok)
	}
	if action.RequireConfirmation == nil || *action.RequireConfirmation {
		t.Fatalf("require_confirmation should have decoded to false: %#v", action.RequireConfirmation)
	}
}

func TestBuildPlanImplementationPromptCarriesThePlan(t *testing.T) {
	issue := "https://github.com/msgmate-io/open-chat-go-ci/issues/649"
	action := postActionPlanAutoImplement{Repo: "msgmate-io/open-chat-go-ci", Number: 649, Issue: &issue, Bot: "b"}
	prompt := buildPlanImplementationPrompt(action, "step 1: do the thing")
	if !strings.Contains(prompt, "msgmate-io/open-chat-go-ci") || !strings.Contains(prompt, "649") {
		t.Fatalf("prompt must carry the repo and issue: %q", prompt)
	}
	if !strings.Contains(prompt, "step 1: do the thing") {
		t.Fatalf("prompt must carry the plan verbatim: %q", prompt)
	}
}
