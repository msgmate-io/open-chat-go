package suspend

import "testing"

// TestParseInTurnPark pins the ci#1178 default: the in-turn confirmation park is
// OFF unless the deployment sets OCI_CONFIRMABLE_IN_TURN_PARK explicitly.
//
// The in-turn park blocks the generation on a decision that can only be produced
// after that generation returned (the pending confirmable_actions row is
// persisted by the handler once the turn is over), so an unset or unparsable
// value must never park - every confirmable round would burn the whole
// suspension TTL and the approval would still fall back to
// continuation=continuation_queued.
func TestParseInTurnPark(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "unset", raw: "", want: false},
		{name: "blank", raw: "   ", want: false},
		{name: "one", raw: "1", want: true},
		{name: "true", raw: "true", want: true},
		{name: "TRUE with spaces", raw: " TRUE ", want: true},
		{name: "zero", raw: "0", want: false},
		{name: "false", raw: "false", want: false},
		{name: "garbage", raw: "yes-please", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseInTurnPark(tc.raw); got != tc.want {
				t.Fatalf("parseInTurnPark(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestInTurnParkEnabledReadsEnv pins that the exported resolver is driven by
// OCI_CONFIRMABLE_IN_TURN_PARK alone and needs no restart-time wiring.
func TestInTurnParkEnabledReadsEnv(t *testing.T) {
	t.Setenv(InTurnParkEnvKey, "")
	if InTurnParkEnabled() {
		t.Fatal("InTurnParkEnabled() = true with the key unset, want false (ci#1178: the park must be opt-in)")
	}
	t.Setenv(InTurnParkEnvKey, "1")
	if !InTurnParkEnabled() {
		t.Fatal("InTurnParkEnabled() = false with the key set to 1, want true")
	}
	t.Setenv(InTurnParkEnvKey, "garbage")
	if InTurnParkEnabled() {
		t.Fatal("InTurnParkEnabled() = true for an unparsable value, want false so an operator typo cannot resurrect the deadlock")
	}
}

// TestInTurnParkEnvKeyName pins the documented key name: deployments and runbooks
// refer to it verbatim.
func TestInTurnParkEnvKeyName(t *testing.T) {
	if InTurnParkEnvKey != "OCI_CONFIRMABLE_IN_TURN_PARK" {
		t.Fatalf("InTurnParkEnvKey = %q, want OCI_CONFIRMABLE_IN_TURN_PARK", InTurnParkEnvKey)
	}
}
