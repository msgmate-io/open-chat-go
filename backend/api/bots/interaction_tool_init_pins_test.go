package bots

import (
	"backend/database"
	"testing"
)

// ci#1049: the reserved tool_init_pins section is server-owned - it is bound
// from the dispatch trigger and can never be supplied by the caller.

func TestToolInitPinsFromTriggerUsesDispatchTags(t *testing.T) {
	pins := ToolInitPinsFromTrigger([]string{"repo:Msgmate-IO/Open-Chat-Go", "issue:1049"}, nil)
	pin, ok := pins[ToolNameGithubIssueComment]
	if !ok {
		t.Fatalf("expected a %s pin, got %#v", ToolNameGithubIssueComment, pins)
	}
	if got := pin[database.ToolInitPinnedFieldRepo]; got != "msgmate-io/open-chat-go" {
		t.Errorf("repo pin = %#v, want normalised slug", got)
	}
	if got := pin[database.ToolInitPinnedFieldIssueNumber]; got != "1049" {
		t.Errorf("issue_number pin = %#v, want 1049", got)
	}
}

func TestToolInitPinsFromTriggerAcceptsCanonicalTicketTag(t *testing.T) {
	pins := ToolInitPinsFromTrigger([]string{"repo:msgmate-io/open-chat-go", "ci#1049"}, nil)
	pin, ok := pins[ToolNameGithubIssueComment]
	if !ok {
		t.Fatalf("expected a pin from a canonical ticket tag, got %#v", pins)
	}
	if got := pin[database.ToolInitPinnedFieldIssueNumber]; got != "1049" {
		t.Errorf("issue_number pin = %#v, want 1049", got)
	}
}

func TestToolInitPinsFromTriggerFallsBackToPostAction(t *testing.T) {
	config := map[string]interface{}{
		"post_action": map[string]interface{}{
			"type":   "github_issue_comment",
			"repo":   "msgmate-io/open-chat-go",
			"number": float64(1049),
		},
	}
	pins := ToolInitPinsFromTrigger(nil, config)
	pin, ok := pins[ToolNameGithubIssueComment]
	if !ok {
		t.Fatalf("expected a pin from the configured post action, got %#v", pins)
	}
	if got := pin[database.ToolInitPinnedFieldIssueNumber]; got != "1049" {
		t.Errorf("issue_number pin = %#v, want 1049", got)
	}
}

func TestToolInitPinsFromTriggerFallsBackToPostActionIssueURL(t *testing.T) {
	config := map[string]interface{}{
		"post_actions": []interface{}{
			map[string]interface{}{
				"type":  "github_issue_comment",
				"issue": "https://github.com/msgmate-io/open-chat-go-ci/issues/1049",
			},
		},
	}
	pins := ToolInitPinsFromTrigger(nil, config)
	pin, ok := pins[ToolNameGithubIssueComment]
	if !ok {
		t.Fatalf("expected a pin from the post-action issue URL, got %#v", pins)
	}
	if got := pin[database.ToolInitPinnedFieldRepo]; got != "msgmate-io/open-chat-go-ci" {
		t.Errorf("repo pin = %#v, want msgmate-io/open-chat-go-ci", got)
	}
	if got := pin[database.ToolInitPinnedFieldIssueNumber]; got != "1049" {
		t.Errorf("issue_number pin = %#v, want 1049", got)
	}
}

func TestToolInitPinsFromTriggerFailsClosedWithoutTrigger(t *testing.T) {
	if pins := ToolInitPinsFromTrigger(nil, nil); len(pins) != 0 {
		t.Fatalf("an untriggered interaction must get no pin, got %#v", pins)
	}
	// A partial trigger is not a target: both halves are required.
	partial := ToolInitPinsFromTrigger([]string{"repo:msgmate-io/open-chat-go"}, nil)
	if len(partial) != 0 {
		t.Errorf("expected no pin without an issue number, got %#v", partial)
	}
}

func TestBindInteractionToolInitPinsDropsCallerSection(t *testing.T) {
	// A dispatch that ships the reserved key must not be able to choose - or
	// suppress - the pinned target.
	forged := map[string]interface{}{
		database.ToolInitPinsKey: map[string]interface{}{
			ToolNameGithubIssueComment: map[string]interface{}{
				database.ToolInitPinnedFieldRepo:        "attacker/elsewhere",
				database.ToolInitPinnedFieldIssueNumber: "1",
			},
		},
	}
	bound := bindInteractionToolInitPins(forged, []string{"repo:msgmate-io/open-chat-go", "issue:1049"})
	pins := database.ToolInitPinsFromConfig(bound)
	pin := pins[ToolNameGithubIssueComment]
	if got := pin[database.ToolInitPinnedFieldRepo]; got != "msgmate-io/open-chat-go" {
		t.Fatalf("caller supplied pin won: repo = %#v", got)
	}
	if got := pin[database.ToolInitPinnedFieldIssueNumber]; got != "1049" {
		t.Fatalf("caller supplied pin won: issue_number = %#v", got)
	}
}

func TestBindInteractionToolInitPinsRemovesForgedSectionWithoutTrigger(t *testing.T) {
	forged := map[string]interface{}{
		database.ToolInitPinsKey: map[string]interface{}{
			ToolNameGithubIssueComment: map[string]interface{}{
				database.ToolInitPinnedFieldRepo:        "attacker/elsewhere",
				database.ToolInitPinnedFieldIssueNumber: "1",
			},
		},
	}
	bound := bindInteractionToolInitPins(forged, nil)
	if _, present := bound[database.ToolInitPinsKey]; present {
		t.Fatalf("forged %s section survived without a trigger: %#v", database.ToolInitPinsKey, bound)
	}
	if pins := database.ToolInitPinsFromConfig(bound); len(pins) != 0 {
		t.Errorf("expected no pins, got %#v", pins)
	}
}

func TestNormalizeToolInitPinRepoAndNumber(t *testing.T) {
	if got := normalizeToolInitPinRepo(" /Msgmate-IO/Open-Chat-Go/ "); got != "msgmate-io/open-chat-go" {
		t.Errorf("normalizeToolInitPinRepo = %q", got)
	}
	if got := normalizeToolInitPinRepo("no-slash"); got != "" {
		t.Errorf("a malformed repo must normalise to empty, got %q", got)
	}
	for raw, want := range map[string]string{"ci#1049": "1049", "#1049": "1049", "issue:1049": "1049", " 1049 ": "1049"} {
		if got := normalizeToolInitPinNumber(raw); got != want {
			t.Errorf("normalizeToolInitPinNumber(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{"", "1049x", "abc"} {
		if got := normalizeToolInitPinNumber(raw); got != "" {
			t.Errorf("normalizeToolInitPinNumber(%q) = %q, want empty", raw, got)
		}
	}
}
