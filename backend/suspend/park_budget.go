package suspend

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ParkWaitEnvKey overrides how long an in-turn confirmation park may block the
// enclosing generation (ci#1177/ci#1175). The value is a Go duration (for
// example "15s") or a bare number of seconds.
//
// This is the ONE lever for the in-turn park bound. ci#1177 introduced it as
// OCI_CONFIRMABLE_PARK_WAIT and ci#1175 introduced a second, competing lever
// (OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET) for the same wait; the consolidation
// kept this key because it is the one the shipped runbook (DEVELOPMENT.md,
// ci#1172) and the ci#1177 code already reference. The ci#1175 key survives as
// the documented alias ParkWaitEnvKeyAlias.
const ParkWaitEnvKey = "OCI_CONFIRMABLE_PARK_WAIT"

// ParkWaitEnvKeyAlias is the legacy ci#1175 spelling of ParkWaitEnvKey. It is
// still honoured - after the canonical key - so a deployment that adopted
// OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET keeps working; the canonical key always
// wins when both are set.
const ParkWaitEnvKeyAlias = "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET"

// DefaultParkWaitBudget caps the in-turn confirmation park independently of the
// suspension TTL.
//
// ci#1177: every confirmable round used to await its decision for the whole
// Registry.TTL() (10m by default). A decider that reads durable state can only
// approve once the generation has RETURNED, so the park was already gone when
// the approval arrived: the private sandbox interactions all ended with
// "pending confirmable action approved" followed by no bot turn and an
// interaction timeout. Waiting the entire TTL made a single confirmable round
// indistinguishable from a dead generation, so the park now yields well before
// the durable record expires and the approval is served by the ordinary
// confirmation handler instead of into a finished generation.
//
// ci#1175 reported the same stall from the private-sandbox side and proposed a
// longer default (90s) with the same shape; the consolidation keeps the single
// 20s default documented by the ci#1172 runbook. A park only has to bridge a
// decision that arrives while the generation is running, and every second spent
// parked is a second the interaction is not doing provider work.
const DefaultParkWaitBudget = 20 * time.Second

// minParkWaitBudget keeps a tiny configured TTL (tests, short-lived sandboxes)
// from collapsing the park to a sub-second window that no decider can meet.
const minParkWaitBudget = 1 * time.Second

// ParkWaitBudget returns how long an in-turn confirmation park may wait for a
// decision given the registry TTL. The result is always strictly shorter than
// the TTL, so a park can never outlive the durable suspension it waits on.
func ParkWaitBudget(ttl time.Duration) time.Duration {
	budget := DefaultParkWaitBudget
	if parsed, ok := parseParkWait(strings.TrimSpace(os.Getenv(ParkWaitEnvKey))); ok {
		budget = parsed
	} else if parsed, ok := parseParkWait(strings.TrimSpace(os.Getenv(ParkWaitEnvKeyAlias))); ok {
		budget = parsed
	}
	if ttl <= 0 {
		return budget
	}
	// Never wait past half the TTL: the confirmation has to stay durable for a
	// while after the park gives up, otherwise a late approval has nothing to
	// deliver into.
	if half := ttl / 2; budget > half {
		budget = half
	}
	if budget < minParkWaitBudget && ttl > minParkWaitBudget {
		budget = minParkWaitBudget
	}
	if budget >= ttl {
		budget = ttl / 2
	}
	if budget <= 0 {
		budget = ttl
	}
	return budget
}

// parseParkWait accepts a Go duration or a bare number of seconds and rejects
// non-positive values.
func parseParkWait(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, false
		}
		return d, true
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}
