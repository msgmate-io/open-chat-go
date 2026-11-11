package bots

import (
	"encoding/json"
	"testing"

	"backend/database"
)

// ci#1049: the trigger issue is derived from the dispatch tags (with the
// post-action target as fallback) and github_issue_comment is granted only when
// such a binding exists.

// mustJSON encodes value as the chat shared-config payload the pin lookup reads.
func mustJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %#v: %v", value, err)
	}
	return raw
}

func TestResolveTriggerIssueBindingFromTags(t *testing.T) {
	binding, ok := resolveTriggerIssueBinding([]string{"repo:msgmate-io/open-chat-go-ci", "issue:1049"}, nil)
	if !ok {
		t.Fatal("expected the dispatch tags to resolve a trigger issue")
	}
	if binding.Repo != "msgmate-io/open-chat-go-ci" || binding.IssueNumber != 1049 {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestResolveTriggerIssueBindingFallsBackToPostAction(t *testing.T) {
	config := map[string]interface{}{
		"post_action": map[string]interface{}{
			"kind":   "github_issue_comment",
			"repo":   "msgmate-io/open-chat-go-ci",
			"number": 940,
		},
	}
	binding, ok := resolveTriggerIssueBinding(nil, config)
	if !ok {
		t.Fatal("expected the post action to resolve a trigger issue")
	}
	if binding.Repo != "msgmate-io/open-chat-go-ci" || binding.IssueNumber != 940 {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestResolveTriggerIssueBindingPrefersIssueURL(t *testing.T) {
	config := map[string]interface{}{
		"post_actions": []interface{}{
			map[string]interface{}{
				"kind":      "github_issue_comment",
				"issue_url": "https://github.com/msgmate-io/open-chat-go-ci/issues/1049",
			},
		},
	}
	binding, ok := resolveTriggerIssueBinding(nil, config)
	if !ok {
		t.Fatal("expected the issue_url to resolve a trigger issue")
	}
	if binding.Repo != "msgmate-io/open-chat-go-ci" || binding.IssueNumber != 1049 {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestResolveTriggerIssueBindingFailsClosed(t *testing.T) {
	if binding, ok := resolveTriggerIssueBinding(nil, nil); ok {
		t.Fatalf("expected no binding without tags or post actions, got %#v", binding)
	}
	// A number without a repo is not a usable binding.
	if binding, ok := resolveTriggerIssueBinding([]string{"issue:1049"}, nil); ok {
		t.Fatalf("expected no binding without a repo, got %#v", binding)
	}
}

func TestApplyTriggerIssuePinGrantPinsAndGrants(t *testing.T) {
	config := map[string]interface{}{
		"tools": []interface{}{"github_issue_view"},
		"tool_init": map[string]interface{}{
			"github_issue_comment": map[string]interface{}{
				"repo":         "attacker/elsewhere",
				"issue_number": 1,
			},
		},
	}
	tags := []string{"repo:msgmate-io/open-chat-go-ci", "issue:1049"}
	out := applyTriggerIssuePinGrant(config, tags)

	pin, ok := database.ToolInitPinsForChat(&database.Chat{
		SharedConfig: &database.SharedChatConfig{ConfigData: mustJSON(t, out[database.ToolInitPinsKey])},
	})["github_issue_comment"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a persisted pin, got %#v", out[database.ToolInitPinsKey])
	}
	if pin["repo"] != "msgmate-io/open-chat-go-ci" || pin["issue_number"] != 1049 {
		t.Fatalf("unexpected pin: %#v", pin)
	}

	// The client supplied tool_init is overwritten by the pinned target.
	toolInit := out["tool_init"].(map[string]interface{})["github_issue_comment"].(map[string]interface{})
	if toolInit["repo"] != "msgmate-io/open-chat-go-ci" || toolInit["issue_number"] != 1049 {
		t.Fatalf("expected tool_init to be pinned, got %#v", toolInit)
	}

	// The tool is granted on top of the declared list.
	names, err := collectToolNames(out["tools"])
	if err != nil {
		t.Fatalf("failed to read tools: %v", err)
	}
	granted := false
	for _, name := range names {
		if name == pinnedIssueCommentTool {
			granted = true
		}
	}
	if !granted {
		t.Fatalf("expected %s to be granted, got %#v", pinnedIssueCommentTool, names)
	}

	// The input config is not mutated.
	if config["tool_init"].(map[string]interface{})["github_issue_comment"].(map[string]interface{})["repo"] != "attacker/elsewhere" {
		t.Fatal("applyTriggerIssuePinGrant mutated its input")
	}
	if _, exists := config[database.ToolInitPinsKey]; exists {
		t.Fatal("applyTriggerIssuePinGrant wrote a pin into its input")
	}
}

func TestApplyTriggerIssuePinGrantFailsClosed(t *testing.T) {
	config := map[string]interface{}{
		"tools": []interface{}{"github_issue_view"},
	}
	out := applyTriggerIssuePinGrant(config, nil)
	if _, exists := out[database.ToolInitPinsKey]; exists {
		t.Fatalf("expected no pin without a trigger issue, got %#v", out[database.ToolInitPinsKey])
	}
	names, err := collectToolNames(out["tools"])
	if err != nil {
		t.Fatalf("failed to read tools: %v", err)
	}
	for _, name := range names {
		if name == pinnedIssueCommentTool {
			t.Fatalf("%s must not be granted without a pin, got %#v", pinnedIssueCommentTool, names)
		}
	}
}

func TestIssueBindingFromURL(t *testing.T) {
	cases := map[string]struct {
		repo   string
		number int
		ok     bool
	}{
		"https://github.com/msgmate-io/open-chat-go-ci/issues/1049":          {"msgmate-io/open-chat-go-ci", 1049, true},
		"https://api.github.com/repos/msgmate-io/open-chat-go-ci/issues/940": {"msgmate-io/open-chat-go-ci", 940, true},
		"https://github.com/msgmate-io/open-chat-go-ci/pull/12":              {"", 0, false},
		"": {"", 0, false},
	}
	for raw, want := range cases {
		binding, ok := issueBindingFromURL(raw)
		if ok != want.ok {
			t.Fatalf("%q: expected ok=%v, got %v", raw, want.ok, ok)
		}
		if ok && (binding.Repo != want.repo || binding.IssueNumber != want.number) {
			t.Fatalf("%q: unexpected binding %#v", raw, binding)
		}
	}
}
