package sandboxpool

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
)

func testAllocator(t *testing.T, policy NetworkPolicy) *PortAllocator {
	t.Helper()
	allocator, err := NewPortAllocator(policy)
	if err != nil {
		t.Fatalf("NewPortAllocator: %v", err)
	}
	return allocator
}

func TestDefaultNetworkPolicyIsBoundedShared(t *testing.T) {
	policy := DefaultNetworkPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("default policy invalid: %v", err)
	}
	if policy.Mode != NetworkModeShared {
		t.Fatalf("expected shared default, got %q", policy.Mode)
	}
	if policy.MaxWorkspaces != DefaultNetworkMaxWorkspaces {
		t.Fatalf("expected %d workspaces, got %d", DefaultNetworkMaxWorkspaces, policy.MaxWorkspaces)
	}
	if policy.PortBase < MinAllocatablePort {
		t.Fatalf("port base %d is privileged", policy.PortBase)
	}
}

func TestNetworkPolicyFromEnvOverridesAndFailsClosedOnBadMode(t *testing.T) {
	t.Setenv(EnvNetworkPortBase, "20000")
	t.Setenv(EnvNetworkPortBlockSize, "4")
	t.Setenv(EnvNetworkMaxWorkspaces, "3")
	t.Setenv(EnvNetworkLoopbackAliases, "off")
	policy := NetworkPolicyFromEnv()
	if policy.PortBase != 20000 || policy.PortBlockSize != 4 || policy.MaxWorkspaces != 3 {
		t.Fatalf("env not applied: %+v", policy)
	}
	if policy.LoopbackAliases {
		t.Fatal("expected aliases disabled")
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid env policy rejected: %v", err)
	}

	// A mis-typed mode must fail closed, never silently downgrade to shared.
	t.Setenv(EnvNetworkMode, "bogus")
	if err := NetworkPolicyFromEnv().Validate(); !errors.Is(err, ErrInvalidNetworkPolicy) {
		t.Fatalf("expected ErrInvalidNetworkPolicy for unknown mode, got %v", err)
	}
}

func TestNetworkPolicyValidateRejectsUnusableValues(t *testing.T) {
	cases := map[string]NetworkPolicy{
		"privileged base":   {Mode: NetworkModeShared, PortBase: 80, PortBlockSize: 10, MaxWorkspaces: 4, LoopbackPrefix: "127.64"},
		"tiny block":        {Mode: NetworkModeShared, PortBase: 12000, PortBlockSize: 1, MaxWorkspaces: 4, LoopbackPrefix: "127.64"},
		"no workspaces":     {Mode: NetworkModeShared, PortBase: 12000, PortBlockSize: 10, MaxWorkspaces: 0},
		"bad alias prefix":  {Mode: NetworkModeShared, PortBase: 12000, PortBlockSize: 10, MaxWorkspaces: 4, LoopbackAliases: true, LoopbackPrefix: "127.64.1"},
		"netns unavailable": {Mode: NetworkModeNetns, PortBase: 12000, PortBlockSize: 10, MaxWorkspaces: 4},
	}
	for name, policy := range cases {
		if err := policy.Validate(); !errors.Is(err, ErrInvalidNetworkPolicy) {
			t.Errorf("%s: expected ErrInvalidNetworkPolicy, got %v", name, err)
		}
	}
}

