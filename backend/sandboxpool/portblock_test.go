package sandboxpool

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// testPublishedPolicy is a small, explicit geometry so the published span is
// easy to assert: 5 blocks of 4 ports from 12100.
func testPublishedPolicy() NetworkPolicy {
	return NetworkPolicy{
		Mode:            NetworkModeShared,
		LoopbackAliases: true,
		PortBase:        12100,
		PortBlockSize:   4,
		MaxWorkspaces:   5,
		LoopbackPrefix:  DefaultNetworkLoopbackPrefix,
		ReservedPorts:   append([]int(nil), DefaultReservedPorts...),
	}
}

func TestResolvePoolPortBlockDefaultPolicy(t *testing.T) {
	block, ok := ResolvePoolPortBlock(DefaultNetworkPolicy())
	if !ok {
		t.Fatal("default policy must publish a port block")
	}
	// 8 workspaces of 10 ports from 12000: the whole-pool span.
	if block.Base != DefaultNetworkPortBase || block.Last != 12079 {
		t.Errorf("span = %d-%d, want %d-12079", block.Base, block.Last, DefaultNetworkPortBase)
	}
	if block.BlockSize != DefaultNetworkPortBlockSize || block.Slots != DefaultNetworkMaxWorkspaces {
		t.Errorf("geometry = size %d slots %d", block.BlockSize, block.Slots)
	}
	if block.Mode != NetworkModeShared || !block.LoopbackAliases {
		t.Errorf("mode/aliases = %q/%v", block.Mode, block.LoopbackAliases)
	}
	if !block.Valid() || block.Size() != 80 {
		t.Errorf("valid/size = %v/%d", block.Valid(), block.Size())
	}
	if !block.Contains(12000) || !block.Contains(12079) || block.Contains(12080) {
		t.Errorf("Contains is not the inclusive span: %s", block.String())
	}
}

func TestResolvePoolPortBlockCustomGeometry(t *testing.T) {
	policy := testPublishedPolicy()
	block, ok := ResolvePoolPortBlock(policy)
	if !ok {
		t.Fatal("custom policy must publish a port block")
	}
	if block.Base != 12100 || block.Last != 12119 || block.Slots != 5 || block.BlockSize != 4 {
		t.Fatalf("span = %+v", block)
	}
	// The published span must agree with the admin plan geometry.
	if got := portGeometry(policy, block.Slots); got != "[12100+4*5 => 12119]" {
		t.Errorf("PortGeometry = %q", got)
	}
	// Every block of the span is the block the allocator hands out.
	for slot := 0; slot < block.Slots; slot++ {
		start := block.Base + slot*block.BlockSize
		end := start + block.BlockSize - 1
		if !block.Contains(start) || !block.Contains(end) {
			t.Errorf("slot %d (%d-%d) is outside the published span %s", slot, start, end, block.String())
		}
	}
}

func TestResolvePoolPortBlockBoundaryAndOverflow(t *testing.T) {
	// A base at the ceiling with a block size that leaves no room: nothing to
	// publish, and no panic.
	empty := testPublishedPolicy()
	empty.PortBase = MaxAllocatablePort
	empty.PortBlockSize = MinNetworkPortBlockSize
	if block, ok := ResolvePoolPortBlock(empty); ok {
		t.Errorf("a policy with no allocatable block published %+v", block)
	}

	// The slot arithmetic is clamped to the allocatable ceiling, so a hand-picked
	// slot count can never publish a port above MaxAllocatablePort.
	clamped := testPublishedPolicy()
	clamped.PortBase = 65500
	clamped.PortBlockSize = 10
	block, ok := resolvePoolPortBlock(clamped, 100)
	if !ok {
		t.Fatal("clamped geometry must still resolve")
	}
	if block.Last != MaxAllocatablePort {
		t.Errorf("Last = %d, want the %d ceiling", block.Last, MaxAllocatablePort)
	}

	// An invalid/zero policy yields the zero value and ok=false.
	if block, ok := ResolvePoolPortBlock(NetworkPolicy{}); ok || block.Valid() {
		t.Errorf("zero policy published %+v (ok=%v)", block, ok)
	}
	if block, ok := resolvePoolPortBlock(testPublishedPolicy(), 0); ok || block.Valid() {
		t.Errorf("zero slots published %+v (ok=%v)", block, ok)
	}
}

