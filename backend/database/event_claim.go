package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EventClaim is the durable dispatch idempotency ledger (ci#1047). It records
// that one trigger event (identified by its trigger + event key) has already
// been claimed by exactly one dispatch, so a retried or replayed delivery -
// a webhook redelivery, a cron poll that fires the same event twice, a caller
// re-POSTing the same dispatch after a timeout - cannot start a second agent.
//
// The claim lives in core (open-chat-go) rather than in the private
// git-integration module that owns the `poll_triggers` rail, because core owns
// the interaction side effect (`POST /api/v1/agents/dispatch`): the rail
// consumes the claim by passing `trigger_id` + `idempotency_key`.
//
// (scope, trigger_id, event_key) is unique *as a tuple*, not on event_key
// alone: the same event key (eg an issue number, or a day stamp) can legitimately
// exist under different triggers, and only the combination identifies a delivery.
type EventClaim struct {
	Model
	// Scope names the dispatch rail the claim belongs to, eg "agent_dispatch".
	// It keeps independent producers from colliding on each other's keys.
	Scope string `json:"scope" gorm:"not null;uniqueIndex:idx_event_claim_scope_key"`
	// TriggerID is the producer's stable trigger identity (the poll_triggers
	// row id, or any other rail's trigger key). Empty means "no trigger", ie an
	// ad-hoc dispatch that still wants a dedupe key.
	TriggerID string `json:"trigger_id" gorm:"not null;default:'';uniqueIndex:idx_event_claim_scope_key"`
	// EventKey is the event identity inside the trigger, eg "issue:1047:opened".
	EventKey string `json:"event_key" gorm:"not null;uniqueIndex:idx_event_claim_scope_key"`
	// ChatUUID is the interaction the claim was fulfilled with, read back on a
	// duplicate so the caller can steer the existing chat instead of starting a
	// new one.
	ChatUUID string `json:"chat_uuid" gorm:"not null;default:''"`
	// State is the claim lifecycle: EventClaimStateClaimed while the dispatch is
	// in flight, EventClaimStateFired once the interaction exists. A released
	// claim (dispatch failed) is deleted, so it can be retried.
	State string `json:"state" gorm:"not null;default:'claimed'"`
}

// TableName pins the ledger table name.
func (EventClaim) TableName() string {
	return "event_claims"
}

// EventClaimStateClaimed marks an in-flight dispatch: the event is taken but the
// interaction does not exist yet.
const EventClaimStateClaimed = "claimed"

// EventClaimStateFired marks a completed dispatch: the interaction exists and
// every further delivery of the same event must be deduplicated.
const EventClaimStateFired = "fired"

// EventClaimScopeAgentDispatch is the scope of the trusted agent dispatch
// surface (`POST /api/v1/agents/dispatch`).
const EventClaimScopeAgentDispatch = "agent_dispatch"

// EventClaimRetention is how long a fired claim is remembered. It bounds the
// ledger while staying well above any realistic retry/redelivery window.
const EventClaimRetention = 30 * 24 * time.Hour

// ClaimEvent claims one (scope, triggerID, eventKey) event for a dispatch. The
// insert is an ON CONFLICT DO NOTHING, so two concurrent callers race on the
// database instead of in application code: exactly one gets claimed=true, the
// other gets claimed=false plus the row the winner already stored (whose
// ChatUUID may still be empty while the winner's dispatch is in flight).
//
// The claim is intentionally inserted *before* the side effect (the interaction
// creation), so a crash in between leaves a claimed-but-not-fired row rather
// than a second agent.
func ClaimEvent(DB *gorm.DB, scope, triggerID, eventKey string) (EventClaim, bool, error) {
	var claim EventClaim
	if DB == nil {
		return claim, false, nil
	}
	if scope == "" {
		scope = EventClaimScopeAgentDispatch
	}
	claim = EventClaim{
		Scope:     scope,
		TriggerID: triggerID,
		EventKey:  eventKey,
		State:     EventClaimStateClaimed,
	}
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&claim)
	if result.Error != nil {
		return EventClaim{}, false, result.Error
	}
	if result.RowsAffected > 0 {
		return claim, true, nil
	}
	// Already claimed: read the winner's row back so the caller can report the
	// chat the event was fulfilled with.
	var existing EventClaim
	if err := DB.Where("scope = ? AND trigger_id = ? AND event_key = ?", scope, triggerID, eventKey).
		First(&existing).Error; err != nil {
		return EventClaim{}, false, err
	}
	return existing, false, nil
}

// MarkEventClaimFired records that the claimed event really started the given
// interaction, so later deliveries dedupe onto that chat.
func MarkEventClaimFired(DB *gorm.DB, scope, triggerID, eventKey, chatUUID string) error {
	if DB == nil {
		return nil
	}
	if scope == "" {
		scope = EventClaimScopeAgentDispatch
	}
	return DB.Model(&EventClaim{}).
		Where("scope = ? AND trigger_id = ? AND event_key = ?", scope, triggerID, eventKey).
		Updates(map[string]interface{}{
			"state":     EventClaimStateFired,
			"chat_uuid": chatUUID,
		}).Error
}

// ReleaseEventClaim drops a claim whose dispatch failed, so the event can be
// retried instead of being lost or deduplicated against a chat that never
// existed. It is a no-op for an already fired claim.
func ReleaseEventClaim(DB *gorm.DB, scope, triggerID, eventKey string) error {
	if DB == nil {
		return nil
	}
	if scope == "" {
		scope = EventClaimScopeAgentDispatch
	}
	return DB.Where("scope = ? AND trigger_id = ? AND event_key = ? AND state = ?",
		scope, triggerID, eventKey, EventClaimStateClaimed).
		Delete(&EventClaim{}).Error
}

// PruneEventClaims deletes fired claims older than EventClaimRetention and
// reports how many rows went away. Claimed rows are kept: they are in-flight
// dispatches, and the release path is what removes them.
func PruneEventClaims(DB *gorm.DB) (int64, error) {
	if DB == nil {
		return 0, nil
	}
	cutoff := time.Now().Add(-EventClaimRetention)
	result := DB.Where("state = ? AND created_at < ?", EventClaimStateFired, cutoff).
		Delete(&EventClaim{})
	return result.RowsAffected, result.Error
}
