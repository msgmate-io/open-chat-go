package chats

import (
	"encoding/json"
	"strings"
	"testing"

	"backend/chatstate"
	"backend/database"
)

// //////////////////////////////////////////////////////////////////////////////
// ci#1093: the interaction status carries a context-usage indicator, so a client
// (TUI, web transcript) can render "x% of context used" for cloud workflows and
// local/TUI coding sessions alike, instead of only discovering the state when a
// turn dies of compaction. The wire hop is deliberately testable without the AI
// handler: the indicator is resolved through the chatstate provider registry
// (ci#1086), so these tests stub the provider instead of a live model.
// //////////////////////////////////////////////////////////////////////////////

// stubContextUsageProvider installs a provider for one test and restores
// whatever the process had before (the msgmate init hook installs the real one).
func stubContextUsageProvider(t *testing.T, provider chatstate.ContextUsageFunc) {
	t.Helper()
	previous, hadPrevious := chatstate.LookupContextUsageProvider()
	t.Cleanup(func() {
		if hadPrevious {
			chatstate.RegisterContextUsageProvider(previous)
			return
		}
		chatstate.RegisterContextUsageProvider(nil)
	})
	chatstate.RegisterContextUsageProvider(provider)
}

// contextUsageTestQueue is a chatQueueActivity stub: the resolver takes the
// queue questions as an interface (ci#850), so the test can pin which state the
// status resolves to without an asynq inspector.
type contextUsageTestQueue struct {
	active bool
}

func (q contextUsageTestQueue) isActive(string) bool { return q.active }
func (q contextUsageTestQueue) queuedCount(string) int {
	return 0
}

