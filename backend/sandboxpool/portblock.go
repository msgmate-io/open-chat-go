package sandboxpool

// ci#792 item 1 (ci#846) - publish the resolved pool port block on the transport.
//
// The ci#546 policy allocates a disjoint per-workspace port block inside the pool
// pod, and the admin surface (netplanstatus.go) renders the resolved plan. What
// was missing is the *publish* half: the pool's SSH/target binding never exposed
// the block, so a sandbox's declared port range was not actually reachable and
// could not be verified from outside. This file is the read-only, reporting-safe
// contract that closes that gap:
//
//   - PoolPortBlock is the whole-pool span the pool publishes on its transport
//     (the pool Service / SSH remote-forward rule a consumer reads): the union of
//     every per-workspace block the allocator can hand out, so a consumer never
//     has to guess a port;
//   - ResolvePoolPortBlock / (*PortAllocator).PublishedPortBlock resolve that span
//     from a policy (or the live allocator) with the exact slot arithmetic the
//     allocator enforces, so the published block always equals the block the
//     admin network plan reports;
//   - PoolPortBlock.PublishLines / Env / HealthCheck render the transport
//     declaration, the env contract and the reachability probe.
//
// It adds no cluster mutation path: it reuses the existing pool configuration and
// carries no credential material (ports, aliases and env names only).

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Published-block env contract. These are the pool-level names a consumer reads
// to learn the span the pool publishes; they are deliberately distinct from the
// per-workspace CODER_PORT_BLOCK_START/END the session env injects.
const (
	// EnvPoolPortBlockStart is the first port of the published pool span.
	EnvPoolPortBlockStart = "CODER_POOL_PORT_BLOCK_START"
	// EnvPoolPortBlockEnd is the last (inclusive) port of the published pool span.
	EnvPoolPortBlockEnd = "CODER_POOL_PORT_BLOCK_END"
	// EnvPoolPortBlockSize is how many ports one workspace owns.
	EnvPoolPortBlockSize = "CODER_POOL_PORT_BLOCK_SIZE"
	// EnvPoolPortSlots is how many workspace blocks the span covers.
	EnvPoolPortSlots = "CODER_POOL_PORT_SLOTS"
)

// PoolPortBlock is the resolved, whole-pool port span the pool publishes on its
// transport: the pool Service / SSH remote-forward rule a consumer (a chat-lead,
// an operator, a health check) reads to reach a workspace dev server. It is the
// union of every per-workspace block the allocator can hand out, so the block the
// admin network plan reports is the block that is reachable.
type PoolPortBlock struct {
	// Mode is the isolation mode the block was resolved under.
	Mode string `json:"mode"`
	// Base is the first port of the first block.
	Base int `json:"base"`
	// Last is the last (inclusive) port of the last block.
	Last int `json:"last"`
	// BlockSize is how many ports one workspace owns.
	BlockSize int `json:"block_size"`
	// Slots is how many workspace blocks the span covers.
	Slots int `json:"slots"`
	// LoopbackPrefix is the two-octet prefix of the per-workspace aliases.
	LoopbackPrefix string `json:"loopback_prefix,omitempty"`
	// LoopbackAliases reports whether per-workspace aliases are in use.
	LoopbackAliases bool `json:"loopback_aliases"`
}

// Valid reports whether the block is a usable, ordered span.
func (b PoolPortBlock) Valid() bool {
	return b.Base > 0 && b.Last >= b.Base && b.BlockSize > 0 && b.Slots > 0
}

// Size is the number of ports in the published span.
func (b PoolPortBlock) Size() int {
	if !b.Valid() {
		return 0
	}
	return b.Last - b.Base + 1
}

// Contains reports whether port is inside the published span.
func (b PoolPortBlock) Contains(port int) bool {
	return b.Valid() && port >= b.Base && port <= b.Last
}

// String renders the span without any workspace-identifying data.
func (b PoolPortBlock) String() string {
	if !b.Valid() {
		return "unpublished"
	}
	return strconv.Itoa(b.Base) + "-" + strconv.Itoa(b.Last)
}

// LoopbackFor renders the loopback host of one block of the span: the
// per-workspace alias when aliases are on, otherwise the shared 127.0.0.1. It
// never panics on an out-of-range slot or an unusable prefix.
func (b PoolPortBlock) LoopbackFor(slot int) string {
	if !b.LoopbackAliases || slot < 0 {
		return "127.0.0.1"
	}
	alias, err := aliasPrefix(b.LoopbackPrefix, slot)
	if err != nil {
		return "127.0.0.1"
	}
	return alias
}

