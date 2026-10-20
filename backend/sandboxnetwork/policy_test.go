package sandboxnetwork

import (
	"fmt"
	"testing"
)

// noEnv is the lookup used when a test wants the deployment env to be unset, so
// a developer's shell cannot influence the resolution.
func noEnv(string) string { return "" }

func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestResolveDefaultsToIsolatedWithoutSharing(t *testing.T) {
	policy := ResolveWithLookup(nil, "/workspace", noEnv)

	if policy.Mode != ModeIsolated {
		t.Fatalf("mode = %q, want %q", policy.Mode, ModeIsolated)
	}
	if !policy.Isolated() || policy.Shared() {
		t.Fatalf("Isolated()/Shared() = %v/%v, want true/false", policy.Isolated(), policy.Shared())
	}
	if policy.Netns != "private" {
		t.Fatalf("netns = %q, want private", policy.Netns)
	}
	if policy.Loopback != "127.0.0.1" {
		t.Fatalf("loopback = %q, want 127.0.0.1", policy.Loopback)
	}
	if policy.Slot != UnassignedSlot {
		t.Fatalf("slot = %d, want %d", policy.Slot, UnassignedSlot)
	}
	if policy.PortBase != 0 || policy.PortStride != 0 || policy.PortMin != 0 || policy.PortMax != 0 {
		t.Fatalf("isolated policy allocated ports: %+v", policy)
	}
	// An isolated workspace must not have its conventional ports moved.
	if got := policy.PortFor(3000); got != 3000 {
		t.Fatalf("PortFor(3000) = %d, want 3000", got)
	}
	if got := policy.Env[EnvLoopback]; got != "127.0.0.1" {
		t.Fatalf("env %s = %q, want 127.0.0.1", EnvLoopback, got)
	}
	if len(policy.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", policy.Warnings)
	}
	if policy.IsolationVerdict() != "own-netns" {
		t.Fatalf("verdict = %q, want own-netns", policy.IsolationVerdict())
	}
	if len(Windows(policy)) != 0 {
		t.Fatalf("isolated policy must have no port windows")
	}
}

func TestResolveSharedPoolDefaultsToBoundedPortAllocation(t *testing.T) {
	config := map[string]interface{}{ConfigSharedMode: "shared"}
	policy := ResolveWithLookup(config, "/workspace/abc", envOf(map[string]string{
		ConfigSharedMode: "shared",
	}))

	if policy.Mode != DefaultSharedMode {
		t.Fatalf("mode = %q, want %q (a shared pool is never left unallocated)", policy.Mode, DefaultSharedMode)
	}
	if !policy.Shared() || policy.Netns != "shared" {
		t.Fatalf("shared policy not marked shared: %+v", policy)
	}
	if policy.PortBase != DefaultPortBase || policy.PortStride != DefaultPortStride {
		t.Fatalf("port base/stride = %d/%d, want %d/%d", policy.PortBase, policy.PortStride, DefaultPortBase, DefaultPortStride)
	}
	if policy.SlotCount != DefaultSlotCount {
		t.Fatalf("slot count = %d, want %d", policy.SlotCount, DefaultSlotCount)
	}
	if policy.Slot < 0 || policy.Slot >= policy.SlotCount {
		t.Fatalf("slot %d out of range 0..%d", policy.Slot, policy.SlotCount-1)
	}
	if policy.PortMin != policy.PortBase+policy.Slot*policy.PortStride {
		t.Fatalf("port min = %d, want %d", policy.PortMin, policy.PortBase+policy.Slot*policy.PortStride)
	}
	if policy.PortMax != policy.PortMin+policy.PortStride-1 {
		t.Fatalf("port max = %d, want %d", policy.PortMax, policy.PortMin+policy.PortStride-1)
	}
	if got := policy.Env[EnvPortMin]; got != fmt.Sprint(policy.PortMin) {
		t.Fatalf("env %s = %q, want %d", EnvPortMin, got, policy.PortMin)
	}
	if got := policy.Env[EnvPortMax]; got != fmt.Sprint(policy.PortMax) {
		t.Fatalf("env %s = %q, want %d", EnvPortMax, got, policy.PortMax)
	}
	if policy.IsolationVerdict() != "shared-netns-shared-localhost" {
		t.Fatalf("verdict = %q", policy.IsolationVerdict())
	}
}

