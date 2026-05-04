package msgmate

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The context-window retry path (ci#394 gap 7) trims the history and retries.
// A cut that lands inside a tool-call/tool-result pair produces a request the
// provider rejects outright, and a single cut is often not enough for a long
// session, so these tests pin the pairing repair, the progressive shrink and
// the oversized-content fallback.

func assistantToolCall(id, name string) map[string]interface{} {
	return map[string]interface{}{
		"role":    "assistant",
		"content": "",
		"tool_calls": []map[string]interface{}{
			{
				"type": "function",
				"id":   id,
				"function": map[string]interface{}{
					"name":      name,
					"arguments": "{}",
				},
			},
		},
	}
}

func assistantToolCalls(ids ...string) map[string]interface{} {
	calls := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, map[string]interface{}{
			"type": "function",
			"id":   id,
			"function": map[string]interface{}{
				"name":      "coder_read",
				"arguments": "{}",
			},
		})
	}
	return map[string]interface{}{"role": "assistant", "content": "", "tool_calls": calls}
}

func toolResult(id, content string) map[string]interface{} {
	return map[string]interface{}{"role": "tool", "tool_call_id": id, "content": content}
}

func userMessage(text string) map[string]interface{} {
	return map[string]interface{}{"role": "user", "content": text}
}

func assistantMessage(text string) map[string]interface{} {
	return map[string]interface{}{"role": "assistant", "content": text}
}

func systemMessage(text string) map[string]interface{} {
	return map[string]interface{}{"role": "system", "content": text}
}

func messageRole(t *testing.T, message map[string]interface{}) string {
	t.Helper()
	role, _ := message["role"].(string)
	return role
}

func assertPairingValid(t *testing.T, messages []map[string]interface{}) {
	t.Helper()
	declared := map[string]bool{}
	for _, message := range messages {
		switch messageRole(t, message) {
		case "assistant":
			for _, id := range toolCallIDsOf(message) {
				declared[id] = true
			}
		case "tool":
			id := toolCallIDOf(message)
			if id == "" {
				t.Fatalf("tool result without a tool_call_id")
			}
			if !declared[id] {
				t.Fatalf("orphaned tool result %q (no preceding assistant tool call)", id)
			}
		}
	}
	// Every declared call must still be answered.
	answered := map[string]bool{}
	for _, message := range messages {
		if messageRole(t, message) == "tool" {
			answered[toolCallIDOf(message)] = true
		}
	}
	for id := range declared {
		if !answered[id] {
			t.Fatalf("declared tool call %q has no result", id)
		}
	}
}

func TestContextRetryTailForRoundHalvesAndFloors(t *testing.T) {
	cases := []struct {
		round int
		want  int
	}{
		{round: 0, want: DefaultContextRetryTailMessages},
		{round: 1, want: DefaultContextRetryTailMessages / 2},
		// 14 -> 7 -> 3, which is below the floor, so round 2 already floors.
		{round: 2, want: contextRetryTailFloorMessages},
		{round: 3, want: contextRetryTailFloorMessages},
		{round: 9, want: contextRetryTailFloorMessages},
		{round: -1, want: DefaultContextRetryTailMessages},
	}
	for _, tc := range cases {
		if got := contextRetryTailForRound(tc.round); got != tc.want {
			t.Errorf("contextRetryTailForRound(%d) = %d, want %d", tc.round, got, tc.want)
		}
	}
}

func TestContextRetryTailNeverBelowFloor(t *testing.T) {
	for round := -3; round < 40; round++ {
		if got := contextRetryTailForRound(round); got < contextRetryTailFloorMessages {
			t.Fatalf("round %d produced a tail of %d, below the floor %d", round, got, contextRetryTailFloorMessages)
		}
	}
}

