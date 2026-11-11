package database

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

// eventClaimTestDB opens a throwaway SQLite database with every migration
// applied, so the composite unique index on (scope, trigger_id, event_key) is
// really created instead of being simulated by the model tags (ci#1047).
func eventClaimTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "event_claim_test.db"),
		ResetDB:  true,
	})
}

// TestClaimEventEnforcesCompositeTriggerScope pins that event identity is the
// (scope, trigger_id, event_key) *tuple*: reclaiming the same event key under a
// different trigger_id must succeed, while the identical tuple must not. A
// single-column unique index on event_key would fail this case, which is why
// the ticket asks for the composite explicitly.
func TestClaimEventEnforcesCompositeTriggerScope(t *testing.T) {
	DB := eventClaimTestDB(t)

	first, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1047:opened")
	if err != nil {
		t.Fatalf("first ClaimEvent failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected the first claim of a fresh event to succeed")
	}
	if first.State != EventClaimStateClaimed {
		t.Fatalf("expected a fresh claim to be in state %q, got %q", EventClaimStateClaimed, first.State)
	}

	// The same tuple is already taken.
	existing, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1047:opened")
	if err != nil {
		t.Fatalf("second ClaimEvent failed: %v", err)
	}
	if claimed {
		t.Fatalf("expected the duplicate claim of the same (scope, trigger, event) tuple to lose the race")
	}
	if existing.ID != first.ID {
		t.Fatalf("expected the stored claim %d to be read back, got %d", first.ID, existing.ID)
	}

	// The SAME event key under a DIFFERENT trigger is a different event.
	other, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-b", "issue:1047:opened")
	if err != nil {
		t.Fatalf("ClaimEvent under a second trigger failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected the same event key under a different trigger_id to be claimable (composite index)")
	}
	if other.ID == first.ID {
		t.Fatalf("expected a distinct claim row for the second trigger, got the row of the first")
	}

	// A third scope must not collide either.
	if _, claimed, err := ClaimEvent(DB, "another_rail", "trigger-a", "issue:1047:opened"); err != nil || !claimed {
		t.Fatalf("expected an independent scope to claim the same key, claimed=%v err=%v", claimed, err)
	}

	var count int64
	if err := DB.Model(&EventClaim{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count claims: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 claim rows (2 triggers + 1 other scope), got %d", count)
	}
}

// TestEventClaimLifecycleFiredAndRelease pins the claim state machine the
// dispatch handler relies on: a fired claim dedupes and reports the chat, and a
// released claim frees the event for a retry while never removing a fired one.
func TestEventClaimLifecycleFiredAndRelease(t *testing.T) {
	DB := eventClaimTestDB(t)

	if _, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened"); err != nil || !claimed {
		t.Fatalf("expected the event to be claimed, claimed=%v err=%v", claimed, err)
	}

	// Releasing a not-yet-fired claim makes the event retryable.
	if err := ReleaseEventClaim(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened"); err != nil {
		t.Fatalf("ReleaseEventClaim failed: %v", err)
	}
	if _, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened"); err != nil || !claimed {
		t.Fatalf("expected the released event to be claimable again, claimed=%v err=%v", claimed, err)
	}

	// Once fired the claim reports the interaction it produced.
	if err := MarkEventClaimFired(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened", "chat-uuid-1"); err != nil {
		t.Fatalf("MarkEventClaimFired failed: %v", err)
	}
	existing, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened")
	if err != nil {
		t.Fatalf("ClaimEvent after firing failed: %v", err)
	}
	if claimed {
		t.Fatalf("expected a fired event to stay claimed")
	}
	if existing.State != EventClaimStateFired {
		t.Fatalf("expected state %q, got %q", EventClaimStateFired, existing.State)
	}
	if existing.ChatUUID != "chat-uuid-1" {
		t.Fatalf("expected the fired chat uuid to be read back, got %q", existing.ChatUUID)
	}

	// A fired claim must not be released away - that would un-dedupe a real chat.
	if err := ReleaseEventClaim(DB, EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened"); err != nil {
		t.Fatalf("ReleaseEventClaim on a fired claim failed: %v", err)
	}
	var count int64
	if err := DB.Model(&EventClaim{}).
		Where("scope = ? AND trigger_id = ? AND event_key = ?", EventClaimScopeAgentDispatch, "trigger-a", "issue:1:opened").
		Count(&count).Error; err != nil {
		t.Fatalf("failed to count claims: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the fired claim to survive a release, got %d rows", count)
	}
}

// TestPruneEventClaims removes only old fired claims: in-flight claims and
// recent ones stay, so the ledger can be trimmed without losing dedupe state.
func TestPruneEventClaims(t *testing.T) {
	DB := eventClaimTestDB(t)

	if _, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "old:fired"); err != nil || !claimed {
		t.Fatalf("failed to seed the old fired claim, claimed=%v err=%v", claimed, err)
	}
	if err := MarkEventClaimFired(DB, EventClaimScopeAgentDispatch, "trigger-a", "old:fired", "chat-old"); err != nil {
		t.Fatalf("failed to fire the old claim: %v", err)
	}
	old := time.Now().Add(-2 * EventClaimRetention)
	if err := DB.Model(&EventClaim{}).Where("event_key = ?", "old:fired").Update("created_at", old).Error; err != nil {
		t.Fatalf("failed to age the fired claim: %v", err)
	}

	if _, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "inflight:claimed"); err != nil || !claimed {
		t.Fatalf("failed to seed the in-flight claim, claimed=%v err=%v", claimed, err)
	}
	if _, claimed, err := ClaimEvent(DB, EventClaimScopeAgentDispatch, "trigger-a", "recent:fired"); err != nil || !claimed {
		t.Fatalf("failed to seed the recent claim, claimed=%v err=%v", claimed, err)
	}
	if err := MarkEventClaimFired(DB, EventClaimScopeAgentDispatch, "trigger-a", "recent:fired", "chat-recent"); err != nil {
		t.Fatalf("failed to fire the recent claim: %v", err)
	}

	removed, err := PruneEventClaims(DB)
	if err != nil {
		t.Fatalf("PruneEventClaims failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected exactly the aged fired claim to be pruned, removed=%d", removed)
	}

	for key, want := range map[string]int64{
		"old:fired":        0,
		"inflight:claimed": 1,
		"recent:fired":     1,
	} {
		var count int64
		if err := DB.Model(&EventClaim{}).Where("event_key = ?", key).Count(&count).Error; err != nil {
			t.Fatalf("failed to count %q: %v", key, err)
		}
		if count != want {
			t.Fatalf("expected %d rows for %q after the prune, got %d", want, key, count)
		}
	}
}
