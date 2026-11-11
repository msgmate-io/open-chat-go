package database

import (
	"encoding/json"
	"testing"
)

// ci#1049: the reserved tool_init_pins section is server-owned; pins override
// caller supplied tool init values and an update that repoints a pinned tool at
// another issue is rejected.

func chatWithPins(t *testing.T, pins map[string]interface{}) *Chat {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{ToolInitPinsKey: pins})
	if err != nil {
		t.Fatalf("failed to marshal pins: %v", err)
	}
	return &Chat{SharedConfig: &SharedChatConfig{ConfigData: json.RawMessage(raw)}}
}

func TestToolInitPinsForChatReadsReservedSection(t *testing.T) {
	pins := map[string]interface{}{
		"github_issue_comment": BuildGithubIssueToolInitPin("msgmate-io/open-chat-go-ci", 1049),
	}
	chat := chatWithPins(t, pins)
	got := ToolInitPinsForChat(chat)
	if len(got) != 1 {
		t.Fatalf("expected one pin, got %#v", got)
	}
	pin, ok := got["github_issue_comment"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a map pin, got %#v", got["github_issue_comment"])
	}
	if pin["repo"] != "msgmate-io/open-chat-go-ci" {
		t.Fatalf("unexpected pinned repo: %#v", pin["repo"])
	}
	if number, _ := pin["issue_number"].(int); number != 1049 {
		t.Fatalf("unexpected pinned issue number: %#v", pin["issue_number"])
	}
}

func TestToolInitPinsForChatWithoutPinsOrConfig(t *testing.T) {
	if got := ToolInitPinsForChat(nil); got != nil {
		t.Fatalf("expected nil pins for a nil chat, got %#v", got)
	}
	if got := ToolInitPinsForChat(&Chat{}); got != nil {
		t.Fatalf("expected nil pins for a chat without shared config, got %#v", got)
	}
	chat := &Chat{SharedConfig: &SharedChatConfig{ConfigData: json.RawMessage(`{"tools":["github_issue_comment"]}`)}}
	if got := ToolInitPinsForChat(chat); got != nil {
		t.Fatalf("expected nil pins for a config without pins, got %#v", got)
	}
}

func TestApplyToolInitPinsServerValueWins(t *testing.T) {
	pins := map[string]interface{}{
		"github_issue_comment": BuildGithubIssueToolInitPin("msgmate-io/open-chat-go-ci", 1049),
	}
	initData := map[string]interface{}{
		"github_issue_comment": map[string]interface{}{
			"repo":         "attacker/elsewhere",
			"issue_number": 1,
			"header":       "kept",
		},
	}
	applied := ApplyToolInitPins(initData, pins)
	toolInit, ok := applied["github_issue_comment"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a tool init map, got %#v", applied["github_issue_comment"])
	}
	if toolInit["repo"] != "msgmate-io/open-chat-go-ci" {
		t.Fatalf("pin must win over the supplied repo, got %#v", toolInit["repo"])
	}
	if number, _ := toolInit["issue_number"].(int); number != 1049 {
		t.Fatalf("pin must win over the supplied issue number, got %#v", toolInit["issue_number"])
	}
	if toolInit["header"] != "kept" {
		t.Fatalf("unpinned params must be preserved, got %#v", toolInit["header"])
	}
	// The input map must not be mutated.
	supplied := initData["github_issue_comment"].(map[string]interface{})
	if supplied["repo"] != "attacker/elsewhere" {
		t.Fatalf("ApplyToolInitPins mutated its input: %#v", supplied)
	}
}

func TestApplyToolInitPinsWithoutPinsIsPassthrough(t *testing.T) {
	initData := map[string]interface{}{"github_issue_comment": map[string]interface{}{"repo": "a/b"}}
	applied := ApplyToolInitPins(initData, nil)
	if got := applied["github_issue_comment"].(map[string]interface{})["repo"]; got != "a/b" {
		t.Fatalf("expected passthrough without pins, got %#v", got)
	}
}

func TestValidatePinnedToolInitUpdate(t *testing.T) {
	pins := map[string]interface{}{
		"github_issue_comment": BuildGithubIssueToolInitPin("msgmate-io/open-chat-go-ci", 1049),
	}

	// Repointing the repo or the issue number is rejected.
	if err := ValidatePinnedToolInitUpdate(pins, "github_issue_comment", map[string]interface{}{"repo": "attacker/elsewhere"}); err == nil {
		t.Fatal("expected a repointed repo to be rejected")
	}
	if err := ValidatePinnedToolInitUpdate(pins, "github_issue_comment", map[string]interface{}{"issue_number": 1}); err == nil {
		t.Fatal("expected a repointed issue number to be rejected")
	}
	// float64 (as decoded from JSON) still matches the pinned int.
	if err := ValidatePinnedToolInitUpdate(pins, "github_issue_comment", map[string]interface{}{"issue_number": float64(1049)}); err != nil {
		t.Fatalf("expected the pinned issue number to be accepted, got %v", err)
	}
	// Matching values and unpinned params are accepted.
	if err := ValidatePinnedToolInitUpdate(pins, "github_issue_comment", map[string]interface{}{
		"repo":         " msgmate-io/open-chat-go-ci ",
		"issue_number": 1049,
		"header":       "ok",
	}); err != nil {
		t.Fatalf("expected the pinned target to be accepted, got %v", err)
	}
	// Unpinned tools are not constrained.
	if err := ValidatePinnedToolInitUpdate(pins, "github_pr_comment", map[string]interface{}{"repo": "attacker/elsewhere"}); err != nil {
		t.Fatalf("expected an unpinned tool to pass, got %v", err)
	}
}
