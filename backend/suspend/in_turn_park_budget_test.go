package suspend

import (
	"testing"
	"time"
)

// TestParkWaitBudgetEnvAlias pins the ci#1175/ci#1177 consolidation: the ci#1177
// key OCI_CONFIRMABLE_PARK_WAIT is canonical, the ci#1175 spelling
// OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET is a documented alias, and the canonical
// key wins when both are set.
func TestParkWaitBudgetEnvAlias(t *testing.T) {
	t.Setenv(ParkWaitEnvKey, "")
	t.Setenv(ParkWaitEnvKeyAlias, "")
	if got := ParkWaitBudget(time.Hour); got != DefaultParkWaitBudget {
		t.Fatalf("ParkWaitBudget = %s with both keys unset, want the default %s", got, DefaultParkWaitBudget)
	}

	t.Setenv(ParkWaitEnvKeyAlias, "30s")
	if got := ParkWaitBudget(time.Hour); got != 30*time.Second {
		t.Fatalf("ParkWaitBudget = %s with only the %s alias set, want 30s", got, ParkWaitEnvKeyAlias)
	}

	t.Setenv(ParkWaitEnvKey, "5s")
	if got := ParkWaitBudget(time.Hour); got != 5*time.Second {
		t.Fatalf("ParkWaitBudget = %s with both keys set, want the canonical %s value 5s", got, ParkWaitEnvKey)
	}

	t.Setenv(ParkWaitEnvKey, "nonsense")
	if got := ParkWaitBudget(time.Hour); got != 30*time.Second {
		t.Fatalf("ParkWaitBudget = %s with an unparsable canonical value, want the alias value 30s", got)
	}
}

// TestParkWaitBudgetEnvKeyName pins the documented key names: the runbook and
// the deployment manifests refer to them verbatim (ci#1172/ci#1177/ci#1175).
func TestParkWaitBudgetEnvKeyName(t *testing.T) {
	if ParkWaitEnvKey != "OCI_CONFIRMABLE_PARK_WAIT" {
		t.Fatalf("ParkWaitEnvKey = %q, want OCI_CONFIRMABLE_PARK_WAIT", ParkWaitEnvKey)
	}
	if ParkWaitEnvKeyAlias != "OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET" {
		t.Fatalf("ParkWaitEnvKeyAlias = %q, want the legacy OCI_CONFIRMABLE_IN_TURN_PARK_BUDGET", ParkWaitEnvKeyAlias)
	}
}

// TestDefaultParkWaitBudgetShorterThanTTL keeps the single default strictly
// below the suspension TTL: an undecided park must never consume the whole
// interaction window (ci#1175/ci#1177).
func TestDefaultParkWaitBudgetShorterThanTTL(t *testing.T) {
	if DefaultParkWaitBudget >= DefaultTTL {
		t.Fatalf(
			"DefaultParkWaitBudget = %s, want strictly shorter than the suspension TTL %s",
			DefaultParkWaitBudget, DefaultTTL,
		)
	}
}
