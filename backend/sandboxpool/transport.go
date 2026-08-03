package sandboxpool

// ci#792 item 1, consumer half (parent ci#546, render half tracked as ci#846) -
// publish the resolved pool port block on the pool transport.
//
// portblock.go resolves and renders the whole-pool span (PoolPortBlock with
// PublishLines / Env / HealthCheck), but nothing tied that declaration to the live
// allocator or to the per-workspace block a chat-lead actually has to dial, so the
// declared range was not provably reachable from outside the pod and "my dev server
// is unreachable" looked like a plain timeout. This file closes that half:
//
//   - PoolTransport maps every slot of the published span onto the per-workspace
//     block and loopback alias, renders the pool Service port range (or the
//     equivalent SSH remote-forward rule) and the per-slot reachability check;
//   - ResolvePoolTransport / ResolvePoolTransportFromEnv build it from a policy or
//     from the deployment env, (*PortAllocator).Transport from the live allocator,
//     so the declaration always matches the block the admin network plan reports.
//
// It never mutates the pool, never allocates a block and carries no credential
// material: ports, aliases, env names and host names only.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Transport kinds and the transport env contract.
const (
	// TransportKindServicePortRange is a pool Service (NodePort-style) port range
	// that maps the whole published span onto the pool pod.
	TransportKindServicePortRange = "service-port-range"
	// TransportKindSSHRemoteForward is the equivalent rule on the pool's SSH
	// transport: one remote-forward port per workspace block.
	TransportKindSSHRemoteForward = "ssh-remote-forward"
	// EnvPoolTransportKind selects the transport kind of the declaration.
	EnvPoolTransportKind = "OCI_MSGMATE_CODER_SANDBOX_POOL_TRANSPORT_KIND"
	// EnvPoolTransportHost is the host a consumer dials: the pool's shared target
	// address (never per chat).
	EnvPoolTransportHost = "OCI_MSGMATE_CODER_SANDBOX_POOL_TRANSPORT_HOST"
	// EnvPoolTransportService is the pool Service name of the declaration.
	EnvPoolTransportService = "OCI_MSGMATE_CODER_SANDBOX_POOL_TRANSPORT_SERVICE"
	// DefaultPoolTransportService is the Service name used when none is set.
	DefaultPoolTransportService = "sandbox-pool"
	// DefaultPoolTransportHost is the fallback host of the declaration (the pool
	// pod's own address); a caller normally passes the pool target address.
	DefaultPoolTransportHost = "127.0.0.1"
)

// PoolTransportMapping is one published slot: the per-workspace block, the alias a
// peer inside the pool dials and the address a consumer outside the pool dials.
type PoolTransportMapping struct {
	// Slot is the block index inside the published span.
	Slot int `json:"slot"`
	// PortStart is the first port of the workspace block.
	PortStart int `json:"port_start"`
	// PortEnd is the last (inclusive) port of the workspace block.
	PortEnd int `json:"port_end"`
	// Loopback is the alias a peer inside the pool dials.
	Loopback string `json:"loopback"`
	// Target is host:port outside the pool (the first port of the block).
	Target string `json:"target"`
	// HealthCheck is the read-only reachability probe of that target.
	HealthCheck string `json:"health_check"`
}

// PoolTransport is the resolved transport declaration of a pool: the kind, the
// Service, the published span and the per-slot block mapping. Values are ports,
// aliases, host names and env names only.
type PoolTransport struct {
	// GeneratedAt is the render time (RFC3339, UTC).
	GeneratedAt string `json:"generated_at"`
	// Kind is TransportKindServicePortRange or TransportKindSSHRemoteForward.
	Kind string `json:"kind"`
	// Service is the pool Service name the rule belongs to.
	Service string `json:"service"`
	// PoolHost is the host a consumer dials.
	PoolHost string `json:"pool_host"`
	// Mode is the isolation mode the span was resolved under.
	Mode string `json:"mode"`
	// Block is the published whole-pool span.
	Block PoolPortBlock `json:"block"`
	// Mappings are the per-slot blocks, in slot order.
	Mappings []PoolTransportMapping `json:"mappings,omitempty"`
	// HealthChecks are the reachability probes of the declaration.
	HealthChecks []string `json:"health_checks,omitempty"`
	// Notes are the caveats; never a secret.
	Notes []string `json:"notes,omitempty"`
}

