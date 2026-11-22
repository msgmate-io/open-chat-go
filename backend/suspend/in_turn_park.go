package suspend

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// InTurnParkEnvKey is the deployment-wide lever for the ci#1116 in-turn
// confirmation park (ci#1178).
//
// The in-turn park blocks the enclosing generation on an approve/reject
// decision and awaits it for up to Registry.TTL() (10m by default). That
// decision can only be produced from the pending confirmable action the
// handler persists once the generation has RETURNED, so a decider that reads
// durable state structurally cannot approve while the park is live: every
// confirmable round then burns the whole suspension TTL, the park is already
// gone when the approval arrives (the handler logs
// continuation=continuation_queued instead of continuation=resumed_turn), and
// the queued continuation parks again on its own next confirmable call.
//
// The flag therefore defaults to OFF: a confirmable round ends the turn the way
// it did before ci#1116 (terminal pause + pending action), and the approval
// starts an ordinary follow-up turn. Set it to 1/true only where a decision can
// be delivered while the generation is still parked (an interactive client
// driving the fine-grained stream, or after the durable-state phase of ci#1178).
const InTurnParkEnvKey = "OCI_CONFIRMABLE_IN_TURN_PARK"

// InTurnParkEnabled reports whether a generation may park in turn on a
// confirmable tool round. It is read per call so a deployment flag flip needs no
// restart-time wiring; the value is a pure function of the environment.
func InTurnParkEnabled() bool {
	return parseInTurnPark(os.Getenv(InTurnParkEnvKey))
}

// parseInTurnPark parses the raw flag value. Unset, empty and unparsable values
// are OFF - an operator typo must never resurrect the ci#1178 deadlock.
func parseInTurnPark(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}

// InTurnParkBudgetEnvKey bounds the ci#1116 in-turn park even when it is
// explicitly enabled (ci#1174).
//
// With the park enabled the generation awaited the full Registry.TTL() (10m) for
// a decision that the confirmation handler can only persist after that same
// generation has RETURNED. On main that turned every confirmable private
// sandbox interaction into an exact 10m03s "generation finished", which is the
// whole per-test window: the harness timed out, the approve click landed after
// the fact (continuation=continuation_queued) and every sandbox test failed.
// The budget makes the await a bounded, visible wait instead: on expiry the park
// is dropped and the pre-ci#1116 terminal pause is kept, so the turn ends in
// seconds and the approval drives an ordinary follow-up turn.
const InTurnParkBudgetEnvKey = "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET"

// DefaultInTurnParkBudget is the budget used when the key is unset, blank,
// unparsable or non-positive. It is deliberately short: a parked generation can
// only be decided by a client that already has the pending action in hand, so a
// decision that has not arrived in well under a minute is not coming.
const DefaultInTurnParkBudget = 45 * time.Second

// InTurnParkBudget returns the effective budget for a single in-turn park. It is
// read per call (no restart-time wiring) and never exceeds DefaultTTL, so the
// budget can only ever shorten - never extend - the suspension wait.
func InTurnParkBudget() time.Duration {
	return parseInTurnParkBudget(os.Getenv(InTurnParkBudgetEnvKey))
}

// parseInTurnParkBudget parses the raw budget value as a Go duration.
// Unset, blank, unparsable and non-positive values fall back to
// DefaultInTurnParkBudget so an operator typo cannot disable the bound; values
// above DefaultTTL are clamped to it.
func parseInTurnParkBudget(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultInTurnParkBudget
	}
	budget, err := time.ParseDuration(raw)
	if err != nil || budget <= 0 {
		return DefaultInTurnParkBudget
	}
	if budget > DefaultTTL {
		return DefaultTTL
	}
	return budget
}
