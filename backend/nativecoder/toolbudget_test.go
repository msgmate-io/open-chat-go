package nativecoder

import (
	"testing"
	"time"
)

// TestToolCallBudgetSnapshotIsRecordedAndStable is the ci#709 regression for
// the per-chat cap snapshot: a chat records the budget it was created with, the
// snapshot survives a re-attach (the same config is re-read) and a second
// snapshot pass never overwrites it.
func TestToolCallBudgetSnapshotIsRecordedAndStable(t *testing.T) {
	createdAt := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	config := map[string]interface{}{
		"model":                "qwen3-4b-instruct-2507_vllm",
		"tool_call_max_total":  400,
		"tool_call_max_failed": 25,
	}

	SnapshotToolCallBudget(config, 400, 25, createdAt)

	snapshot, ok := ToolCallBudgetSnapshot(config)
	if !ok {
		t.Fatalf("expected a tool-call budget snapshot on the chat config, got none: %#v", config)
	}
	if snapshot.MaxTotal != 400 || snapshot.MaxFailed != 25 {
		t.Fatalf("unexpected snapshot caps: total=%d failed=%d", snapshot.MaxTotal, snapshot.MaxFailed)
	}
	if snapshot.SnapshotAt != createdAt.Format(time.RFC3339) {
		t.Fatalf("unexpected snapshot timestamp: %q", snapshot.SnapshotAt)
	}

	// A second pass (re-provision, re-attach) must not overwrite the snapshot.
	SnapshotToolCallBudget(config, 5, 1, createdAt.Add(24*time.Hour))
	again, ok := ToolCallBudgetSnapshot(config)
	if !ok {
		t.Fatal("expected the snapshot to survive a second pass")
	}
	if again.MaxTotal != 400 || again.MaxFailed != 25 {
		t.Fatalf("the snapshot was overwritten: total=%d failed=%d", again.MaxTotal, again.MaxFailed)
	}
	if again.SnapshotAt != snapshot.SnapshotAt {
		t.Fatalf("the snapshot timestamp changed: %q -> %q", snapshot.SnapshotAt, again.SnapshotAt)
	}

	// The resolved budget reports the snapshot as structured state, so the caps
	// no longer live only inside an abort error string.
	resolved := ResolveToolCallBudget(config, nil)
	if resolved.MaxTotal != 400 || resolved.MaxFailed != 25 {
		t.Fatalf("expected the snapshot caps to be effective, got total=%d failed=%d", resolved.MaxTotal, resolved.MaxFailed)
	}
	if resolved.SnapshotMaxTotal != 400 || resolved.SnapshotMaxFailed != 25 {
		t.Fatalf("expected the resolved budget to report the snapshot, got %+v", resolved)
	}
	meta := resolved.Metadata()
	if meta["snapshot_max_total"] != 400 || meta["snapshot_max_failed"] != 25 {
		t.Fatalf("expected the snapshot in the structured metadata, got %#v", meta)
	}
	if meta["source"] != ToolCallBudgetSourceChatSnapshot {
		t.Fatalf("expected the snapshot to be the reported source, got %#v", meta["source"])
	}

	// A fresh snapshot pass only fills a chat config that has none.
	fresh := map[string]interface{}{}
	WithToolCallBudgetSnapshot(fresh, createdAt)
	if !HasToolCallBudgetSnapshot(fresh) {
		t.Fatalf("expected a default snapshot on a fresh config, got %#v", fresh)
	}
	freshSnapshot, _ := ToolCallBudgetSnapshot(fresh)
	if freshSnapshot.MaxTotal != DefaultToolCallMaxTotal || freshSnapshot.MaxFailed != DefaultToolCallMaxFailed {
		t.Fatalf("expected the compiled-in defaults in the fresh snapshot, got %+v", freshSnapshot)
	}
}

