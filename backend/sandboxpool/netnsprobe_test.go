package sandboxpool

import (
	"strings"
	"testing"
	"time"
)

// withNetnsLevers clears the two capability levers so a test observes the
// read-only host probe instead of whatever the CI host exports.
func withNetnsLevers(t *testing.T) {
	t.Helper()
	for _, key := range []string{EnvNetworkNetnsAvailable, EnvNetworkNetnsProvider} {
		t.Setenv(key, "")
	}
}

func TestProbeNetnsCapabilityPrefersSlirp4netns(t *testing.T) {
	// slirp4netns plus the tun device is the preferred mechanism and must win
	// even when the userns fallback would also qualify.
	capability := ProbeNetnsCapability(NetnsHostState{
		Slirp4netns: true,
		TunDevice:   true,
		UnshareTool: true,
	})
	if !capability.Available || capability.Provider != NetnsProviderSlirp4netns {
		t.Fatalf("capability = %+v, want available via %s", capability, NetnsProviderSlirp4netns)
	}
	if len(capability.MissingFacts) != 0 {
		t.Errorf("a fully probed host reported missing facts: %v", capability.MissingFacts)
	}
	if !strings.Contains(strings.Join(capability.Reasons, " "), NetnsTunPath) {
		t.Errorf("reasons do not name the tun device: %v", capability.Reasons)
	}
}

func TestProbeNetnsCapabilityFallsBackToUserNamespace(t *testing.T) {
	cases := map[string]NetnsHostState{
		"userns knob present and enabled": {
			UnprivilegedUsernsClone: 1,
			UsernsKnobPresent:       true,
			UnshareTool:             true,
		},
		"userns knob absent": {
			UnprivilegedUsernsClone: -1,
			UnshareTool:             true,
		},
		"already inside a user namespace": {
			UnprivilegedUsernsClone: -1,
			InUserNamespace:         true,
			UnshareTool:             true,
		},
	}
	for name, state := range cases {
		capability := ProbeNetnsCapability(state)
		if !capability.Available || capability.Provider != NetnsProviderUserNamespace {
			t.Errorf("%s: capability = %+v, want available via %s", name, capability, NetnsProviderUserNamespace)
		}
	}
}

func TestProbeNetnsCapabilityFailsClosed(t *testing.T) {
	cases := map[string]struct {
		state          NetnsHostState
		wantMissing    string
		wantReasonPart string
	}{
		"slirp4netns without the tun device": {
			state:          NetnsHostState{Slirp4netns: true},
			wantMissing:    NetnsTunPath,
			wantReasonPart: "cannot be created",
		},
		"user namespaces disabled": {
			state:          NetnsHostState{UnprivilegedUsernsClone: 0, UsernsKnobPresent: true, UnshareTool: true},
			wantReasonPart: "unprivileged user namespaces are disabled",
		},
		"no unshare entry point": {
			state:          NetnsHostState{UnprivilegedUsernsClone: 1, UsernsKnobPresent: true},
			wantMissing:    "unshare",
			wantReasonPart: "no unshare(1) entry point",
		},
		"bare pod": {
			state:          NetnsHostState{UnprivilegedUsernsClone: -1},
			wantReasonPart: "ErrWorkspaceIsolationUnavailable",
		},
	}
	for name, tc := range cases {
		capability := ProbeNetnsCapability(tc.state)
		if capability.Available {
			t.Errorf("%s: probe reported available, want fail closed", name)
			continue
		}
		if capability.Provider != NetnsProviderUnavailable {
			t.Errorf("%s: provider = %q, want %q", name, capability.Provider, NetnsProviderUnavailable)
		}
		if tc.wantMissing != "" {
			if !strings.Contains(strings.Join(capability.MissingFacts, ","), tc.wantMissing) {
				t.Errorf("%s: missing facts = %v, want %s", name, capability.MissingFacts, tc.wantMissing)
			}
		}
		if !strings.Contains(strings.Join(capability.Reasons, " "), tc.wantReasonPart) {
			t.Errorf("%s: reasons = %v, want a mention of %q", name, capability.Reasons, tc.wantReasonPart)
		}
	}
}

func TestProbeNetnsCapabilityIsDeterministic(t *testing.T) {
	// Two probes of the same pod must render the same report: missing facts are
	// sorted and de-duplicated, so a report diff means a real host change.
	state := NetnsHostState{
		Slirp4netns: true, // false tun device: the preferred path adds a missing fact
		MissingFact: []string{NetnsTunPath, "unshare", NetnsTunPath},
	}
	first := ProbeNetnsCapability(state)
	second := ProbeNetnsCapability(state)
	if strings.Join(first.MissingFacts, ",") != strings.Join(second.MissingFacts, ",") {
		t.Fatalf("missing facts are not stable: %v vs %v", first.MissingFacts, second.MissingFacts)
	}
	facts := first.MissingFacts
	if len(facts) != 2 {
		t.Fatalf("missing facts = %v, want the duplicate dropped", facts)
	}
	if facts[0] != NetnsTunPath || facts[1] != "unshare" {
		t.Errorf("missing facts = %v, want them sorted", facts)
	}
	// A missing fact must never be silently dropped from the verdict.
	if !strings.Contains(strings.Join(first.Reasons, " "), "cannot be created") {
		t.Errorf("reasons = %v, want the missing fact named", first.Reasons)
	}
}