func TestPoolPortBlockEnvAndPublishLines(t *testing.T) {
	block, ok := ResolvePoolPortBlock(testPublishedPolicy())
	if !ok {
		t.Fatal("resolve failed")
	}

	env := block.Env()
	want := map[string]string{
		EnvPoolPortBlockStart: "12100",
		EnvPoolPortBlockEnd:   "12119",
		EnvPoolPortBlockSize:  "4",
		EnvPoolPortSlots:      "5",
	}
	for name, value := range want {
		if env[name] != value {
			t.Errorf("env[%s] = %q, want %q", name, env[name], value)
		}
	}

	lines := block.PublishLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "sandbox_pool_transport published=true") {
		t.Fatalf("publish lines are not self-describing:\n%s", joined)
	}
	if !strings.Contains(joined, "service_ports=12100-12119") {
		t.Errorf("publish lines omit the service span:\n%s", joined)
	}
	// The env contract line names every lever a consumer reads, sorted.
	if !strings.Contains(joined, "env="+strings.Join([]string{
		EnvPoolPortBlockEnd, EnvPoolPortBlockSize, EnvPoolPortBlockStart, EnvPoolPortSlots,
	}, ",")) {
		t.Errorf("publish lines omit the env contract:\n%s", joined)
	}
	if !strings.Contains(joined, "health_check=tcp-connect pool:12100") {
		t.Errorf("publish lines omit the health check:\n%s", joined)
	}
	// One line per block of the span, with the per-workspace loopback alias.
	if got := len(lines); got != 3+block.Slots {
		t.Errorf("publish lines = %d, want %d", got, 3+block.Slots)
	}
	if !strings.Contains(joined, "block=0 ports=12100-12103 loopback=127.64.1.1") {
		t.Errorf("first block line is wrong:\n%s", joined)
	}
	if !strings.Contains(joined, "block=4 ports=12116-12119 loopback=127.64.1.5") {
		t.Errorf("last block line is wrong:\n%s", joined)
	}

	// An unresolved block publishes an explicit "nothing published" line and no
	// env contract, instead of a half-filled one.
	unpublished := PoolPortBlock{}
	if unpublished.Env() != nil {
		t.Error("an unresolved block must not carry an env contract")
	}
	if got := strings.Join(unpublished.PublishLines(), "\n"); !strings.Contains(got, "published=false") {
		t.Errorf("unresolved publish lines = %q", got)
	}
	if unpublished.String() != "unpublished" || unpublished.HealthCheck("pool") != "unpublished" {
		t.Errorf("unresolved block renders %q/%q", unpublished.String(), unpublished.HealthCheck("pool"))
	}
}

func TestPublishedPortBlockMatchesAdminPlan(t *testing.T) {
	policy := testPublishedPolicy()
	alloc := testAllocator(t, policy)

	published, ok := alloc.PublishedPortBlock()
	if !ok {
		t.Fatal("allocator must publish a port block")
	}
	fromPolicy, ok := ResolvePoolPortBlock(policy)
	if !ok || published != fromPolicy {
		t.Fatalf("allocator span %+v != policy span %+v", published, fromPolicy)
	}

	// The acceptance criterion of ci#846: the published block equals the block
	// the admin network plan reports, and every live workspace block is inside it.
	status := alloc.Status()
	if status.PublishedBlock == nil || *status.PublishedBlock != published {
		t.Fatalf("admin plan published block = %+v, want %+v", status.PublishedBlock, published)
	}
	if status.PortGeometry != "["+strconv.Itoa(published.Base)+"+"+strconv.Itoa(published.BlockSize)+"*"+strconv.Itoa(published.Slots)+" => "+strconv.Itoa(published.Last)+"]" {
		t.Errorf("PortGeometry %q disagrees with the published span %s", status.PortGeometry, published.String())
	}

	plan, err := alloc.Allocate("11111111-1111-1111-1111-111111111111", "", "")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if !published.Contains(plan.PortStart) || !published.Contains(plan.PortEnd) {
		t.Errorf("workspace block %d-%d is outside the published span %s", plan.PortStart, plan.PortEnd, published.String())
	}

	// The view is read-only: publishing must not consume or free a block.
	status = alloc.Status()
	if status.ActiveWorkspaces != 1 || status.FreeSlots != policy.MaxWorkspaces-1 {
		t.Errorf("publishing changed allocator state: active=%d free=%d", status.ActiveWorkspaces, status.FreeSlots)
	}
	if status.PublishedBlock == nil || *status.PublishedBlock != published {
		t.Errorf("published block changed after an allocation: %+v", status.PublishedBlock)
	}
}

func TestNetworkPlanStatusFromEnvPublishesBlock(t *testing.T) {
	t.Setenv(EnvNetworkMode, "")
	t.Setenv(EnvNetworkPortBase, "12100")
	t.Setenv(EnvNetworkPortBlockSize, "4")
	t.Setenv(EnvNetworkMaxWorkspaces, "5")

	status := NetworkPlanStatusFromEnv(time.Now())
	if status.PolicyError != "" {
		t.Fatalf("PolicyError = %q", status.PolicyError)
	}
	if status.PublishedBlock == nil {
		t.Fatal("env-resolved plan published no block")
	}
	if status.PublishedBlock.Base != 12100 || status.PublishedBlock.Last != 12119 {
		t.Errorf("published span = %+v", status.PublishedBlock)
	}

	// An invalid deployment lever publishes nothing rather than a zero block.
	t.Setenv(EnvNetworkMode, "carrier-pigeon")
	invalid := NetworkPlanStatusFromEnv(time.Now())
	if invalid.PolicyError == "" {
		t.Fatal("invalid mode reported no PolicyError")
	}
	if invalid.PublishedBlock != nil {
		t.Errorf("invalid policy published %+v", invalid.PublishedBlock)
	}
}