// Env returns the published-block env contract, or nil for an unresolved block.
// Values are ports and counts only, never a secret.
func (b PoolPortBlock) Env() map[string]string {
	if !b.Valid() {
		return nil
	}
	return map[string]string{
		EnvPoolPortBlockStart: strconv.Itoa(b.Base),
		EnvPoolPortBlockEnd:   strconv.Itoa(b.Last),
		EnvPoolPortBlockSize:  strconv.Itoa(b.BlockSize),
		EnvPoolPortSlots:      strconv.Itoa(b.Slots),
	}
}

// HealthCheck renders the read-only probe a consumer runs to verify the published
// block is reachable on the pool host: a TCP connect to the first port of the
// span. It never mutates the pool and carries no secret.
func (b PoolPortBlock) HealthCheck(poolHost string) string {
	if !b.Valid() {
		return "unpublished"
	}
	host := strings.TrimSpace(poolHost)
	if host == "" {
		host = "pool"
	}
	return fmt.Sprintf("tcp-connect %s (first port %d of published block %s)",
		net.JoinHostPort(host, strconv.Itoa(b.Base)), b.Base, b.String())
}

// PublishLines renders the transport declaration of the resolved block: the pool
// Service port range, the env contract, the health check and the per-workspace
// block mapping. It is the "publish" half of ci#792 item 1 and carries no secret:
// ports, aliases and env names only.
func (b PoolPortBlock) PublishLines() []string {
	if !b.Valid() {
		return []string{"sandbox_pool_transport published=false reason=no-resolved-port-block"}
	}
	lines := []string{
		fmt.Sprintf("sandbox_pool_transport published=true mode=%s service_ports=%s block_size=%d slots=%d loopback_aliases=%t",
			b.Mode, b.String(), b.BlockSize, b.Slots, b.LoopbackAliases),
		fmt.Sprintf("sandbox_pool_transport env=%s", b.envList()),
		fmt.Sprintf("sandbox_pool_transport health_check=%s", b.HealthCheck("")),
	}
	for slot := 0; slot < b.Slots; slot++ {
		start := b.Base + slot*b.BlockSize
		end := start + b.BlockSize - 1
		lines = append(lines, fmt.Sprintf("sandbox_pool_transport block=%d ports=%d-%d loopback=%s",
			slot, start, end, b.LoopbackFor(slot)))
	}
	return lines
}

// envList renders the sorted env names of the contract, so the publish line is
// stable.
func (b PoolPortBlock) envList() string {
	env := b.Env()
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// policySlots is the number of allocatable blocks of a policy: the port range
// divided by the block size, capped by the workspace bound. It is the single
// source of the slot count, shared by the allocator and the published block, so
// the two can never disagree.
func policySlots(policy NetworkPolicy) int {
	slots := (MaxAllocatablePort - policy.PortBase + 1) / policy.PortBlockSize
	if slots > policy.MaxWorkspaces {
		slots = policy.MaxWorkspaces
	}
	return slots
}

// resolvePoolPortBlock builds the published span of a policy for a known slot
// count. It is pure and never panics: an unusable geometry yields ok=false.
func resolvePoolPortBlock(policy NetworkPolicy, slots int) (PoolPortBlock, bool) {
	if slots <= 0 || policy.PortBase <= 0 || policy.PortBlockSize <= 0 {
		return PoolPortBlock{}, false
	}
	last := policy.PortBase + slots*policy.PortBlockSize - 1
	if last > MaxAllocatablePort {
		last = MaxAllocatablePort
	}
	if last < policy.PortBase {
		return PoolPortBlock{}, false
	}
	return PoolPortBlock{
		Mode:            policy.Mode,
		Base:            policy.PortBase,
		Last:            last,
		BlockSize:       policy.PortBlockSize,
		Slots:           slots,
		LoopbackPrefix:  policy.LoopbackPrefix,
		LoopbackAliases: policy.LoopbackAliases,
	}, true
}

// ResolvePoolPortBlock resolves the whole-pool port span a policy publishes on
// the transport. It is pure and deterministic and never panics: an invalid or
// zero policy yields the zero value and ok=false, so a caller reports "nothing
// published" instead of a half-filled contract.
func ResolvePoolPortBlock(policy NetworkPolicy) (PoolPortBlock, bool) {
	if err := policy.Validate(); err != nil {
		return PoolPortBlock{}, false
	}
	return resolvePoolPortBlock(policy, policySlots(policy))
}

// PublishedPortBlock returns the span the live allocator publishes: the same
// geometry the admin network plan reports. It is read-only (it never allocates or
// releases a block) and safe for concurrent use.
func (a *PortAllocator) PublishedPortBlock() (PoolPortBlock, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return resolvePoolPortBlock(a.policy, a.slots)
}