func TestNetnsCapabilityEnvAndReportLines(t *testing.T) {
	capability := ProbeNetnsCapability(NetnsHostState{Slirp4netns: true, TunDevice: true})
	env := capability.Env()
	if env["CODER_NETNS_AVAILABLE"] != "true" || env["CODER_NETNS_PROVIDER"] != NetnsProviderSlirp4netns {
		t.Errorf("env = %v", env)
	}
	lines := strings.Join(capability.ReportLines(), "\n")
	if !strings.Contains(lines, "netns_available=true") || !strings.Contains(lines, "netns_provider="+NetnsProviderSlirp4netns) {
		t.Errorf("report lines = %v", capability.ReportLines())
	}

	// The unavailable verdict must be just as readable, and must not leak an
	// empty provider that reads like a broken field.
	unavailable := ProbeNetnsCapability(NetnsHostState{UnprivilegedUsernsClone: -1})
	if got := unavailable.Env()["CODER_NETNS_PROVIDER"]; got != NetnsProviderUnavailable {
		t.Errorf("unavailable provider = %q, want %q", got, NetnsProviderUnavailable)
	}
	if !strings.Contains(strings.Join(unavailable.ReportLines(), "\n"), "netns_available=false") {
		t.Errorf("unavailable report lines = %v", unavailable.ReportLines())
	}
}

func TestHostNetnsCapabilityMatchesDirectProbe(t *testing.T) {
	// The convenience wrapper must be exactly the probe of the real host, so the
	// lease path and the status surface can never drift apart.
	direct := ProbeNetnsCapability(DefaultNetnsHostState())
	host := HostNetnsCapability()
	if direct.Available != host.Available || direct.Provider != host.Provider {
		t.Errorf("wrapper = %+v, direct = %+v", host, direct)
	}
}

func TestIsNonIdentityUIDMap(t *testing.T) {
	cases := map[string]bool{
		"         0          0 4294967295\n": false,
		"      1000          0          1\n": true,
		"":                                   false,
		"0 0\n":                              false,
		"not numbers here\n":                 false,
	}
	for raw, want := range cases {
		if got := isNonIdentityUIDMap(raw); got != want {
			t.Errorf("isNonIdentityUIDMap(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestNetworkPolicyFromEnvNetnsLevers(t *testing.T) {
	withNetnsLevers(t)

	// A pinned lever wins over the probe, in both directions: a rollout must be
	// able to both grant and deny the capability without touching the host.
	t.Setenv(EnvNetworkNetnsAvailable, "1")
	t.Setenv(EnvNetworkNetnsProvider, NetnsProviderSlirp4netns)
	pinned := NetworkPolicyFromEnv()
	if !pinned.NetnsAvailable || pinned.NetnsProvider != NetnsProviderSlirp4netns {
		t.Fatalf("pinned policy = available %v via %q", pinned.NetnsAvailable, pinned.NetnsProvider)
	}
	if got := networkProviderName(pinned); got != NetnsProviderSlirp4netns {
		t.Errorf("provider name = %q", got)
	}

	t.Setenv(EnvNetworkNetnsAvailable, "off")
	denied := NetworkPolicyFromEnv()
	if denied.NetnsAvailable {
		t.Fatal("EnvNetworkNetnsAvailable=off must deny the capability")
	}
	if denied.NetnsProvider != "" {
		t.Errorf("a denied policy kept provider %q", denied.NetnsProvider)
	}
	if got := networkProviderName(denied); got != NetnsProviderUnavailable {
		t.Errorf("provider name = %q, want %q", got, NetnsProviderUnavailable)
	}

	// A lever that only asserts availability (no mechanism pinned, no probe on
	// this host) must not claim a mechanism: it reports "declared".
	t.Setenv(EnvNetworkNetnsAvailable, "1")
	t.Setenv(EnvNetworkNetnsProvider, "")
	declared := NetworkPolicyFromEnv()
	if !declared.NetnsAvailable {
		t.Fatal("EnvNetworkNetnsAvailable=1 must grant the capability")
	}
	probe := HostNetnsCapability()
	wantProvider := NetnsProviderDeclared
	if probe.Available {
		// This host really can do netns, so the probe legitimately names it
		// instead of falling back to the "declared" spelling.
		wantProvider = probe.Provider
		if declared.NetnsProvider != probe.Provider {
			t.Errorf("provider = %q, want the probed %q", declared.NetnsProvider, probe.Provider)
		}
	} else if declared.NetnsProvider != "" {
		t.Errorf("provider = %q, want empty (rendered as %s)", declared.NetnsProvider, NetnsProviderDeclared)
	}
	if got := networkProviderName(declared); got != wantProvider {
		t.Errorf("provider name = %q, want %q", got, wantProvider)
	}
}

func TestNetworkPlanStatusRendersNetnsProviderNotes(t *testing.T) {
	withNetnsLevers(t)
	t.Setenv(EnvNetworkMode, NetworkModeNetns)
	t.Setenv(EnvNetworkNetnsAvailable, "1")
	t.Setenv(EnvNetworkNetnsProvider, NetnsProviderUserNamespace)
	t.Setenv(EnvNetworkPortBase, "20100")

	status := NetworkPlanStatusFromEnv(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	if status.PolicyError != "" {
		t.Fatalf("policy error = %q", status.PolicyError)
	}
	if status.NetnsProvider != NetnsProviderUserNamespace {
		t.Errorf("netns_provider = %q", status.NetnsProvider)
	}
	if status.EnvNetnsAvailable != "1" || status.EnvNetnsProvider != NetnsProviderUserNamespace {
		t.Errorf("env levers = %q/%q", status.EnvNetnsAvailable, status.EnvNetnsProvider)
	}
	notes := strings.Join(status.Notes, " ")
	if !strings.Contains(notes, "netns is available via "+NetnsProviderUserNamespace) {
		t.Errorf("notes do not name the provider: %v", status.Notes)
	}
	if !strings.Contains(notes, EnvNetworkNetnsAvailable) {
		t.Errorf("notes do not name the pinning lever: %v", status.Notes)
	}
}
