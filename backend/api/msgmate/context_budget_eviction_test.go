package msgmate

import (
	"fmt"
	"strings"
	"testing"
)

// buildToolHeavyConversation models the shape that overran the budget on
// ci.msgmate.io: a task, then many tool-call rounds whose results dominate the
// transcript (265 tool-result messages, ~1.1M chars, reported as 928% of a
// 120k budget while nothing was evicted).
func buildToolHeavyConversation(rounds, resultChars int) []map[string]interface{} {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are the msgmate coder."},
		{"role": "user", "content": "make the failing test pass"},
	}
	for i := 0; i < rounds; i++ {
		callID := fmt.Sprintf("call_%d", i)
		messages = append(messages, map[string]interface{}{
			"role":    "assistant",
			"content": "",
			"tool_calls": []interface{}{
				map[string]interface{}{
					"id":       callID,
					"type":     "function",
					"function": map[string]interface{}{"name": "coder_read", "arguments": `{"path":"big.go"}`},
				},
			},
		})
		messages = append(messages, map[string]interface{}{
			"role":         "tool",
			"tool_call_id": callID,
			"content":      strings.Repeat("x", resultChars),
		})
	}
	return messages
}

// TestContextBudgetGuardBoundsToolHeavyConversation is the regression guard for
// ci#910: a transcript far over budget must be evicted down under the budget
// before it is sent, or every consecutive tool call re-bills the whole growing
// history as prompt tokens.
func TestContextBudgetGuardBoundsToolHeavyConversation(t *testing.T) {
	const budget = 120000
	messages := buildToolHeavyConversation(100, 10000) // ~1.0M chars over 100 rounds

	before := messagesCharCount(messages)
	if before <= budget {
		t.Fatalf("test setup is not over budget: %d chars", before)
	}

	compacted, stats, changed := compactMessagesToContextBudget(messages, budget)
	after := messagesCharCount(compacted)

	if !changed {
		t.Fatalf("compaction reported no change for a %d-char transcript (budget %d, %.0f%%)", before, budget, 100*float64(before)/float64(budget))
	}
	if after > budget {
		t.Fatalf("compacted transcript is still over budget: %d chars > %d (stats: evicted_tool=%d narration=%d user=%d dropped=%d)",
			after, budget, stats.EvictedToolResults, stats.EvictedNarration, stats.EvictedUserMessages, stats.DroppedMessages)
	}
	// It must actually have evicted tool results (the dominant content), not
	// merely rewritten the system prompt.
	if stats.EvictedToolResults+stats.DroppedMessages == 0 {
		t.Fatalf("nothing meaningful was evicted for a %d-char transcript", before)
	}
}

// buildSingleGiantRound models the real ci.msgmate.io shape: one assistant turn
// that issued a large batch of tool calls, so every tool result belongs to the
// SAME newest round (which the guard protects to stop re-issued calls, ci#728).
func buildSingleGiantRound(rounds, resultChars int) []map[string]interface{} {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are the msgmate coder."},
		{"role": "user", "content": "make the failing test pass"},
	}
	calls := make([]interface{}, 0, rounds)
	for i := 0; i < rounds; i++ {
		calls = append(calls, map[string]interface{}{
			"id":       fmt.Sprintf("call_%d", i),
			"type":     "function",
			"function": map[string]interface{}{"name": "coder_read", "arguments": `{}`},
		})
	}
	messages = append(messages, map[string]interface{}{"role": "assistant", "content": "", "tool_calls": calls})
	for i := 0; i < rounds; i++ {
		messages = append(messages, map[string]interface{}{
			"role":         "tool",
			"tool_call_id": fmt.Sprintf("call_%d", i),
			"content":      strings.Repeat("x", resultChars),
		})
	}
	return messages
}

// TestContextBudgetGuardBoundsSingleGiantRound is the ci#910 reproducer: a
// single protected round whose results alone exceed the budget must still be
// bounded (truncate older results of the round, never leave 900%+ over).
func TestContextBudgetGuardBoundsSingleGiantRound(t *testing.T) {
	const budget = 120000
	messages := buildSingleGiantRound(200, 6000) // ~1.2M chars in one round

	before := messagesCharCount(messages)
	if before <= budget {
		t.Fatalf("test setup is not over budget: %d", before)
	}
	compacted, stats, _ := compactMessagesToContextBudget(messages, budget)
	after := messagesCharCount(compacted)
	if after > budget {
		t.Fatalf("single giant round left %d chars > %d budget (%.0f%%); evicted_tool=%d dropped=%d",
			after, budget, 100*float64(after)/float64(budget), stats.EvictedToolResults, stats.DroppedMessages)
	}
}
