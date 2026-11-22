package suspend

import (
	"os"
	"strconv"
	"strings"
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