func TestAllocateGivesDisjointBlocksAndIdempotentPlans(t *testing.T) {
	policy := DefaultNetworkPolicy()
	policy.MaxWorkspaces = 4
	allocator := testAllocator(t, policy)

	first, err := allocator.Allocate("chat-a", "msgmate-io/open-chat-go", "user")
	if err != nil {
		t.Fatalf("allocate chat-a: %v", err)
	}
	again, err := allocator.Allocate("chat-a", "msgmate-io/open-chat-go", "user")
	if err != nil {
		t.Fatalf("re-allocate chat-a: %v", err)
	}
	if first.PortStart != again.PortStart || first.PortEnd != again.PortEnd ||
		first.BlockIndex != again.BlockIndex || first.LoopbackHost != again.LoopbackHost ||
		first.Env["PORT"] != again.Env["PORT"] {
		t.Fatalf("allocation is not idempotent: %+v vs %+v", first, again)
	}
	if first.PortEnd-first.PortStart != policy.PortBlockSize-1 {
		t.Fatalf("block size wrong: %+v", first)
	}
	if first.Env["PORT"] != strconv.Itoa(first.PortStart) || first.Env["CODER_LOCALHOST"] != first.LoopbackHost {
		t.Fatalf("env not injected: %+v", first.Env)
	}
	if first.Env["CODER_PORT_BLOCK_END"] != strconv.Itoa(first.PortEnd) {
		t.Fatalf("block end not injected: %+v", first.Env)
	}
	if net.ParseIP(first.LoopbackHost) == nil || !strings.HasPrefix(first.LoopbackHost, policy.LoopbackPrefix+".") {
		t.Fatalf("loopback alias %q is not a %s.* address", first.LoopbackHost, policy.LoopbackPrefix)
	}

	overlap := func(a, b WorkspaceNetworkPlan) bool { return a.PortStart <= b.PortEnd && b.PortStart <= a.PortEnd }
	seen := []WorkspaceNetworkPlan{first}
	hosts := map[string]bool{first.LoopbackHost: true}
	for _, uuid := range []string{"chat-b", "chat-c", "chat-d"} {
		plan, err := allocator.Allocate(uuid, "msgmate-io/open-chat-go", "user")
		if err != nil {
			t.Fatalf("allocate %s: %v", uuid, err)
		}
		for _, other := range seen {
			if overlap(plan, other) {
				t.Fatalf("blocks overlap: %+v vs %+v", plan, other)
			}
		}
		if hosts[plan.LoopbackHost] {
			t.Fatalf("loopback alias %q reused", plan.LoopbackHost)
		}
		hosts[plan.LoopbackHost] = true
		seen = append(seen, plan)
	}

	// The workspace bound, not the port range, is the ceiling: the 5th workspace
	// is refused instead of sharing a block.
	if _, err := allocator.Allocate("chat-e", "msgmate-io/open-chat-go", "user"); !errors.Is(err, ErrPoolNetworkCapacity) {
		t.Fatalf("expected ErrPoolNetworkCapacity, got %v", err)
	}
	if !allocator.Release("chat-e") {
		if allocator.Release("chat-e") {
			t.Fatal("release of a never-allocated workspace reported success")
		}
	}
	if !allocator.Release("chat-b") {
		t.Fatal("release of an allocated workspace failed")
	}
	if _, err := allocator.Allocate("chat-e", "msgmate-io/open-chat-go", "user"); err != nil {
		t.Fatalf("allocate after release: %v", err)
	}
	if got := len(allocator.ActiveWorkspaces()); got != 4 {
		t.Fatalf("expected 4 active workspaces, got %d", got)
	}
}

func TestAllocateSkipsReservedAndPrivilegedBlocks(t *testing.T) {
	policy := NetworkPolicy{
		Mode: NetworkModeShared, LoopbackAliases: false, PortBase: 12000,
		PortBlockSize: 2, MaxWorkspaces: 6, ReservedPorts: []int{12000, 12002},
	}
	allocator := testAllocator(t, policy)
	for _, uuid := range []string{"w1", "w2", "w3", "w4"} {
		plan, err := allocator.Allocate(uuid, "", "")
		if err != nil {
			t.Fatalf("allocate %s: %v", uuid, err)
		}
		for _, reserved := range policy.ReservedPorts {
			if reserved >= plan.PortStart && reserved <= plan.PortEnd {
				t.Fatalf("plan %+v covers reserved port %d", plan, reserved)
			}
		}
	}
}

