package chats

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/chatstate"
)

// TestContextUsageForChat covers the ci#1086 indicator hop: the interaction
// status resolves a chat's context usage through the chatstate provider registry
// so local/TUI sessions report the same percentage as cloud workflows.
func TestContextUsageForChat(t *testing.T) {
	previous, hadPrevious := chatstate.LookupContextUsageProvider()
	t.Cleanup(func() {
		if hadPrevious {
			chatstate.RegisterContextUsageProvider(previous)
			return
		}
		chatstate.RegisterContextUsageProvider(nil)
	})

	chatstate.RegisterContextUsageProvider(nil)
	if got := contextUsageForChat("chat-1086"); got != nil {
		t.Fatalf("expected no indicator without a provider, got %+v", got)
	}
	if got := contextUsageForChat("   "); got != nil {
		t.Fatalf("expected no indicator for a blank chat uuid, got %+v", got)
	}

	chatstate.RegisterContextUsageProvider(func(chatUUID string) (chatstate.ContextUsageSnapshot, bool) {
		if chatUUID != "chat-1086" {
			return chatstate.ContextUsageSnapshot{}, false
		}
		return chatstate.ContextUsageSnapshot{
			UsedPercent:     73,
			UsedChars:       87600,
			BudgetChars:     120000,
			EstimatedTokens: 21900,
			Compacted:       true,
			UpdatedAt:       "2025-01-02T03:04:05Z",
		}, true
	})

	indicator := contextUsageForChat("chat-1086")
	if indicator == nil {
		t.Fatalf("expected an indicator for a chat with recorded usage")
	}
	if indicator.UsedPercent != 73 || indicator.UsedChars != 87600 || indicator.BudgetChars != 120000 ||
		indicator.EstimatedTokens != 21900 || !indicator.Compacted {
		t.Fatalf("unexpected indicator: %+v", indicator)
	}

	if got := contextUsageForChat("unknown-chat"); got != nil {
		t.Fatalf("expected no indicator for a chat without recorded usage, got %+v", got)
	}
}

// TestContextUsageIndicatorSerialization pins the wire shape the TUI and the web
// client read to render the "x% of context used" indicator (ci#1086).
func TestContextUsageIndicatorSerialization(t *testing.T) {
	response := InteractionStatusResponse{
		ChatUUID: "chat-1086",
		State:    "active",
		ContextUsage: &ContextUsageIndicator{
			UsedPercent:     12,
			UsedChars:       14400,
			BudgetChars:     120000,
			EstimatedTokens: 3600,
		},
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal interaction status: %v", err)
	}
	encoded := string(payload)
	for _, want := range []string{`"context_usage"`, `"used_percent":12`, `"used_chars":14400`, `"budget_chars":120000`, `"estimated_tokens":3600`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("expected %s in %s", want, encoded)
		}
	}
	// A status response without recorded usage must not carry the block at all,
	// so clients can distinguish "not measured" from "0% used".
	payload, err = json.Marshal(InteractionStatusResponse{ChatUUID: "chat-1", State: "idle"})
	if err != nil {
		t.Fatalf("marshal interaction status: %v", err)
	}
	if strings.Contains(string(payload), "context_usage") {
		t.Fatalf("expected no context_usage block without usage, got %s", payload)
	}
}