// Valid reports whether the declaration is usable (a kind, a resolvable span and
// at least one published slot).
func (t PoolTransport) Valid() bool {
	return strings.TrimSpace(t.Kind) != "" && t.Block.Valid() && len(t.Mappings) > 0
}

// ServicePortRange renders the port range the rule has to expose, or "" when the
// span is unresolved.
func (t PoolTransport) ServicePortRange() string { return t.Block.String() }

// MappingForSlot returns the mapping of one block index.
func (t PoolTransport) MappingForSlot(slot int) (PoolTransportMapping, bool) {
	for _, mapping := range t.Mappings {
		if mapping.Slot == slot {
			return mapping, true
		}
	}
	return PoolTransportMapping{}, false
}

// HealthCheckForSlot returns the reachability probe of one block index: the probe
// a chat-lead or a health check runs before blaming the agent's dev server.
func (t PoolTransport) HealthCheckForSlot(slot int) (string, bool) {
	mapping, ok := t.MappingForSlot(slot)
	if !ok {
		return "", false
	}
	return mapping.HealthCheck, true
}

// Lines renders the declaration as the CI/admin report block of ci#792 item 1. It
// carries no secret: ports, aliases, host names, env names and notes only.
func (t PoolTransport) Lines() []string {
	if !t.Valid() {
		return []string{"sandbox_pool_transport publish=false reason=no-published-port-block"}
	}
	lines := []string{
		fmt.Sprintf("sandbox_pool_transport publish=true kind=%s service=%s host=%s mode=%s ports=%s block_size=%d slots=%d",
			t.Kind, t.Service, t.PoolHost, t.Mode, t.Block.String(), t.Block.BlockSize, t.Block.Slots),
	}
	block, _ := resolvePoolPortBlock(t.policyView(), t.Block.Slots)
	if env := block.Env(); len(env) > 0 {
		lines = append(lines, fmt.Sprintf("sandbox_pool_transport env=%s", block.envList()))
	}
	for _, mapping := range t.Mappings {
		lines = append(lines, fmt.Sprintf("sandbox_pool_transport slot=%d ports=%d-%d loopback=%s target=%s health_check=%s",
			mapping.Slot, mapping.PortStart, mapping.PortEnd, mapping.Loopback, mapping.Target, mapping.HealthCheck))
	}
	for _, note := range t.Notes {
		lines = append(lines, "sandbox_pool_transport note="+note)
	}
	return lines
}

// policyView rebuilds the minimal policy the published span was resolved from, so
// the env contract of Lines matches the reported span without keeping allocator
// state.
func (t PoolTransport) policyView() NetworkPolicy {
	return NetworkPolicy{
		Mode:            t.Mode,
		PortBase:        t.Block.Base,
		PortBlockSize:   t.Block.BlockSize,
		LoopbackAliases: t.Block.LoopbackAliases,
		LoopbackPrefix:  t.Block.LoopbackPrefix,
	}
}

// NormalizePoolTransportKind resolves the transport kind: the explicit value
// first, then EnvPoolTransportKind, then the Service port range. It reports an
// unusable kind instead of guessing one.
func NormalizePoolTransportKind(kind string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(kind))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(os.Getenv(EnvPoolTransportKind)))
	}
	if raw == "" {
		return TransportKindServicePortRange, nil
	}
	switch raw {
	case TransportKindServicePortRange, TransportKindSSHRemoteForward:
		return raw, nil
	}
	return "", fmt.Errorf("%w: unknown pool transport kind %q (want %s or %s)",
		ErrInvalidNetworkPolicy, kind, TransportKindServicePortRange, TransportKindSSHRemoteForward)
}