func TestResolveMovesConventionalPortsInsideTheSlice(t *testing.T) {
	config := map[string]interface{}{ConfigSharedMode: "shared"}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)

	for _, conventional := range []int{3000, 8080, 5173, 1} {
		port := policy.PortFor(conventional)
		if port < policy.PortMin || port > policy.PortMax {
			t.Fatalf("PortFor(%d) = %d, outside %d..%d", conventional, port, policy.PortMin, policy.PortMax)
		}
	}
	// Distinct conventional ports inside one stride must stay distinct.
	if policy.PortFor(3000) == policy.PortFor(8080) {
		t.Fatalf("3000 and 8080 collapsed onto the same slice port")
	}
	// The mapping is stable.
	if policy.PortFor(3000) != policy.PortFor(3000) {
		t.Fatalf("PortFor is not deterministic")
	}
}

func TestResolveLoopbackAliasKeepsConventionalPorts(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode:  "shared",
		ConfigNetworkMode: ModeLoopbackAlias,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)

	if policy.Mode != ModeLoopbackAlias {
		t.Fatalf("mode = %q, want %q", policy.Mode, ModeLoopbackAlias)
	}
	if policy.Netns != "shared" {
		t.Fatalf("netns = %q, want shared", policy.Netns)
	}
	if policy.Loopback == "127.0.0.1" {
		t.Fatalf("loopback alias not assigned: %q", policy.Loopback)
	}
	if policy.PortBase != 0 || policy.PortStride != 0 || policy.PortMin != 0 {
		t.Fatalf("loopback alias must not consume a port slice: %+v", policy)
	}
	if got := policy.PortFor(3000); got != 3000 {
		t.Fatalf("PortFor(3000) = %d, want 3000", got)
	}
	if policy.IsolationVerdict() != "shared-netns-own-loopback" {
		t.Fatalf("verdict = %q", policy.IsolationVerdict())
	}
}

func TestResolveSlotIsStableAndInjective(t *testing.T) {
	const slots = 8
	seen := make(map[int]string, slots)
	keys := []string{"/workspace/aaa", "/workspace/bbb", "/workspace/ccc", "/workspace/ddd"}
	for _, key := range keys {
		first := SlotFor(key, slots)
		second := SlotFor(key, slots)
		if first != second {
			t.Fatalf("SlotFor(%q) not stable: %d then %d", key, first, second)
		}
		if first < 0 || first >= slots {
			t.Fatalf("SlotFor(%q) = %d out of range", key, first)
		}
		if other, taken := seen[first]; taken {
			t.Fatalf("slots collided: %q and %q both got %d", other, key, first)
		}
		seen[first] = key
	}
	if SlotFor("", slots) != UnassignedSlot {
		t.Fatalf("empty workspace key must be unassigned")
	}
	if SlotFor("/workspace/aaa", 0) != UnassignedSlot {
		t.Fatalf("zero slot count must be unassigned")
	}
}

func TestResolveWarnsWithoutWorkspaceKey(t *testing.T) {
	config := map[string]interface{}{ConfigSharedMode: "shared"}
	policy := ResolveWithLookup(config, "  ", noEnv)

	if policy.Slot != UnassignedSlot {
		t.Fatalf("slot = %d, want %d", policy.Slot, UnassignedSlot)
	}
	if len(policy.Warnings) == 0 {
		t.Fatalf("expected a warning for an unkeyed shared workspace")
	}
}

