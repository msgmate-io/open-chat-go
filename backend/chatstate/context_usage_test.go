package chatstate

import "testing"

// TestContextUsageProviderRegistry covers the ci#1086 hop that lets the chat
// status API report a chat's context usage without importing the AI handler.
func TestContextUsageProviderRegistry(t *testing.T) {
	// Leave the registry as it was: another test (or the msgmate init hook) may
	// have installed the real provider.
	previous, hadPrevious := LookupContextUsageProvider()
	t.Cleanup(func() {
		if hadPrevious {
			RegisterContextUsageProvider(previous)
			return
		}
		RegisterContextUsageProvider(nil)
	})

	RegisterContextUsageProvider(nil)
	if _, ok := LookupContextUsageProvider(); ok {
		t.Fatalf("expected no provider after clearing the registry")
	}

	requested := ""
	RegisterContextUsageProvider(func(chatUUID string) (ContextUsageSnapshot, bool) {
		requested = chatUUID
		if chatUUID != "chat-1086" {
			return ContextUsageSnapshot{}, false
		}
		return ContextUsageSnapshot{UsedPercent: 42, UsedChars: 420, BudgetChars: 1000, Compacted: true}, true
	})

	provider, ok := LookupContextUsageProvider()
	if !ok {
		t.Fatalf("expected the registered provider to be returned")
	}
	snapshot, ok := provider("chat-1086")
	if !ok || requested != "chat-1086" {
		t.Fatalf("expected provider to be invoked with the chat uuid, got %q (ok=%v)", requested, ok)
	}
	if snapshot.UsedPercent != 42 || snapshot.UsedChars != 420 || snapshot.BudgetChars != 1000 || !snapshot.Compacted {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	if _, ok := provider("other-chat"); ok {
		t.Fatalf("expected unknown chats to report no usage")
	}

	RegisterContextUsageProvider(nil)
	if _, ok := LookupContextUsageProvider(); ok {
		t.Fatalf("expected nil registration to clear the provider")
	}
}
