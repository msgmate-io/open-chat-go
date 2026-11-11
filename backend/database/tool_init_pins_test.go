package database

import (
	"errors"
	"testing"
)

func TestToolInitPinsFromConfigSkipsMalformedEntries(t *testing.T) {
	config := map[string]interface{}{
		ToolInitPinsKey: map[string]interface{}{
			"github_issue_comment": map[string]interface{}{
				ToolInitPinnedFieldRepo:        "msgmate-io/open-chat-go",
				ToolInitPinnedFieldIssueNumber: "1049",
			},
			"broken": "not-a-map",
		},
	}
	pins := ToolInitPinsFromConfig(config)
	if len(pins) != 1 {
		t.Fatalf("expected only the well-formed pin, got %d: %#v", len(pins), pins)
	}
	pin, ok := pins["github_issue_comment"]
	if !ok {
		t.Fatalf("github_issue_comment pin missing: %#v", pins)
	}
	if pin[ToolInitPinnedFieldRepo] != "msgmate-io/open-chat-go" {
		t.Errorf("unexpected repo pin: %#v", pin)
	}
	if pin[ToolInitPinnedFieldIssueNumber] != "1049" {
		t.Errorf("unexpected issue_number pin: %#v", pin)
	}
}

func TestToolInitPinsFromConfigWithoutSection(t *testing.T) {
	if pins := ToolInitPinsFromConfig(map[string]interface{}{}); len(pins) != 0 {
		t.Fatalf("expected no pins, got %#v", pins)
	}
	if pins := ToolInitPinsFromConfig(nil); len(pins) != 0 {
		t.Fatalf("expected no pins for nil config, got %#v", pins)
	}
	// A caller-supplied non-map value under the reserved key is ignored.
	if pins := ToolInitPinsFromConfig(map[string]interface{}{ToolInitPinsKey: "forged"}); len(pins) != 0 {
		t.Fatalf("expected no pins for malformed section, got %#v", pins)
	}
}

func TestToolInitPinsFromConfigDataIgnoresBrokenJSON(t *testing.T) {
	if pins := ToolInitPinsFromConfigData([]byte("{not json")); len(pins) != 0 {
		t.Fatalf("expected no pins for broken JSON, got %#v", pins)
	}
	if pins := ToolInitPinsFromConfigData(nil); len(pins) != 0 {
		t.Fatalf("expected no pins for empty data, got %#v", pins)
	}
}

func TestToolInitPinsForChatNilChat(t *testing.T) {
	if pins := ToolInitPinsForChat(nil); len(pins) != 0 {
		t.Fatalf("expected no pins for nil chat, got %#v", pins)
	}
	if pins := ToolInitPinsForChat(&Chat{}); len(pins) != 0 {
		t.Fatalf("expected no pins for chat without shared config, got %#v", pins)
	}
}

func pinnedTestPins() map[string]map[string]interface{} {
	return map[string]map[string]interface{}{
		"github_issue_comment": {
			ToolInitPinnedFieldRepo:        "msgmate-io/open-chat-go",
			ToolInitPinnedFieldIssueNumber: "1049",
		},
	}
}

func TestApplyToolInitPinsServerValueWins(t *testing.T) {
	// A payload that tries to redirect the target must be overwritten.
	initData := map[string]interface{}{
		"github_issue_comment": map[string]interface{}{
			ToolInitPinnedFieldRepo:        "attacker/evil",
			ToolInitPinnedFieldIssueNumber: "1",
		},
	}
	merged := ApplyToolInitPins(initData, pinnedTestPins())
	payload, ok := merged["github_issue_comment"].(map[string]interface{})
	if !ok {
		t.Fatalf("pinned tool entry missing: %#v", merged)
	}
	if payload[ToolInitPinnedFieldRepo] != "msgmate-io/open-chat-go" {
		t.Fatalf("server repo pin did not win: %#v", payload)
	}
	if payload[ToolInitPinnedFieldIssueNumber] != "1049" {
		t.Fatalf("server issue_number pin did not win: %#v", payload)
	}
	// The input map must not be mutated.
	original, _ := initData["github_issue_comment"].(map[string]interface{})
	if original[ToolInitPinnedFieldRepo] != "attacker/evil" {
		t.Fatalf("input map was mutated: %#v", original)
	}
}