// TestContextUsageForChat covers the indicator hop itself: no provider and a
// blank chat uuid report nothing, recorded usage is mapped field by field, and
// a chat this process never measured reports nothing at all.
func TestContextUsageForChat(t *testing.T) {
	stubContextUsageProvider(t, nil)
	if got := contextUsageForChat("chat-1093"); got != nil {
		t.Fatalf("expected no indicator without a provider, got %+v", got)
	}

	stubContextUsageProvider(t, func(chatUUID string) (chatstate.ContextUsageSnapshot, bool) {
		if chatUUID != "chat-1093" {
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
	if got := contextUsageForChat("   "); got != nil {
		t.Fatalf("expected no indicator for a blank chat uuid, got %+v", got)
	}

	indicator := contextUsageForChat("chat-1093")
	if indicator == nil {
		t.Fatalf("expected an indicator for a chat with recorded usage")
	}
	if indicator.UsedPercent != 73 || indicator.UsedChars != 87600 || indicator.BudgetChars != 120000 ||
		indicator.EstimatedTokens != 21900 || !indicator.Compacted || indicator.UpdatedAt != "2025-01-02T03:04:05Z" {
		t.Fatalf("unexpected indicator: %+v", indicator)
	}

	if got := contextUsageForChat("unknown-chat"); got != nil {
		t.Fatalf("expected no indicator for a chat without recorded usage, got %+v", got)
	}
}

// TestContextUsageCarriedByEveryInteractionState pins the ci#1093 acceptance
// criterion: the indicator is resolved before the resolver's early returns, so
// every interaction state (active, needs_confirmation, finished, idle) carries
// it - not just the active path a "live turn" surface would read.
func TestContextUsageCarriedByEveryInteractionState(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ctx-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "ctx-bot@example.com", false)

	stubContextUsageProvider(t, func(chatUUID string) (chatstate.ContextUsageSnapshot, bool) {
		// Every chat in this test reports usage: the assertion is about which
		// states surface it, not about which chat was measured.
		if strings.TrimSpace(chatUUID) == "" {
			return chatstate.ContextUsageSnapshot{}, false
		}
		return chatstate.ContextUsageSnapshot{UsedPercent: 42, UsedChars: 4200, BudgetChars: 10000}, true
	})

	newChat := func() database.Chat {
		t.Helper()
		chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
		if err := DB.Create(&chat).Error; err != nil {
			t.Fatalf("failed to create chat: %v", err)
		}
		return chat
	}
	addMessage := func(chat database.Chat, meta string) {
		t.Helper()
		text := "message"
		msg := database.Message{
			ChatId:     chat.ID,
			SenderId:   bot.ID,
			ReceiverId: owner.ID,
			DataType:   "text",
			Text:       &text,
			MetaData:   database.JSONRaw(meta),
		}
		if err := DB.Create(&msg).Error; err != nil {
			t.Fatalf("failed to create message: %v", err)
		}
	}

	pendingChat := newChat()
	addMessage(pendingChat, `{"finished":true,"interaction_confirmation":{"status":"pending","source_message_uuid":"msg-1"}}`)

	finishedChat := newChat()
	addMessage(finishedChat, `{"finished":true}`)

	// The idle chat deliberately has no messages at all: that is the resolver's
	// "nothing ever happened here" path, which still must carry the indicator
	// when the chat happens to have recorded usage.
	idleChat := newChat()

	cases := []struct {
		name  string
		chat  database.Chat
		queue chatQueueActivity
		want  string
	}{
		{name: "active", chat: newChat(), queue: contextUsageTestQueue{active: true}, want: string(chatstate.StateActive)},
		{name: "needs_confirmation", chat: pendingChat, queue: contextUsageTestQueue{}, want: string(chatstate.StateNeedsConfirmation)},
		{name: "finished", chat: finishedChat, queue: contextUsageTestQueue{}, want: string(chatstate.StateFinished)},
		{name: "idle", chat: idleChat, queue: contextUsageTestQueue{}, want: string(chatstate.StateIdle)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, err := resolveInteractionStatusWithQueue(DB, tc.chat, tc.queue)
			if err != nil {
				t.Fatalf("resolveInteractionStatusWithQueue failed: %v", err)
			}
			if status.State != tc.want {
				t.Fatalf("expected state %q, got %q", tc.want, status.State)
			}
			if status.ContextUsage == nil {
				t.Fatalf("expected the %s status to carry context_usage", tc.name)
			}
			if status.ContextUsage.UsedPercent != 42 || status.ContextUsage.UsedChars != 4200 || status.ContextUsage.BudgetChars != 10000 {
				t.Fatalf("unexpected context_usage on the %s status: %+v", tc.name, status.ContextUsage)
			}
		})
	}
}

// TestContextUsageAbsentWhenUnmeasured pins the other half of the wire contract:
// a chat this process never measured must not carry the block, so a client can
// tell "not measured" from "0% used".
func TestContextUsageAbsentWhenUnmeasured(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "ctx-unmeasured-owner@example.com", false)
	bot := createUserForChatsTest(t, DB, "ctx-unmeasured-bot@example.com", false)

	stubContextUsageProvider(t, func(string) (chatstate.ContextUsageSnapshot, bool) {
		return chatstate.ContextUsageSnapshot{}, false
	})

	chat := database.Chat{User1Id: owner.ID, User2Id: bot.ID, ChatType: "interaction"}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	status, err := resolveInteractionStatusWithQueue(DB, chat, contextUsageTestQueue{})
	if err != nil {
		t.Fatalf("resolveInteractionStatusWithQueue failed: %v", err)
	}
	if status.ContextUsage != nil {
		t.Fatalf("expected no context_usage for an unmeasured chat, got %+v", status.ContextUsage)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal interaction status: %v", err)
	}
	if strings.Contains(string(payload), "context_usage") {
		t.Fatalf("expected the response to omit context_usage, got %s", payload)
	}
}

// TestContextUsageIndicatorSerialization pins the wire shape the TUI and the web
// client read to render the indicator.
func TestContextUsageIndicatorSerialization(t *testing.T) {
	response := InteractionStatusResponse{
		ChatUUID: "chat-1093",
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