// ResolvePoolTransport builds the transport declaration of a policy. The span is
// resolved with the exact slot arithmetic the allocator enforces, so the published
// block equals the block the admin network plan reports; an unusable geometry is
// an error rather than a silently empty declaration.
func ResolvePoolTransport(policy NetworkPolicy, kind, poolHost, service string, now time.Time) (PoolTransport, error) {
	transportKind, err := NormalizePoolTransportKind(kind)
	if err != nil {
		return PoolTransport{}, err
	}

	block, ok := resolvePoolPortBlock(policy, policySlots(policy))
	if !ok {
		return PoolTransport{}, fmt.Errorf("%w: unusable port geometry (base=%d block_size=%d mode=%s), nothing can be published",
			ErrInvalidNetworkPolicy, policy.PortBase, policy.PortBlockSize, policy.Mode)
	}

	return buildPoolTransport(transportKind, block, poolHost, service, now), nil
}

// ResolvePoolTransportFromEnv builds the declaration from the deployment env: the
// transport levers and the network policy of the pool. It is the convenience entry
// point of a server-side or admin render.
func ResolvePoolTransportFromEnv(now time.Time) (PoolTransport, error) {
	return ResolvePoolTransport(NetworkPolicyFromEnv(),
		os.Getenv(EnvPoolTransportKind),
		os.Getenv(EnvPoolTransportHost),
		os.Getenv(EnvPoolTransportService),
		now)
}

// Transport renders the transport declaration of the live allocator, so the
// published span is the span the running pool actually hands out.
func (a *PortAllocator) Transport(kind, poolHost, service string, now time.Time) (PoolTransport, error) {
	transportKind, err := NormalizePoolTransportKind(kind)
	if err != nil {
		return PoolTransport{}, err
	}
	block, ok := a.PublishedPortBlock()
	if !ok {
		return PoolTransport{}, fmt.Errorf("%w: the live allocator has no publishable port geometry (mode=%s)",
			ErrInvalidNetworkPolicy, a.Policy().Mode)
	}
	return buildPoolTransport(transportKind, block, poolHost, service, now), nil
}

func buildPoolTransport(kind string, block PoolPortBlock, poolHost, service string, now time.Time) PoolTransport {
	host := strings.TrimSpace(poolHost)
	if host == "" {
		host = DefaultPoolTransportHost
	}
	svc := strings.TrimSpace(service)
	if svc == "" {
		svc = DefaultPoolTransportService
	}

	transport := PoolTransport{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Kind:        kind,
		Service:     svc,
		PoolHost:    host,
		Mode:        block.Mode,
		Block:       block,
		Mappings:    make([]PoolTransportMapping, 0, block.Slots),
	}
	for slot := 0; slot < block.Slots; slot++ {
		start := block.Base + slot*block.BlockSize
		end := start + block.BlockSize - 1
		target := net.JoinHostPort(host, strconv.Itoa(start))
		mapping := PoolTransportMapping{
			Slot:        slot,
			PortStart:   start,
			PortEnd:     end,
			Loopback:    block.LoopbackFor(slot),
			Target:      target,
			HealthCheck: fmt.Sprintf("tcp-connect %s (slot %d block %d-%d loopback %s)", target, slot, start, end, block.LoopbackFor(slot)),
		}
		transport.Mappings = append(transport.Mappings, mapping)
	}

	transport.HealthChecks = []string{
		block.HealthCheck(host),
		fmt.Sprintf("tcp-connect %s (any published port of block %s)", net.JoinHostPort(host, strconv.Itoa(block.Base)), block.String()),
	}
	transport.Notes = []string{
		fmt.Sprintf("declared as %s: the pool %s has to expose ports %s, otherwise the block the admin network plan reports is not reachable",
			kind, svc, block.String()),
		"a consumer dials the pool host, never a per-chat address; the loopback alias is only meaningful inside the pool pod",
		"the health check is a TCP connect: it proves the transport publishes the span, not that the workspace's dev server is bound to it",
	}
	return transport
}
