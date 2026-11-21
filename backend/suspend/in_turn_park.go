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

// InTurnParkBudgetEnvKey bounds a single ci#1178 in-turn park (ci#1175).
//
// Even on a deployment that enables the park, an undecided park must never
// consume the whole suspension TTL. The TTL exists so an orphaned suspension
// survives until a *queued* continuation picks it up, while the in-turn park
// can at most bridge a decision that arrives while the generation is still
// running - an interaction window of seconds to a couple of minutes. Waiting a
// full DefaultTTL (10m) inside a generation is what the ci#1175 relay shows:
// confirmable rounds whose generation blocked until the harness killed the
// interaction, with the approval arriving afterwards as
// continuation=continuation_queued. The budget is therefore deliberately
// shorter than the TTL and is consulted only by the in-turn park; an ordinary
// suspension keeps using Registry.TTL().
const InTurnParkBudgetEnvKey = "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET"

// DefaultInTurnParkBudget is how long an enabled in-turn park waits for a
// decision before it gives up and lets the caller end the turn.
const DefaultInTurnParkBudget = 90 * time.Second

// minInTurnParkBudget keeps a typo'd tiny value from turning the in-turn park
// into a no-op that can never observe an interactive click.
const minInTurnParkBudget = time.Second

// InTurnParkBudget reports the wait budget of a single in-turn park. It is read
// per call, so raising it needs no restart-time wiring.
func InTurnParkBudget() time.Duration {
	return parseInTurnParkBudget(os.Getenv(InTurnParkBudgetEnvKey))
}

// parseInTurnParkBudget parses the raw budget value. Unset, empty, unparsable
// and non-positive values fall back to DefaultInTurnParkBudget, and a value
// below minInTurnParkBudget is raised to it - a bad value must never make the
// park fail instantly or wait unboundedly.
func parseInTurnParkBudget(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultInTurnParkBudget
	}
	budget, err := time.ParseDuration(raw)
	if err != nil || budget <= 0 {
		return DefaultInTurnParkBudget
	}
	if budget < minInTurnParkBudget {
		return minInTurnParkBudget
	}
	return budget
}
