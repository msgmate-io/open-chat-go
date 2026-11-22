package suspend

import (
	"testing"
	"time"
)

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

// TestParseInTurnParkBudget pins the ci#1174 bound: an unset, blank, unparsable
// or non-positive budget falls back to the short default (never to the TTL), and
// a configured budget can only shorten the suspension wait.
func TestParseInTurnParkBudget(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset", raw: "", want: DefaultInTurnParkBudget},
		{name: "blank", raw: "  ", want: DefaultInTurnParkBudget},
		{name: "explicit", raw: "5s", want: 5 * time.Second},
		{name: "sub-second", raw: "250ms", want: 250 * time.Millisecond},
		{name: "garbage", raw: "soon", want: DefaultInTurnParkBudget},
		{name: "bare number", raw: "30", want: DefaultInTurnParkBudget},
		{name: "zero", raw: "0s", want: DefaultInTurnParkBudget},
		{name: "negative", raw: "-10s", want: DefaultInTurnParkBudget},
		{name: "clamped to TTL", raw: "1h", want: DefaultTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseInTurnParkBudget(tc.raw); got != tc.want {
				t.Fatalf("parseInTurnParkBudget(%q) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
	// ci#1174: the default has to be far below the 10m suspension TTL, otherwise
	// a confirmable round still eats the whole private sandbox window.
	if DefaultInTurnParkBudget >= DefaultTTL {
		t.Fatalf("DefaultInTurnParkBudget (%s) must be well below DefaultTTL (%s) so an enabled park cannot stall a turn for the full TTL", DefaultInTurnParkBudget, DefaultTTL)
	}
}

// TestInTurnParkBudgetReadsEnv pins that the exported resolver is driven by
// OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET alone and that a garbage value keeps the
// short default instead of disabling the bound.
func TestInTurnParkBudgetReadsEnv(t *testing.T) {
	t.Setenv(InTurnParkBudgetEnvKey, "")
	if got := InTurnParkBudget(); got != DefaultInTurnParkBudget {
		t.Fatalf("InTurnParkBudget() = %s with the key unset, want %s", got, DefaultInTurnParkBudget)
	}
	t.Setenv(InTurnParkBudgetEnvKey, "2s")
	if got := InTurnParkBudget(); got != 2*time.Second {
		t.Fatalf("InTurnParkBudget() = %s with the key set to 2s, want 2s", got)
	}
	t.Setenv(InTurnParkBudgetEnvKey, "not-a-duration")
	if got := InTurnParkBudget(); got != DefaultInTurnParkBudget {
		t.Fatalf("InTurnParkBudget() = %s for an unparsable value, want the %s default", got, DefaultInTurnParkBudget)
	}
}

// TestInTurnParkBudgetEnvKeyName pins the documented key name: deployments and
// runbooks refer to it verbatim.
func TestInTurnParkBudgetEnvKeyName(t *testing.T) {
	if InTurnParkBudgetEnvKey != "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET" {
		t.Fatalf("InTurnParkBudgetEnvKey = %q, want OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET", InTurnParkBudgetEnvKey)
	}
}
