package suspend

import (
	"testing"
	"time"
)

func TestParkWaitBudgetStaysBelowTTL(t *testing.T) {
	t.Setenv(ParkWaitEnvKey, "")
	for _, ttl := range []time.Duration{
		2 * time.Second,
		10 * time.Minute,
		24 * time.Hour,
	} {
		budget := ParkWaitBudget(ttl)
		if budget <= 0 {
			t.Fatalf("ttl %s: budget must be positive, got %s", ttl, budget)
		}
		if budget >= ttl {
			t.Fatalf("ttl %s: budget %s must be shorter than the TTL", ttl, budget)
		}
	}
}

func TestParkWaitBudgetDefault(t *testing.T) {
	t.Setenv(ParkWaitEnvKey, "")
	if got := ParkWaitBudget(time.Hour); got != DefaultParkWaitBudget {
		t.Fatalf("expected default budget %s, got %s", DefaultParkWaitBudget, got)
	}
}

func TestParkWaitBudgetEnvOverride(t *testing.T) {
	t.Setenv(ParkWaitEnvKey, "15s")
	if got := ParkWaitBudget(time.Hour); got != 15*time.Second {
		t.Fatalf("expected 15s from %s, got %s", ParkWaitEnvKey, got)
	}
	t.Setenv(ParkWaitEnvKey, "42")
	if got := ParkWaitBudget(time.Hour); got != 42*time.Second {
		t.Fatalf("expected bare seconds to parse as 42s, got %s", got)
	}
	t.Setenv(ParkWaitEnvKey, "not-a-duration")
	if got := ParkWaitBudget(time.Hour); got != DefaultParkWaitBudget {
		t.Fatalf("expected fallback to default on invalid value, got %s", got)
	}
}

func TestParkWaitBudgetClampedByTTL(t *testing.T) {
	t.Setenv(ParkWaitEnvKey, "10m")
	ttl := 4 * time.Second
	if got := ParkWaitBudget(ttl); got >= ttl {
		t.Fatalf("budget %s must stay below ttl %s", got, ttl)
	}
}