func TestShrinkMessagesForContextRetryRepairsOrphanedToolResult(t *testing.T) {
	// The cut lands between an assistant tool_calls message and its result, so
	// the surviving tail starts with an orphaned tool result.
	messages := []map[string]interface{}{
		systemMessage("base"),
		systemMessage("summary"),
		userMessage("old"),
		assistantToolCall("call-1", "coder_read"),
		toolResult("call-1", "old result"),
	}
	for i := 0; i < DefaultContextRetryTailMessages; i++ {
		messages = append(messages, userMessage(fmt.Sprintf("recent user %d", i)))
	}
	// Make the cut land exactly on the orphaned tool result by adding a final
	// assistant call that has no result either.
	messages = append(messages, assistantMessage("final"))

	shrunk, ok := shrinkMessagesForContextRetry(messages, 0)
	if !ok {
		t.Fatalf("expected the history to shrink")
	}
	if len(shrunk) >= len(messages) {
		t.Fatalf("expected fewer messages, got %d of %d", len(shrunk), len(messages))
	}
	if messageRole(t, shrunk[0]) != "system" || messageRole(t, shrunk[1]) != "system" {
		t.Fatalf("leading system messages must survive, got %q,%q", messageRole(t, shrunk[0]), messageRole(t, shrunk[1]))
	}
	assertPairingValid(t, shrunk)
	for _, message := range shrunk {
		if messageRole(t, message) == "tool" && toolCallIDOf(message) == "call-1" {
			t.Fatalf("orphaned tool result must be dropped")
		}
	}
}