func TestResolvePrecedenceEnvBeatsAgentConfig(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode:       "shared",
		ConfigNetworkMode:      ModeSharedPorts,
		ConfigPortBase:         40000,
		ConfigPortStride:       10,
		ConfigMaxAgentsPerPool: 2,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", envOf(map[string]string{
		EnvNetworkModeLever: ModeLoopbackAlias,
		EnvPortBaseLever:    "55000",
		EnvPortStrideLever:  "5",
		EnvSlotCountLever:   "3",
	}))

	if policy.Mode != ModeLoopbackAlias {
		t.Fatalf("mode = %q, want env override %q", policy.Mode, ModeLoopbackAlias)
	}
	if policy.SlotCount != 3 {
		t.Fatalf("slot count = %d, want env override 3", policy.SlotCount)
	}
	// Loopback alias zeroes the slice, so switch back to shared_ports to check
	// that the env port levers win over the agent config ones.
	ports := ResolveWithLookup(config, "/workspace/abc", envOf(map[string]string{
		EnvPortBaseLever:   "55000",
		EnvPortStrideLever: "5",
	}))
	if ports.PortBase != 55000 || ports.PortStride != 5 {
		t.Fatalf("port base/stride = %d/%d, want env override 55000/5", ports.PortBase, ports.PortStride)
	}
	if ports.PortMax-ports.PortMin+1 != 5 {
		t.Fatalf("slice width = %d, want 5", ports.PortMax-ports.PortMin+1)
	}
}

func TestResolveAgentPortsAndSlotCount(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode:          "shared",
		ConfigPortBase:            21000,
		ConfigPortStride:          50,
		ConfigMaxSharedWorkspaces: 6,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)

	if policy.PortBase != 21000 || policy.PortStride != 50 || policy.SlotCount != 6 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	windowEnd := policy.PortBase + policy.SlotCount*policy.PortStride
	if policy.PortMin < policy.PortBase || policy.PortMax >= windowEnd {
		t.Fatalf("slot slice %d..%d outside %d..%d", policy.PortMin, policy.PortMax, policy.PortBase, windowEnd-1)
	}
}

func TestResolveClampsAndWarns(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode:          "shared",
		ConfigPortBase:            80,
		ConfigMaxSharedWorkspaces: 400,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)

	if policy.SlotCount != MaxSlotCount {
		t.Fatalf("slot count = %d, want clamp to %d", policy.SlotCount, MaxSlotCount)
	}
	if policy.PortBase != MinPort {
		t.Fatalf("port base = %d, want clamp to %d", policy.PortBase, MinPort)
	}
	if len(policy.Warnings) < 2 {
		t.Fatalf("expected clamp warnings, got %v", policy.Warnings)
	}
}

func TestNormalizeMode(t *testing.T) {
	cases := map[string]string{
		"isolated":       ModeIsolated,
		"PRIVATE":        ModeIsolated,
		" own-netns ":    ModeIsolated,
		"loopback":       ModeLoopbackAlias,
		"loopback_alias": ModeLoopbackAlias,
		"ports":          ModeSharedPorts,
		"shared_ports":   ModeSharedPorts,
	}
	for raw, want := range cases {
		got, ok := NormalizeMode(raw)
		if !ok || got != want {
			t.Fatalf("NormalizeMode(%q) = %q,%v want %q,true", raw, got, ok, want)
		}
	}
	if _, ok := NormalizeMode("typo"); ok {
		t.Fatalf("an unknown mode must not be accepted as isolation")
	}
}

