package chats

import (
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// confirmRuntimeSelector drives the pending -> confirmed transition so a reset
// test starts from the same state the widget produces.
func confirmRuntimeSelector(t *testing.T, DB *gorm.DB, owner *database.User, chat database.Chat, message database.Message, bot *database.User, claimID string) database.Chat {
	t.Helper()

	startedChat := createChatOfType(t, DB, owner, bot, "interaction")
	body := map[string]interface{}{
		"selector_id":       "tc-1",
		"decision":          "started",
		"started_chat_uuid": startedChat.UUID,
	}
	if claimID != "" {
		body["claim_id"] = claimID
	}
	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, body)
	if rr.Code != 200 {
		t.Fatalf("failed to confirm the selector before resetting: %d: %s", rr.Code, rr.Body.String())
	}
	return startedChat
}

// TestResolveRuntimeSelectorResetReturnsToPending covers the core transition
// confirmed --reset--> pending: the selector is un-confirmed, every decision
// artefact is cleared, the abandoned run is archived, and the reset is counted.
func TestResolveRuntimeSelectorResetReturnsToPending(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	startedChat := confirmRuntimeSelector(t, DB, owner, chat, message, bot, "claim-a")

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if rr.Code != 200 {
		t.Fatalf("expected status 200 for reset, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if len(selectors) != 1 {
		t.Fatalf("expected one selector, got %d", len(selectors))
	}
	selector := selectors[0]
	if selector["status"] != RuntimeSelectorPending {
		t.Fatalf("expected pending status after reset, got %#v", selector["status"])
	}
	for _, key := range []string{"claim_id", "decided_by", "decided_at", "started_chat_uuid", "started_interaction_url"} {
		if value, ok := selector[key]; ok {
			t.Fatalf("expected %q to be cleared by reset, got %#v", key, value)
		}
	}
	if count := actionTaskIntField(selector, "reset_count"); count != 1 {
		t.Fatalf("expected reset_count 1, got %d", count)
	}

	history, ok := selector["reset_history"].([]interface{})
	if !ok || len(history) != 1 {
		t.Fatalf("expected one reset_history entry, got %#v", selector["reset_history"])
	}
	entry, _ := history[0].(map[string]interface{})
	if entry["chat_uuid"] != startedChat.UUID {
		t.Fatalf("expected the abandoned run chat uuid in reset_history, got %#v", entry["chat_uuid"])
	}
	if entry["reset_by"] != owner.UUID {
		t.Fatalf("expected reset_by to record the resolving user, got %#v", entry["reset_by"])
	}
	if resetAt, _ := entry["reset_at"].(string); resetAt == "" {
		t.Fatalf("expected a reset_at timestamp, got %#v", entry["reset_at"])
	}
}

// TestResolveRuntimeSelectorResetRejectsPending proves the reset only applies to
// a confirmed selector: resetting what was never confirmed is a conflict.
func TestResolveRuntimeSelectorResetRejectsPending(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-pending@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if rr.Code != 409 {
		t.Fatalf("expected 409 for reset on a pending selector, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["status"] != RuntimeSelectorPending {
		t.Fatalf("expected the selector to stay pending, got %#v", selectors[0]["status"])
	}
	if count := actionTaskIntField(selectors[0], "reset_count"); count != 0 {
		t.Fatalf("expected reset_count to stay 0, got %d", count)
	}
	if _, ok := selectors[0]["reset_history"]; ok {
		t.Fatalf("expected no reset_history on a rejected reset, got %#v", selectors[0]["reset_history"])
	}
}

// TestResolveRuntimeSelectorResetRejectsCancelled proves cancelled stays
// terminal: a cancelled selector cannot be reset back into the stack.
func TestResolveRuntimeSelectorResetRejectsCancelled(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-cancelled@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	cancelled := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "cancelled",
	})
	if cancelled.Code != 200 {
		t.Fatalf("failed to cancel the selector: %d: %s", cancelled.Code, cancelled.Body.String())
	}

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if rr.Code != 409 {
		t.Fatalf("expected 409 for reset on a cancelled selector, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["status"] != RuntimeSelectorCancelled {
		t.Fatalf("expected the selector to stay cancelled, got %#v", selectors[0]["status"])
	}
	if count := actionTaskIntField(selectors[0], "reset_count"); count != 0 {
		t.Fatalf("expected reset_count to stay 0, got %d", count)
	}
}

// TestResolveRuntimeSelectorResetDefaultOn proves the reset affordance is
// opt-out: a selector that never carries allow_reset (every selector persisted
// before ci#1135) can be reset.
func TestResolveRuntimeSelectorResetDefaultOn(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-default@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	confirmRuntimeSelector(t, DB, owner, chat, message, bot, "claim-a")

	_, before := loadRuntimeSelectors(t, DB, message)
	if _, ok := before[0]["allow_reset"]; ok {
		t.Fatalf("expected the fixture to carry no allow_reset key, got %#v", before[0]["allow_reset"])
	}

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if rr.Code != 200 {
		t.Fatalf("expected 200 for reset with allow_reset absent, got %d: %s", rr.Code, rr.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["status"] != RuntimeSelectorPending {
		t.Fatalf("expected pending status after reset, got %#v", selectors[0]["status"])
	}
}

// TestResolveRuntimeSelectorResetForbiddenByConfig proves allow_reset:false makes
// the reset a conflict and leaves the stored meta untouched.
func TestResolveRuntimeSelectorResetForbiddenByConfig(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-forbidden@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	confirmRuntimeSelector(t, DB, owner, chat, message, bot, "claim-a")

	// Stamp allow_reset:false onto the persisted selector (what the proposing
	// tool emits when the runtime proposal forbids resetting).
	meta, selectors := loadRuntimeSelectors(t, DB, message)
	selectors[0]["allow_reset"] = false
	if err := persistRuntimeSelectorMeta(DB, message, meta, selectors); err != nil {
		t.Fatalf("failed to stamp allow_reset: %v", err)
	}

	rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if rr.Code != 409 {
		t.Fatalf("expected 409 for reset with allow_reset:false, got %d: %s", rr.Code, rr.Body.String())
	}

	afterMeta, after := loadRuntimeSelectors(t, DB, message)
	if after[0]["status"] != RuntimeSelectorConfirmed {
		t.Fatalf("expected the selector to stay confirmed, got %#v", after[0]["status"])
	}
	if _, ok := after[0]["reset_history"]; ok {
		t.Fatalf("expected no reset_history on a forbidden reset, got %#v", after[0]["reset_history"])
	}
	if count := actionTaskIntField(after[0], "reset_count"); count != 0 {
		t.Fatalf("expected reset_count to stay 0, got %d", count)
	}
	if allowReset, ok := after[0]["allow_reset"].(bool); !ok || allowReset {
		t.Fatalf("expected allow_reset:false to survive the rejected reset, got %#v", after[0]["allow_reset"])
	}
	if after[0]["claim_id"] != "claim-a" {
		t.Fatalf("expected claim_id to be preserved on a forbidden reset, got %#v", after[0]["claim_id"])
	}
	if _, ok := afterMeta["runtime_selectors"]; !ok {
		t.Fatalf("expected runtime_selectors to still be present in meta")
	}
}

// TestResolveRuntimeSelectorResetAllowsFreshClaim proves the reset loop is
// usable: a new claim may confirm the selector again after a reset.
func TestResolveRuntimeSelectorResetAllowsFreshClaim(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-reclaim@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	confirmRuntimeSelector(t, DB, owner, chat, message, bot, "claim-a")

	reset := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if reset.Code != 200 {
		t.Fatalf("expected status 200 for reset, got %d: %s", reset.Code, reset.Body.String())
	}

	secondChat := createChatOfType(t, DB, owner, bot, "interaction")
	restarted := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id":       "tc-1",
		"decision":          "started",
		"claim_id":          "claim-b",
		"started_chat_uuid": secondChat.UUID,
	})
	if restarted.Code != 200 {
		t.Fatalf("expected a fresh claim to succeed after reset, got %d: %s", restarted.Code, restarted.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["status"] != RuntimeSelectorConfirmed {
		t.Fatalf("expected confirmed status after the fresh claim, got %#v", selectors[0]["status"])
	}
	if selectors[0]["claim_id"] != "claim-b" {
		t.Fatalf("expected the fresh claim to own the selector, got %#v", selectors[0]["claim_id"])
	}
	if selectors[0]["started_chat_uuid"] != secondChat.UUID {
		t.Fatalf("expected the new run to be recorded, got %#v", selectors[0]["started_chat_uuid"])
	}
	if count := actionTaskIntField(selectors[0], "reset_count"); count != 1 {
		t.Fatalf("expected reset_count to survive the re-confirmation, got %d", count)
	}
}

// TestResolveRuntimeSelectorResetInvalidatesOldClaim proves clearing claim_id on
// reset closes the double-run hole: a stale tab that still holds the pre-reset
// claim can no longer complete the second phase.
func TestResolveRuntimeSelectorResetInvalidatesOldClaim(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-stale@example.com", false)
	chat, message, bot := seedRuntimeSelector(t, DB, owner)
	confirmRuntimeSelector(t, DB, owner, chat, message, bot, "claim-a")

	reset := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "reset",
	})
	if reset.Code != 200 {
		t.Fatalf("expected status 200 for reset, got %d: %s", reset.Code, reset.Body.String())
	}

	// A fresh claim wins the pending -> confirmed transition.
	secondChat := createChatOfType(t, DB, owner, bot, "interaction")
	restarted := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id":       "tc-1",
		"decision":          "started",
		"claim_id":          "claim-b",
		"started_chat_uuid": secondChat.UUID,
	})
	if restarted.Code != 200 {
		t.Fatalf("expected the fresh claim to succeed, got %d: %s", restarted.Code, restarted.Body.String())
	}

	stale := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
		"selector_id": "tc-1",
		"decision":    "started",
		"claim_id":    "claim-a",
	})
	if stale.Code != 409 {
		t.Fatalf("expected the pre-reset claim to conflict, got %d: %s", stale.Code, stale.Body.String())
	}

	_, selectors := loadRuntimeSelectors(t, DB, message)
	if selectors[0]["claim_id"] != "claim-b" {
		t.Fatalf("expected the fresh claim to keep the selector, got %#v", selectors[0]["claim_id"])
	}
	if selectors[0]["started_chat_uuid"] != secondChat.UUID {
		t.Fatalf("expected the fresh run to be recorded, got %#v", selectors[0]["started_chat_uuid"])
	}
}

// TestResolveRuntimeSelectorResetInvalidDecisionStaysRejected keeps the widened
// whitelist honest: unknown decisions are still a 400.
func TestResolveRuntimeSelectorResetInvalidDecisionStaysRejected(t *testing.T) {
	DB := setupChatsTestDB(t)
	owner := createUserForChatsTest(t, DB, "owner-runtime-reset-invalid@example.com", false)
	chat, message, _ := seedRuntimeSelector(t, DB, owner)

	for _, decision := range []string{"", "resetted", "restart", "reset_"} {
		rr := callResolveRuntimeSelector(t, DB, owner, chat, message, map[string]interface{}{
			"selector_id": "tc-1",
			"decision":    decision,
		})
		if rr.Code != 400 {
			t.Fatalf("expected 400 for decision %q, got %d: %s", decision, rr.Code, rr.Body.String())
		}
	}
}