func TestApplyToolInitPinsCreatesMissingEntry(t *testing.T) {
	merged := ApplyToolInitPins(map[string]interface{}{}, pinnedTestPins())
	payload, ok := merged["github_issue_comment"].(map[string]interface{})
	if !ok {
		t.Fatalf("pinned tool entry was not created: %#v", merged)
	}
	if payload[ToolInitPinnedFieldIssueNumber] != "1049" {
		t.Fatalf("unexpected pinned payload: %#v", payload)
	}
}

func TestApplyToolInitPinsWithoutPinsIsIdentity(t *testing.T) {
	initData := map[string]interface{}{
		"web_search": map[string]interface{}{"query": "kept"},
	}
	merged := ApplyToolInitPins(initData, nil)
	payload, ok := merged["web_search"].(map[string]interface{})
	if !ok || payload["query"] != "kept" {
		t.Fatalf("unpinned payload lost: %#v", merged)
	}
}

func TestMergeToolInitPinDropsRedirectButKeepsRest(t *testing.T) {
	payload := map[string]interface{}{
		ToolInitPinnedFieldRepo:        "attacker/evil",
		ToolInitPinnedFieldIssueNumber: "7",
		"note":                         "kept",
	}
	merged := MergeToolInitPin(payload, map[string]interface{}{
		ToolInitPinnedFieldRepo:        "msgmate-io/open-chat-go",
		ToolInitPinnedFieldIssueNumber: "1049",
	})
	if merged[ToolInitPinnedFieldRepo] != "msgmate-io/open-chat-go" {
		t.Fatalf("pinned repo missing: %#v", merged)
	}
	if merged[ToolInitPinnedFieldIssueNumber] != "1049" {
		t.Fatalf("pinned issue number missing: %#v", merged)
	}
	if merged["note"] != "kept" {
		t.Fatalf("unrelated field lost: %#v", merged)
	}
}

func TestValidatePinnedToolInitUpdate(t *testing.T) {
	pins := pinnedTestPins()

	// A restatement of the pinned target is allowed (case/prefix tolerant).
	if err := ValidatePinnedToolInitUpdate(pins, map[string]interface{}{
		"github_issue_comment": map[string]interface{}{
			ToolInitPinnedFieldRepo:        "Msgmate-IO/Open-Chat-Go",
			ToolInitPinnedFieldIssueNumber: "ci#1049",
		},
	}); err != nil {
		t.Fatalf("equivalent restatement rejected: %v", err)
	}

	// A redirect of either pinned field is rejected.
	err := ValidatePinnedToolInitUpdate(pins, map[string]interface{}{
		"github_issue_comment": map[string]interface{}{
			ToolInitPinnedFieldRepo: "attacker/evil",
		},
	})
	if !errors.Is(err, ErrPinnedToolInit) {
		t.Fatalf("repo redirect was not rejected: %v", err)
	}
	err = ValidatePinnedToolInitUpdate(pins, map[string]interface{}{
		"github_issue_comment": map[string]interface{}{
			ToolInitPinnedFieldIssueNumber: 1,
		},
	})
	if !errors.Is(err, ErrPinnedToolInit) {
		t.Fatalf("issue_number redirect was not rejected: %v", err)
	}

	// Partial updates that leave the pinned fields alone and unpinned tools pass.
	if err := ValidatePinnedToolInitUpdate(pins, map[string]interface{}{}); err != nil {
		t.Fatalf("empty update rejected: %v", err)
	}
	if err := ValidatePinnedToolInitUpdate(pins, map[string]interface{}{
		"github_issue_comment": map[string]interface{}{},
		"web_search":           map[string]interface{}{"query": "free"},
	}); err != nil {
		t.Fatalf("unpinned update rejected: %v", err)
	}
	if err := ValidatePinnedToolInitUpdate(nil, map[string]interface{}{
		"github_issue_comment": map[string]interface{}{ToolInitPinnedFieldRepo: "attacker/evil"},
	}); err != nil {
		t.Fatalf("update without pins was rejected: %v", err)
	}
}