func TestRepairToolCallPairingDropsUnansweredCall(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		userMessage("do it"),
		assistantToolCalls("answered", "dangling"),
		toolResult("answered", "ok"),
		assistantMessage("done"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if !changed {
		t.Fatalf("expected the pairing to be repaired")
	}
	if len(repaired) != len(messages) {
		t.Fatalf("expected the message count to stay the same, got %d", len(repaired))
	}
	ids := toolCallIDsOf(repaired[2])
	if len(ids) != 1 || ids[0] != "answered" {
		t.Fatalf("expected only the answered call to survive, got %v", ids)
	}
	assertPairingValid(t, repaired)
	// The input must not be mutated in place.
	if ids := toolCallIDsOf(messages[2]); len(ids) != 2 {
		t.Fatalf("input message was mutated: %v", ids)
	}
}

func TestRepairToolCallPairingDropsEmptyAssistantShell(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		userMessage("do it"),
		assistantToolCall("dangling", "coder_read"),
		assistantMessage("I moved on"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if !changed {
		t.Fatalf("expected the pairing to be repaired")
	}
	if len(repaired) != 3 {
		t.Fatalf("expected the empty tool-call shell to be dropped, got %d messages", len(repaired))
	}
	for _, message := range repaired {
		if len(toolCallIDsOf(message)) > 0 {
			t.Fatalf("no tool call should survive")
		}
	}
}

func TestRepairToolCallPairingKeepsAssistantContentWhenCallsDropped(t *testing.T) {
	shell := assistantToolCall("dangling", "coder_read")
	shell["content"] = "thinking out loud"
	messages := []map[string]interface{}{
		systemMessage("base"),
		shell,
		assistantMessage("done"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if !changed {
		t.Fatalf("expected the pairing to be repaired")
	}
	if len(repaired) != 3 {
		t.Fatalf("expected the assistant message to survive, got %d", len(repaired))
	}
	if _, exists := repaired[1]["tool_calls"]; exists {
		t.Fatalf("the dangling tool_calls field must be stripped")
	}
	if content, _ := repaired[1]["content"].(string); content != "thinking out loud" {
		t.Fatalf("assistant content must survive, got %q", content)
	}
}

func TestRepairToolCallPairingDropsOrphanedToolResult(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		toolResult("from-a-cut", "orphan"),
		userMessage("hello"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if !changed {
		t.Fatalf("expected the orphaned tool result to be reported as changed")
	}
	if len(repaired) != 2 {
		t.Fatalf("expected the orphaned tool result to be dropped, got %d messages", len(repaired))
	}
	assertPairingValid(t, repaired)
}

func TestRepairToolCallPairingNoopOnValidHistory(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		userMessage("read it"),
		assistantToolCall("call-1", "coder_read"),
		toolResult("call-1", "contents"),
		assistantMessage("done"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if changed {
		t.Fatalf("a valid history must not be reported as changed")
	}
	if len(repaired) != len(messages) {
		t.Fatalf("a valid history must be returned unchanged, got %d messages", len(repaired))
	}
	assertPairingValid(t, repaired)
}

func TestRepairToolCallPairingAcceptsInterfaceSliceShape(t *testing.T) {
	// A message decoded from JSON carries []interface{} rather than
	// []map[string]interface{}; the repair must still recognise the calls.
	decoded := map[string]interface{}{
		"role":    "assistant",
		"content": "",
		"tool_calls": []interface{}{
			map[string]interface{}{"type": "function", "id": "decoded-1", "function": map[string]interface{}{"name": "coder_read"}},
			map[string]interface{}{"type": "function", "id": "decoded-2", "function": map[string]interface{}{"name": "coder_grep"}},
		},
	}
	messages := []map[string]interface{}{
		systemMessage("base"),
		decoded,
		toolResult("decoded-1", "ok"),
	}
	repaired, changed := repairToolCallPairing(messages)
	if !changed {
		t.Fatalf("expected the unanswered decoded call to be dropped")
	}
	ids := toolCallIDsOf(repaired[1])
	if len(ids) != 1 || ids[0] != "decoded-1" {
		t.Fatalf("expected only decoded-1 to survive, got %v", ids)
	}
}

func TestShrinkMessagesForContextRetryNeverReturnsOnlySystemMessages(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		systemMessage("summary"),
	}
	for i := 0; i < 2; i++ {
		messages = append(messages, userMessage(fmt.Sprintf("m%d", i)))
	}
	// Ask for an aggressive shrink that would cut every non-system turn.
	shrunk, ok := shrinkMessagesForContextRetry(messages, 9)
	if ok {
		for _, message := range shrunk {
			if messageRole(t, message) != "system" {
				return // still has a conversational turn: acceptable
			}
		}
		t.Fatalf("a shrink must never leave only system messages")
	}
}

func TestShrinkMessagesForContextRetryProgressiveRoundsCutFurther(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		systemMessage("summary"),
	}
	for i := 0; i < DefaultContextRetryTailMessages*3; i++ {
		messages = append(messages, userMessage(fmt.Sprintf("m%d", i)))
	}

	round0, ok0 := shrinkMessagesForContextRetry(messages, 0)
	if !ok0 {
		t.Fatalf("round 0 must shrink an oversized history")
	}
	round1, ok1 := shrinkMessagesForContextRetry(round0, 1)
	if !ok1 {
		t.Fatalf("round 1 must shrink the already-shrunk history further")
	}
	round2, ok2 := shrinkMessagesForContextRetry(round1, 2)
	if !ok2 {
		t.Fatalf("round 2 must shrink further")
	}
	if !(len(round2) < len(round1) && len(round1) < len(round0)) {
		t.Fatalf("rounds must shrink monotonically: %d -> %d -> %d", len(round0), len(round1), len(round2))
	}
	if messageRole(t, round2[0]) != "system" || messageRole(t, round2[1]) != "system" {
		t.Fatalf("leading system messages must survive every round")
	}
	assertPairingValid(t, round2)
}

func TestShrinkMessagesForContextRetryNoopOnShortHistory(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		userMessage("hi"),
	}
	shrunk, ok := shrinkMessagesForContextRetry(messages, 0)
	if ok {
		t.Fatalf("a short history must not shrink")
	}
	if len(shrunk) != len(messages) {
		t.Fatalf("a short history must be returned unchanged")
	}
}

func TestTrimMessagesForContextRetryMatchesRoundZero(t *testing.T) {
	messages := []map[string]interface{}{systemMessage("base"), systemMessage("summary")}
	for i := 0; i < DefaultContextRetryTailMessages+5; i++ {
		messages = append(messages, userMessage("m"))
	}
	trimmed, ok := trimMessagesForContextRetry(messages)
	if !ok {
		t.Fatalf("expected the history to be trimmed")
	}
	round0, ok0 := shrinkMessagesForContextRetry(messages, 0)
	if !ok0 || len(trimmed) != len(round0) {
		t.Fatalf("trimMessagesForContextRetry must equal round 0 (%d vs %d)", len(trimmed), len(round0))
	}
}

func TestShrinkOversizedContextMessagesTruncatesToolResults(t *testing.T) {
	huge := strings.Repeat("x", contextRetryMaxMessageChars*3)
	messages := []map[string]interface{}{
		systemMessage("base prompt that must survive"),
		userMessage("keep me"),
		toolResult("call-1", huge),
	}
	// The message must be paired so the repair does not drop it.
	messages = append([]map[string]interface{}{messages[0], assistantToolCall("call-1", "coder_read")}, messages[1:]...)

	shrunk, ok := shrinkOversizedContextMessages(messages, contextRetryMaxMessageChars)
	if !ok {
		t.Fatalf("expected an oversized tool result to be truncated")
	}
	var toolContent string
	for _, message := range shrunk {
		if messageRole(t, message) == "tool" {
			toolContent, _ = message["content"].(string)
		}
	}
	if toolContent == "" {
		t.Fatalf("the tool result must survive truncation")
	}
	if len(toolContent) >= len(huge) {
		t.Fatalf("the tool result must be truncated, still %d chars", len(toolContent))
	}
	if !strings.Contains(toolContent, "content truncated from") {
		t.Fatalf("the truncation notice must be present, got %q", toolContent[:min(80, len(toolContent))])
	}
	if content, _ := shrunk[0]["content"].(string); content != "base prompt that must survive" {
		t.Fatalf("the system message must never be truncated, got %q", content)
	}
	assertPairingValid(t, shrunk)
}

// TestShrinkOversizedContextMessagesStaysValidUTF8 covers the last-resort
// context fallback (ci#680): the oversized content that reaches it is normally a
// tool result - source files, diffs, sandbox logs - so a byte-offset cut can
// land mid-rune. The shrunk message must stay valid UTF-8, otherwise the retry
// request this fallback exists to send is itself unparseable.
func TestShrinkOversizedContextMessagesStaysValidUTF8(t *testing.T) {
	for _, fill := range []string{"é", "→"} {
		huge := strings.Repeat(fill, contextRetryMaxMessageChars) // 2-3 bytes per rune
		messages := []map[string]interface{}{
			systemMessage("base prompt that must survive"),
			assistantToolCall("call-1", "coder_read"),
			toolResult("call-1", huge),
			userMessage("keep me"),
		}
		shrunk, ok := shrinkOversizedContextMessages(messages, contextRetryMaxMessageChars)
		if !ok {
			t.Fatalf("expected an oversized tool result to be truncated (%q)", fill)
		}
		for _, message := range shrunk {
			if messageRole(t, message) != "tool" {
				continue
			}
			content, _ := message["content"].(string)
			if !utf8.ValidString(content) {
				t.Fatalf("the shrunk tool result must stay valid UTF-8, got %q", content)
			}
			if !strings.Contains(content, "content truncated from") {
				t.Fatalf("the truncation notice must be present, got %q", content)
			}
		}
		assertPairingValid(t, shrunk)
	}
}

func TestShrinkOversizedContextMessagesNoopWhenNothingOversized(t *testing.T) {
	messages := []map[string]interface{}{
		systemMessage("base"),
		userMessage("short"),
	}
	shrunk, ok := shrinkOversizedContextMessages(messages, contextRetryMaxMessageChars)
	if ok {
		t.Fatalf("nothing oversized must be a no-op")
	}
	if len(shrunk) != len(messages) {
		t.Fatalf("the history must be returned unchanged")
	}
}

func TestToolCallIDsOfIgnoresBlankAndMissingIDs(t *testing.T) {
	blank := map[string]interface{}{
		"role": "assistant",
		"tool_calls": []map[string]interface{}{
			{"id": "   ", "function": map[string]interface{}{"name": "x"}},
			{"function": map[string]interface{}{"name": "y"}},
		},
	}
	if ids := toolCallIDsOf(blank); len(ids) != 0 {
		t.Fatalf("blank/missing ids must be ignored, got %v", ids)
	}
	if ids := toolCallIDsOf(userMessage("no calls")); len(ids) != 0 {
		t.Fatalf("a message without tool_calls must yield no ids, got %v", ids)
	}
}