// TestCapRaiseReachesAlreadyCreatedChat is the ticket's second regression: a
// chat created under the old caps must honour a cap raise applied to the bot's
// current shared config, because the chat's config is a frozen copy of the bot
// defaults taken at creation time.
func TestCapRaiseReachesAlreadyCreatedChat(t *testing.T) {
	chatCreatedUnderOldCaps := map[string]interface{}{
		"model":                "qwen3-4b-instruct-2507_vllm",
		"tool_call_max_total":  100,
		"tool_call_max_failed": 3,
	}
	// No snapshot on the chat: it was created before the snapshot existed.
	if HasToolCallBudgetSnapshot(chatCreatedUnderOldCaps) {
		t.Fatal("expected no snapshot on a legacy chat config")
	}

	botConfigRaised := map[string]interface{}{
		"tool_call_max_total":  400,
		"tool_call_max_failed": 25,
	}

	resolved := ResolveToolCallBudget(chatCreatedUnderOldCaps, botConfigRaised)
	if resolved.MaxTotal != 400 {
		t.Fatalf("expected the raised total cap to reach the existing chat, got %d", resolved.MaxTotal)
	}
	if resolved.MaxFailed != 25 {
		t.Fatalf("expected the raised failed cap to reach the existing chat, got %d", resolved.MaxFailed)
	}
	if resolved.Source != ToolCallBudgetSourceBotConfig {
		t.Fatalf("expected the bot config to be reported as the source, got %q", resolved.Source)
	}
}

// TestResolveToolCallBudgetKeepsDeclaredChatBudget guards the other half: a
// resolution never lowers what the chat (or its snapshot) declared, and a bot
// config that is lower than a chat's declared budget cannot shrink it.
func TestResolveToolCallBudgetKeepsDeclaredChatBudget(t *testing.T) {
	config := map[string]interface{}{
		"tool_call_max_total":  400,
		"tool_call_max_failed": 25,
	}
	SnapshotToolCallBudget(config, 400, 25, time.Now())

	resolved := ResolveToolCallBudget(config, map[string]interface{}{
		"tool_call_max_total":  12,
		"tool_call_max_failed": 3,
	})
	if resolved.MaxTotal != 400 || resolved.MaxFailed != 25 {
		t.Fatalf("a chat's declared budget was lowered: total=%d failed=%d", resolved.MaxTotal, resolved.MaxFailed)
	}
}

// TestToolCallBudgetUsageReportsConsumedAndRemaining locks in the structured
// consumed/remaining counters.
func TestToolCallBudgetUsageReportsConsumedAndRemaining(t *testing.T) {
	budget := ToolCallBudget{MaxTotal: 10, MaxFailed: 2, Source: ToolCallBudgetSourceChatSnapshot}
	usage := budget.Usage(9, 1, 3, 1, 2)

	if usage.ConsumedTotal != 9 || usage.RemainingTotal != 1 {
		t.Fatalf("unexpected total counters: %+v", usage)
	}
	if usage.ConsumedFailed != 1 || usage.RemainingFailed != 1 {
		t.Fatalf("unexpected failed counters: %+v", usage)
	}
	if usage.ExhaustedTotal || usage.ExhaustedFailed {
		t.Fatalf("expected the budget to still have room: %+v", usage)
	}
	if usage.TransientFailed != 3 || usage.SandboxCapacityF != 1 {
		t.Fatalf("expected the budget-exempt counters, got %+v", usage)
	}
	// ci#724: the bookkeeping calls of a self-re-arming turn are reported
	// separately from the charged ones.
	if usage.BudgetExemptTotal != 2 {
		t.Fatalf("expected the budget-exempt call count, got %+v", usage)
	}

	meta := usage.Metadata()
	if meta["consumed_total"] != 9 || meta["remaining_total"] != 1 {
		t.Fatalf("unexpected usage metadata: %#v", meta)
	}
	if meta["max_total"] != 10 || meta["max_failed"] != 2 {
		t.Fatalf("expected the caps in the usage metadata, got %#v", meta)
	}
	if meta["budget_exempt"] != 2 {
		t.Fatalf("expected the budget-exempt count in the usage metadata, got %#v", meta)
	}

	exhausted := budget.Usage(10, 2, 0, 0, 0)
	if !exhausted.ExhaustedTotal || !exhausted.ExhaustedFailed {
		t.Fatalf("expected the exhausted flags, got %+v", exhausted)
	}
	if exhausted.RemainingTotal != 0 || exhausted.RemainingFailed != 0 {
		t.Fatalf("expected zero remaining, got %+v", exhausted)
	}

	over := budget.Usage(14, 5, 0, 0, 0)
	if over.RemainingTotal != 0 || over.RemainingFailed != 0 {
		t.Fatalf("remaining must never go negative, got %+v", over)
	}
}