func TestAllocateRefusesSensitiveScopeAndUnavailableNetns(t *testing.T) {
	shared := testAllocator(t, DefaultNetworkPolicy())
	if _, err := shared.Allocate("sensitive-chat", "msgmate-io/open-chat-go", "sensitive"); !errors.Is(err, ErrSharedNetnsDenied) {
		t.Fatalf("expected ErrSharedNetnsDenied for sensitive scope, got %v", err)
	}

	policy := DefaultNetworkPolicy()
	policy.IsolationRepos = []string{"acme/*"}
	isolating := testAllocator(t, policy)
	if _, err := isolating.Allocate("private-chat", "acme/secret", "user"); err == nil {
		t.Fatal("expected an isolation error for an isolated repo")
	} else if !errors.Is(err, ErrSharedNetnsDenied) {
		t.Fatalf("expected ErrSharedNetnsDenied, got %v", err)
	}
	if _, err := shared.Allocate("public-chat", "msgmate-io/open-chat-go", "user"); err != nil {
		t.Fatalf("public repo must be admitted in shared mode: %v", err)
	}

	netns := DefaultNetworkPolicy()
	netns.Mode = NetworkModeNetns
	if _, err := NewPortAllocator(netns); !errors.Is(err, ErrInvalidNetworkPolicy) {
		t.Fatalf("expected a netns policy without pool support to be rejected, got %v", err)
	}
	netns.NetnsAvailable = true
	allocator := testAllocator(t, netns)
	if _, err := allocator.Allocate("private-chat", "acme/secret", "user"); err != nil {
		t.Fatalf("isolated repo must be admitted in netns mode: %v", err)
	}
}

func TestPlanReportsAndServiceAddressCarryNoSecrets(t *testing.T) {
	allocator := testAllocator(t, DefaultNetworkPolicy())
	plan, err := allocator.Allocate("chat-report", "msgmate-io/open-chat-go", "user")
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	lines := append(plan.ReportLines(), allocator.ReportLines()...)
	lines = append(lines, plan.Describe(), WorkspaceServiceAddress("pool-0.internal", plan))
	for _, mode := range NetworkFailureModes(allocator.Policy()) {
		lines = append(lines, mode)
	}
	joined := strings.Join(lines, "\n")
	for _, marker := range []string{"token", "password", "kubeconfig", "BAH", plan.WorkspaceUUID[:4] + "secret"} {
		if strings.Contains(strings.ToLower(joined), strings.ToLower(marker)) {
			t.Fatalf("report contains %q:\n%s", marker, joined)
		}
	}
	if !strings.Contains(joined, "sandbox_network") || !strings.Contains(joined, "ports=") {
		t.Fatalf("report is not self-describing:\n%s", joined)
	}
	if !strings.Contains(joined, "pool-0.internal") {
		t.Fatalf("service address must name the pool host:\n%s", joined)
	}
	if !strings.Contains(joined, plan.Env["PORT"]) {
		t.Fatalf("service address must use the workspace port:\n%s", joined)
	}
	if !IsPoolNetworkFailure(ErrPoolNetworkCapacity) || IsPoolNetworkFailure(errors.New("other")) {
		t.Fatal("IsPoolNetworkFailure must only match policy errors")
	}
}

func TestNormalizeWorkspaceUUIDRejectsUnsafeValues(t *testing.T) {
	for _, raw := range []string{"", "   ", "with space", "line\nbreak", strings.Repeat("a", 129)} {
		if _, err := NormalizeWorkspaceUUID(raw); !errors.Is(err, ErrInvalidWorkspaceUUID) {
			t.Errorf("expected ErrInvalidWorkspaceUUID for %q", raw)
		}
	}
	uuid, err := NormalizeWorkspaceUUID(" 6b70f615-189a-48a2 ")
	if err != nil || uuid != "6b70f615-189a-48a2" {
		t.Fatalf("unexpected normalisation: %q %v", uuid, err)
	}
}
