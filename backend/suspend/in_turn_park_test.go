package suspend

import (
	"os"
	"strings"
	"testing"
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

// TestCiDeploymentPassesInTurnParkLever guards the ci#1176 follow-up on the
// ci#1178 lever: the ci deployment profile (docker-compose.ci.yaml) is what the
// private `tags=coder` sandbox suite starts the backend from, so a lever that is
// not forwarded there can never be pinned by a run - the container silently
// inherits the unset default and the sandbox has no way to opt in or out.
//
// The regression this protects against: the r99 sandbox run on `main` parked
// every confirmable round for the full suspend.DefaultTTL (10m) before the
// approval could even be persisted, which consumed the whole 720s interaction
// window and failed ten unrelated coder interactions with a blank assertion.
// Forwarding the key makes the park state explicit and reproducible per run.
func TestCiDeploymentPassesInTurnParkLever(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.ci.yaml")
	if err != nil {
		t.Fatalf("read ci deployment profile: %v", err)
	}
	profile := string(raw)
	if !strings.Contains(profile, "${"+InTurnParkEnvKey+":-}") {
		t.Errorf("docker-compose.ci.yaml must pass %s through to the backend service", InTurnParkEnvKey)
	}
}