func TestPooledSandboxRequestedModes(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"shared", true},
		{"pooled", true},
		{"shared-pod", true},
		{"dedicated", false},
		{"per_chat", false},
	}
	for _, tc := range cases {
		config := map[string]interface{}{ConfigSharedMode: tc.value}
		if got, _ := pooledSandboxRequested(config, noEnv); got != tc.want {
			t.Fatalf("pooledSandboxRequested(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
	if got, _ := pooledSandboxRequested(nil, noEnv); got {
		t.Fatalf("no config must not request a pooled sandbox")
	}
	config := map[string]interface{}{ConfigSharedMode: true}
	if got, _ := pooledSandboxRequested(config, noEnv); !got {
		t.Fatalf("boolean shared mode must request a pooled sandbox")
	}
}

func TestWindowsDoNotOverlap(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode:       "shared",
		ConfigPortBase:         31000,
		ConfigPortStride:       100,
		ConfigMaxAgentsPerPool: 5,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)
	windows := Windows(policy)
	if len(windows) != 5 {
		t.Fatalf("got %d windows, want 5: %v", len(windows), windows)
	}

	slotPorts := make(map[int][2]int, policy.SlotCount)
	for slot := 0; slot < policy.SlotCount; slot++ {
		low := policy.PortBase + slot*policy.PortStride
		slotPorts[slot] = [2]int{low, low + policy.PortStride - 1}
	}
	if conflicts := Overlaps(slotPorts); len(conflicts) != 0 {
		t.Fatalf("resolved policy overlaps: %v", conflicts)
	}
	// The overlap checker must actually detect an overlap.
	slotPorts[0] = [2]int{31000, 31150}
	if conflicts := Overlaps(slotPorts); len(conflicts) == 0 {
		t.Fatalf("Overlaps missed an obvious overlap")
	}
}

func TestSharedNetnsPermitted(t *testing.T) {
	allowed, reason := SharedNetnsPermitted(nil, "msgmate-io/open-chat-go-ci")
	if !allowed || reason != "" {
		t.Fatalf("plain repo slug should be permitted, reason=%q", reason)
	}
	if allowed, _ := SharedNetnsPermitted(nil, ""); allowed {
		t.Fatalf("an unknown repository scope must not be permitted")
	}
	if allowed, _ := SharedNetnsPermitted(nil, "git@github.com:owner/repo.git"); allowed {
		t.Fatalf("a raw remote must not be permitted")
	}
	if allowed, reason := SharedNetnsPermitted(map[string]interface{}{
		ConfigRequireIsolation: true,
	}, "msgmate-io/open-chat-go-ci"); allowed || reason == "" {
		t.Fatalf("required isolation must deny with a reason, got %v %q", allowed, reason)
	}
	if allowed, _ := SharedNetnsPermitted(map[string]interface{}{
		ConfigIsolatedRepos: []interface{}{"msgmate-io/secret-repo"},
	}, "msgmate-io/secret-repo"); allowed {
		t.Fatalf("a listed repository must be denied")
	}
	if allowed, _ := SharedNetnsPermitted(map[string]interface{}{
		ConfigIsolatedRepos: "msgmate-io/secret-repo, msgmate-io/other",
	}, "msgmate-io/other"); allowed {
		t.Fatalf("a comma-separated isolated repo list must be honoured")
	}
	if allowed, _ := SharedNetnsPermitted(map[string]interface{}{
		ConfigIsolatedRepos: []interface{}{"msgmate-io/external"},
	}, "msgmate-io/open-chat-go-ci"); !allowed {
		t.Fatalf("an unlisted repository must still be permitted")
	}
}

func TestEnvForDoesNotOverwriteExplicitValues(t *testing.T) {
	config := map[string]interface{}{ConfigSharedMode: "shared"}
	policy := ResolveWithLookup(config, "/workspace/abc", noEnv)

	merged := policy.EnvFor(map[string]string{EnvLoopback: "127.0.0.9", "OTHER": "kept"})
	if merged[EnvLoopback] != "127.0.0.9" {
		t.Fatalf("explicit env value overwritten: %q", merged[EnvLoopback])
	}
	if merged["OTHER"] != "kept" || merged[EnvNetworkMode] != policy.Mode {
		t.Fatalf("unexpected merged env: %v", merged)
	}
	if got := policy.EnvFor(nil); got[EnvNetns] != "shared" {
		t.Fatalf("nil base must still yield the policy env: %v", got)
	}
}

func TestIsolatedForcedByLever(t *testing.T) {
	config := map[string]interface{}{
		ConfigSharedMode: "shared",
		ConfigPortBase:   31000,
	}
	policy := ResolveWithLookup(config, "/workspace/abc", envOf(map[string]string{
		EnvNetworkModeLever: ModeIsolated,
	}))
	if !policy.Isolated() || policy.PortMin != 0 {
		t.Fatalf("explicit isolated lever must win: %+v", policy)
	}
}

func TestStringAndReportNeverLeakSecrets(t *testing.T) {
	policy := ResolveWithLookup(map[string]interface{}{ConfigSharedMode: "shared"}, "/workspace/abc", noEnv)
	line := policy.String()
	if line == "" {
		t.Fatalf("empty policy line")
	}
	lines := policy.ReportLines()
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 report lines, got %d", len(lines))
	}
	for _, l := range lines {
		if l == "" {
			t.Fatalf("empty report line in %v", lines)
		}
	}
}
