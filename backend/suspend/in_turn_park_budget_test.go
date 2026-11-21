package suspend

import (
	"testing"
	"time"
)

// TestParseInTurnParkBudget pins the ci#1175 park budget resolver: the value is a
// duration string, and every unusable value has to fall back to the default
// instead of removing the bound or making the park wait forever.
func TestParseInTurnParkBudget(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "unset", raw: "", want: DefaultInTurnParkBudget},
		{name: "blank", raw: "   ", want: DefaultInTurnParkBudget},
		{name: "seconds", raw: "45s", want: 45 * time.Second},
		{name: "minutes with spaces", raw: " 2m ", want: 2 * time.Minute},
		{name: "garbage", raw: "soon", want: DefaultInTurnParkBudget},
		{name: "bare number", raw: "90", want: DefaultInTurnParkBudget},
		{name: "zero", raw: "0s", want: DefaultInTurnParkBudget},
		{name: "negative", raw: "-30s", want: DefaultInTurnParkBudget},
		{name: "below floor", raw: "1ms", want: minInTurnParkBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseInTurnParkBudget(tc.raw); got != tc.want {
				t.Fatalf("parseInTurnParkBudget(%q) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}

// TestInTurnParkBudgetReadsEnv pins that the exported resolver is driven by
// OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET alone, needs no restart-time wiring and
// defaults to something strictly shorter than the suspension TTL - an undecided
// park must never consume the whole interaction window (ci#1175).
func TestInTurnParkBudgetReadsEnv(t *testing.T) {
	t.Setenv(InTurnParkBudgetEnvKey, "")
	if got := InTurnParkBudget(); got != DefaultInTurnParkBudget {
		t.Fatalf("InTurnParkBudget() = %s with the key unset, want %s", got, DefaultInTurnParkBudget)
	}
	if DefaultInTurnParkBudget >= DefaultTTL {
		t.Fatalf(
			"DefaultInTurnParkBudget = %s, want strictly shorter than the suspension TTL %s (ci#1175: a parked generation must not block for a whole TTL)",
			DefaultInTurnParkBudget, DefaultTTL,
		)
	}
	t.Setenv(InTurnParkBudgetEnvKey, "30s")
	if got := InTurnParkBudget(); got != 30*time.Second {
		t.Fatalf("InTurnParkBudget() = %s with the key set to 30s, want 30s", got)
	}
	t.Setenv(InTurnParkBudgetEnvKey, "nonsense")
	if got := InTurnParkBudget(); got != DefaultInTurnParkBudget {
		t.Fatalf("InTurnParkBudget() = %s for an unparsable value, want the default %s", got, DefaultInTurnParkBudget)
	}
}

// TestInTurnParkBudgetEnvKeyName pins the documented key name: runbooks and
// deployment manifests refer to it verbatim.
func TestInTurnParkBudgetEnvKeyName(t *testing.T) {
	if InTurnParkBudgetEnvKey != "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET" {
		t.Fatalf("InTurnParkBudgetEnvKey = %q, want OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET", InTurnParkBudgetEnvKey)
	}
}
