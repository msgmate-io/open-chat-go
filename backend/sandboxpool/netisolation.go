package sandboxpool

// ci#546 - networking & isolation design for shared/pooled coding sandboxes.
//
// A pooled backing sandbox is provisioned once and then accepts leases from many
// chats, each with its own random workspace path (ci#603/ci#614). Several agents
// therefore share ONE pod and, unless something changes, ONE network namespace.
// This file is the bounded policy that makes that sharing deliberate instead of
// discovering the conflicts at runtime.
//
// # Design note
//
// Modes. There are exactly two, and the default is the bounded shared one:
//
//	shared (default) - one pod netns for all workspaces. Every workspace gets a
//	  deterministic, unique port block and (unless aliases are switched off) a
//	  unique loopback alias; both are injected into the session environment.
//	netns - per-workspace network namespace (slirp4netns/userns). Full
//	  separation of processes *and* ports, at the cost of requiring user
//	  namespaces / privileged helpers on the pool. Requires
//	  NetnsAvailable == true; a pool that cannot offer it fails closed instead
//	  of silently downgrading.
//
// port conflicts. A dev server on a fixed port (3000, 8080, ...) in two
// workspaces of one pod collides. The bounded policy hands each workspace a
// disjoint block [start, start+BlockSize) out of a fixed base (default 12000,
// 10 ports per workspace) and injects PORT / DEV_SERVER_PORT / VITE_PORT plus
// CODER_PORT_BLOCK_START/END, so a well-behaved server takes its port from the
// environment. Blocks never overlap, never include the pool's own reserved
// ports (SSH 22 by default) and never enter the privileged range (<1024).
//
// localhost semantics. Agents assume localhost:<port> is theirs. In the shared
// netns that is true for the *ports* (blocks are disjoint) and false for the
// *name* once two workspaces both bind 127.0.0.1:3000. The policy therefore
// derives a per-workspace loopback alias 127.64.<a>.<b> from the allocation
// index and injects it as CODER_LOCALHOST: the correct host is
// "$CODER_LOCALHOST:<port>", which is also what the coder tool result reports
// for a server it started. A literal 127.0.0.1 bind stays a documented
// limitation of the shared mode, not a runtime surprise.
//
// DNS and service access. Nothing here touches DNS, NAT or egress: the pool must
// still pass the ci#613 pool-level DNS/egress gate (which covers the ci#497
// credential endpoints `gh` needs). Port allocation cannot break a credential,
// because ci#603/ci#614 credentials live in a per-workspace HOME and are
// re-provisioned per container generation. A pool restart re-derives the same
// plan for the same live workspace set, so replay stays idempotent.
//
// service discovery of the shared sandbox. The SSH/target address of a shared
// sandbox is the pool pod, not the chat: the workspace path (and the injected
// block) are what differentiate one agent from another. A dev server bound
// inside the pod is only reachable from outside if the pool publishes that port,
// which is why WorkspaceServiceAddress renders "<pool-host>:<port>" explicitly
// and the failure modes say it is not exposed by default.
//
// isolation. In the shared mode one agent can see another agent's processes and
// network. That is acceptable only for the ephemeral dev mode and never for a
// sensitive repo/scope: those require netns mode (or a dedicated sandbox) and
// are refused here otherwise. Gate by repo/scope, fail closed.
//
// resource contention. N agents in one pod share CPU/mem/disk. This package
// bounds N itself (MaxWorkspaces, default 8) so the ci#662 memory-derived
// agents-per-pool bound and this network bound have to agree; whichever is
// smaller wins at the call site. Per-workspace cgroups stay the pool's business.
//
// Failure modes (all fail closed, all remediated in the returned error):
//
//  1. block exhaustion - more live workspaces than blocks: the lease is refused
//     instead of two workspaces silently sharing a port block.
//  2. reserved/privileged collisions - a block is skipped when it would cover a
//     reserved pool port or the privileged range.
//  3. alias collision - the alias is derived from the block index, so unique
//     blocks imply unique aliases; allocation is tracked per workspace UUID.
//  4. netns requested but unavailable - error, never a downgrade.
//  5. sensitive scope in shared mode - error, never a downgrade.
//  6. hard-coded ports outside the block - not preventable from here; the tool
//     result and the report line name the block so the agent can move the port.
//  7. localhost assumptions - injected CODER_LOCALHOST; a literal 127.0.0.1
//     bind remains the documented shared-mode limitation.
//
// Follow-ups (one batched ticket): publish the allocated block through the pool
// Service so an operator can reach a workspace dev server; expose the plan on the
// admin surface next to ProviderAdmission; add a per-workspace cgroup guard.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Network isolation modes.
const (
	// NetworkModeShared is one pod netns for every workspace, with disjoint port
	// blocks and (by default) per-workspace loopback aliases. The default.
	NetworkModeShared = "shared"
	// NetworkModeNetns is a per-workspace network namespace (slirp4netns/userns).
	// Mandatory for sensitive scopes; requires NetnsAvailable.
	NetworkModeNetns = "netns"
)

// Deployment levers. All optional; with nothing set the bounded shared policy of
// DefaultNetworkPolicy applies.
const (
	// EnvNetworkMode selects NetworkModeShared or NetworkModeNetns.
	EnvNetworkMode = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_MODE"
	// EnvNetworkLoopbackAliases toggles per-workspace loopback aliases ("0"/"false").
	EnvNetworkLoopbackAliases = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_LOOPBACK_ALIASES"
	// EnvNetworkPortBase is the first port of the first block.
	EnvNetworkPortBase = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_PORT_BASE"
	// EnvNetworkPortBlockSize is how many ports one workspace owns.
	EnvNetworkPortBlockSize = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_PORT_BLOCK_SIZE"
	// EnvNetworkMaxWorkspaces bounds how many workspaces one pool may host.
	EnvNetworkMaxWorkspaces = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_MAX_WORKSPACES"
	// EnvNetworkLoopbackPrefix is the two-octet prefix of the loopback alias,
	// "127.64" by default (ie the 127.64.0.0/16 slice, which never collides with
	// the loopback address itself).
	EnvNetworkLoopbackPrefix = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_LOOPBACK_PREFIX"
	// EnvNetworkIsolationRepos is a comma-separated list of "owner/name" repos (or
	// whole owners, "owner/*") that may never share a network namespace.
	EnvNetworkIsolationRepos = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_ISOLATION_REPOS"
)

// Defaults of the bounded shared policy.
const (
	// DefaultNetworkPortBase keeps the pool above the privileged range and out of
	// the way of the common dev-server ports.
	DefaultNetworkPortBase = 12000
	// DefaultNetworkPortBlockSize is enough for a dev server, a Vite HMR port, a
	// test server and a couple of headroom ports.
	DefaultNetworkPortBlockSize = 10
	// DefaultNetworkMaxWorkspaces bounds N agents per pod (ci#546 resource
	// contention) and must not exceed the ci#662 memory-derived bound.
	DefaultNetworkMaxWorkspaces = 8
	// DefaultNetworkLoopbackPrefix is the 127.64.0.0/16 loopback slice.
	DefaultNetworkLoopbackPrefix = "127.64"
	// MinAllocatablePort is the floor: below it ports need privileges.
	MinAllocatablePort = 1024
	// MaxAllocatablePort is the ceiling of the allocatable range.
	MaxAllocatablePort = 65535
	// MinNetworkPortBlockSize keeps a block usable.
	MinNetworkPortBlockSize = 2
)

// DefaultReservedPorts are the pool pod's own listeners that a workspace block
// must never cover. The list is deliberately small and overridable: the SSH
// server of the pool is 22, the backend is configured, not guessed.
var DefaultReservedPorts = []int{22}

// Sentinel errors; match with errors.Is.
var (
	// ErrPoolNetworkCapacity means no free port block is left for a new workspace.
	ErrPoolNetworkCapacity = errors.New("sandbox pool network: no free port block for another workspace")
	// ErrSharedNetnsDenied means the scope/repo requires a per-workspace network
	// namespace and the pool is in shared mode.
	ErrSharedNetnsDenied = errors.New("sandbox pool network: shared network namespace is not allowed for this repo/scope")
	// ErrWorkspaceIsolationUnavailable means netns mode was asked for but the pool
	// cannot offer it (no userns/slirp support or explicitly disabled).
	ErrWorkspaceIsolationUnavailable = errors.New("sandbox pool network: per-workspace network namespace is not available on this pool")
	// ErrInvalidNetworkPolicy means the resolved policy cannot be applied.
	ErrInvalidNetworkPolicy = errors.New("sandbox pool network: invalid network policy")
	// ErrInvalidWorkspaceUUID means the workspace cannot be identified.
	ErrInvalidWorkspaceUUID = errors.New("sandbox pool network: invalid workspace uuid")
)

// IsPoolNetworkFailure reports whether err came from this policy.
func IsPoolNetworkFailure(err error) bool {
	return errors.Is(err, ErrPoolNetworkCapacity) ||
		errors.Is(err, ErrSharedNetnsDenied) ||
		errors.Is(err, ErrWorkspaceIsolationUnavailable) ||
		errors.Is(err, ErrInvalidNetworkPolicy) ||
		errors.Is(err, ErrInvalidWorkspaceUUID)
}

// NetworkPolicy is the resolved, operator-readable networking policy of a pool.
type NetworkPolicy struct {
	// Mode is NetworkModeShared or NetworkModeNetns.
	Mode string
	// LoopbackAliases injects a per-workspace loopback alias (shared mode only).
	LoopbackAliases bool
	// PortBase is the first port of the first block.
	PortBase int
	// PortBlockSize is how many ports one workspace owns.
	PortBlockSize int
	// MaxWorkspaces is the ceiling on live workspaces per pool.
	MaxWorkspaces int
	// LoopbackPrefix is the two-octet prefix of the derived aliases.
	LoopbackPrefix string
	// NetnsAvailable reports whether the pool can create per-workspace netns.
	NetnsAvailable bool
	// IsolationRepos are repos/owners that always require netns mode.
	IsolationRepos []string
	// ReservedPorts are ports no block may cover.
	ReservedPorts []int
}

// DefaultNetworkPolicy is the bounded shared-netns policy: disjoint blocks,
// aliases on, 8 workspaces, loopback alias host in 127.64.0.0/16.
func DefaultNetworkPolicy() NetworkPolicy {
	return NetworkPolicy{
		Mode:            NetworkModeShared,
		LoopbackAliases: true,
		PortBase:        DefaultNetworkPortBase,
		PortBlockSize:   DefaultNetworkPortBlockSize,
		MaxWorkspaces:   DefaultNetworkMaxWorkspaces,
		LoopbackPrefix:  DefaultNetworkLoopbackPrefix,
		ReservedPorts:   append([]int(nil), DefaultReservedPorts...),
	}
}

// NetworkPolicyFromEnv resolves the policy from the deployment env, falling back
// to DefaultNetworkPolicy per field. An unparseable value is ignored (the default
// applies); an unknown mode is kept, so Validate fails closed instead of
// silently degrading a netns deployment to a shared one.
func NetworkPolicyFromEnv() NetworkPolicy {
	policy := DefaultNetworkPolicy()
	if raw := strings.TrimSpace(os.Getenv(EnvNetworkMode)); raw != "" {
		policy.Mode = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv(EnvNetworkLoopbackAliases)); raw != "" {
		policy.LoopbackAliases = !isFalsey(raw)
	}
	if value, ok := envPositiveInt(EnvNetworkPortBase); ok {
		policy.PortBase = value
	}
	if value, ok := envPositiveInt(EnvNetworkPortBlockSize); ok {
		policy.PortBlockSize = value
	}
	if value, ok := envPositiveInt(EnvNetworkMaxWorkspaces); ok {
		policy.MaxWorkspaces = value
	}
	if raw := strings.TrimSpace(os.Getenv(EnvNetworkLoopbackPrefix)); raw != "" {
		policy.LoopbackPrefix = raw
	}
	if raw := strings.TrimSpace(os.Getenv(EnvNetworkIsolationRepos)); raw != "" {
		policy.IsolationRepos = splitList(raw)
	}
	return policy
}

// DefaultNetworkModeEnvKey is a nil-safe alias for the env key constant, kept so
// callers that build a plan report do not have to import the string twice.
const DefaultNetworkModeEnvKey = EnvNetworkMode

func isFalsey(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off", "disabled":
		return true
	}
	return false
}

func envPositiveInt(key string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Validate rejects a policy that cannot be applied. It is what makes a
// mis-typed mode fail closed rather than fall back to an unbounded shared netns.
func (p NetworkPolicy) Validate() error {
	switch p.Mode {
	case NetworkModeShared, NetworkModeNetns:
	default:
		return fmt.Errorf("%w: unknown mode %q (expected %s or %s, see %s)",
			ErrInvalidNetworkPolicy, p.Mode, NetworkModeShared, NetworkModeNetns, EnvNetworkMode)
	}
	if p.PortBase < MinAllocatablePort {
		return fmt.Errorf("%w: port base %d is below the allocatable floor %d (privileged ports)",
			ErrInvalidNetworkPolicy, p.PortBase, MinAllocatablePort)
	}
	if p.PortBase > MaxAllocatablePort {
		return fmt.Errorf("%w: port base %d is above %d", ErrInvalidNetworkPolicy, p.PortBase, MaxAllocatablePort)
	}
	if p.PortBlockSize < MinNetworkPortBlockSize {
		return fmt.Errorf("%w: port block size %d is below %d", ErrInvalidNetworkPolicy, p.PortBlockSize, MinNetworkPortBlockSize)
	}
	if p.MaxWorkspaces <= 0 {
		return fmt.Errorf("%w: max workspaces %d must be positive", ErrInvalidNetworkPolicy, p.MaxWorkspaces)
	}
	if p.LoopbackAliases {
		if _, err := aliasPrefix(p.LoopbackPrefix, 0); err != nil {
			return fmt.Errorf("%w: loopback prefix %q is not a two-octet IPv4 prefix", ErrInvalidNetworkPolicy, p.LoopbackPrefix)
		}
	}
	for _, port := range p.ReservedPorts {
		if port < 1 || port > MaxAllocatablePort {
			return fmt.Errorf("%w: reserved port %d is outside 1-%d", ErrInvalidNetworkPolicy, port, MaxAllocatablePort)
		}
	}
	if p.Mode == NetworkModeNetns && !p.NetnsAvailable {
		return fmt.Errorf("%w: mode %s requires a pool with user-namespace/slirp support", ErrInvalidNetworkPolicy, NetworkModeNetns)
	}
	return nil
}

// RequiresIsolation reports whether a repo/scope may never share a network
// namespace. Scope wins first ("sensitive", "private"), then the configured
// repo/owner list (exact "owner/name" or "owner/*").
func (p NetworkPolicy) RequiresIsolation(repo, scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "sensitive", "private", "restricted":
		return true
	}
	normalised := strings.ToLower(strings.TrimSpace(repo))
	if normalised == "" {
		return false
	}
	for _, pattern := range p.IsolationRepos {
		candidate := strings.ToLower(strings.TrimSpace(pattern))
		if candidate == "" {
			continue
		}
		if candidate == normalised {
			return true
		}
		if strings.HasSuffix(candidate, "/*") {
			if strings.HasPrefix(normalised, strings.TrimSuffix(candidate, "*")) {
				return true
			}
		}
	}
	return false
}

// WorkspaceNetworkPlan is the resolved networking of ONE workspace inside a pool.
// It never carries credential material: only ports, a loopback alias and the env
// names/values the session needs.
type WorkspaceNetworkPlan struct {
	// WorkspaceUUID is the workspace the plan belongs to.
	WorkspaceUUID string `json:"workspace_uuid"`
	// Mode is the isolation mode the plan was allocated under.
	Mode string `json:"mode"`
	// PortStart is the first port of the workspace's block.
	PortStart int `json:"port_start"`
	// PortEnd is the last (inclusive) port of the block.
	PortEnd int `json:"port_end"`
	// LoopbackHost is the per-workspace alias (or 127.0.0.1 when aliases are off).
	LoopbackHost string `json:"loopback_host"`
	// BlockIndex is the allocation index the alias is derived from.
	BlockIndex int `json:"block_index"`
	// Env is the session environment to inject. Values are names/ports/addresses only.
	Env map[string]string `json:"env"`
	// Notes are the human-readable caveats of the plan (localhost semantics,
	// reserved ports of the pool, ...). Never a secret.
	Notes []string `json:"notes,omitempty"`
}

// Describe renders the plan as one operator/agent-readable line.
func (p WorkspaceNetworkPlan) Describe() string {
	return fmt.Sprintf("workspace=%s mode=%s ports=%d-%d loopback=%s",
		p.WorkspaceUUID, p.Mode, p.PortStart, p.PortEnd, p.LoopbackHost)
}

// ReportLines renders the plan as the CI/admin report block of ci#546: the
// allocation, the injected env names and the documented failure modes. No secret.
func (p WorkspaceNetworkPlan) ReportLines() []string {
	lines := []string{
		fmt.Sprintf("sandbox_network workspace=%s mode=%s ports=%d-%d block_index=%d loopback=%s",
			p.WorkspaceUUID, p.Mode, p.PortStart, p.PortEnd, p.BlockIndex, p.LoopbackHost),
		fmt.Sprintf("sandbox_network workspace=%s env=%s", p.WorkspaceUUID, sortedEnvNames(p.Env)),
	}
	for _, note := range p.Notes {
		lines = append(lines, fmt.Sprintf("sandbox_network workspace=%s note=%s", p.WorkspaceUUID, note))
	}
	return lines
}

func sortedEnvNames(env map[string]string) string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// PortAllocator hands out one disjoint port block (and loopback alias) per live
// workspace of a pool, and is the thing that makes the bounded shared netns
// policy collision-free. Allocation is idempotent per workspace UUID, so the
// ci#614 replay path re-derives the same plan for the same workspace.
type PortAllocator struct {
	policy NetworkPolicy
	// slots is the number of allocatable blocks of the policy.
	slots int

	mu       sync.Mutex
	assigned map[string]WorkspaceNetworkPlan
	// blockIndex maps an allocation index to the workspace that owns it.
	blockIndex map[int]string
}

// NewPortAllocator validates the policy and builds the allocator.
func NewPortAllocator(policy NetworkPolicy) (*PortAllocator, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	slots := (MaxAllocatablePort - policy.PortBase + 1) / policy.PortBlockSize
	if slots > policy.MaxWorkspaces {
		// The workspace bound of the ci#546 resource-contention policy, not the
		// port range, is the effective ceiling.
		slots = policy.MaxWorkspaces
	}
	if slots <= 0 {
		return nil, fmt.Errorf("%w: port base %d and block size %d leave no block",
			ErrInvalidNetworkPolicy, policy.PortBase, policy.PortBlockSize)
	}
	return &PortAllocator{
		policy:     policy,
		slots:      slots,
		assigned:   make(map[string]WorkspaceNetworkPlan),
		blockIndex: make(map[int]string),
	}, nil
}

// Policy returns the policy the allocator enforces.
func (a *PortAllocator) Policy() NetworkPolicy { return a.policy }

// Slots returns how many blocks the policy provides.
func (a *PortAllocator) Slots() int { return a.slots }

// ActiveWorkspaces returns the currently allocated workspace UUIDs, sorted.
func (a *PortAllocator) ActiveWorkspaces() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.assigned))
	for uuid := range a.assigned {
		out = append(out, uuid)
	}
	sort.Strings(out)
	return out
}

// Allocate returns the plan of a workspace, allocating it on first use. It is
// idempotent: the same workspace always gets the same ports and alias, which is
// what keeps a pool restart and the ci#614 credential replay idempotent. repo and
// scope drive the isolation gate only; they are never stored.
func (a *PortAllocator) Allocate(workspaceUUID, repo, scope string) (WorkspaceNetworkPlan, error) {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return WorkspaceNetworkPlan{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allocateLocked(uuid, repo, scope)
}

func (a *PortAllocator) allocateLocked(uuid, repo, scope string) (WorkspaceNetworkPlan, error) {
	if existing, ok := a.assigned[uuid]; ok {
		return existing, nil
	}
	// Isolation gate first: a sensitive workspace must not learn a shared plan it
	// is not allowed to use.
	if a.policy.RequiresIsolation(repo, scope) && a.policy.Mode != NetworkModeNetns {
		return WorkspaceNetworkPlan{}, fmt.Errorf(
			"%w: workspace %s repo %q scope %q needs a per-workspace network namespace: use a dedicated sandbox, or set %s=%s (and %s for the repo/scope list)",
			ErrSharedNetnsDenied, uuid, repo, scope, EnvNetworkMode, NetworkModeNetns, EnvNetworkIsolationRepos)
	}
	if a.policy.Mode == NetworkModeNetns && !a.policy.NetnsAvailable {
		return WorkspaceNetworkPlan{}, fmt.Errorf(
			"%w: workspace %s: mode %s was requested but this pool cannot create per-workspace namespaces",
			ErrWorkspaceIsolationUnavailable, uuid, NetworkModeNetns)
	}
	index, ok := a.freeBlockIndex(uuid)
	if !ok {
		return WorkspaceNetworkPlan{}, fmt.Errorf(
			"%w: workspace %s: %d of %d blocks in %d+%d*%d are in use; release a lease or raise %s (the ci#662 memory bound must allow it too)",
			ErrPoolNetworkCapacity, uuid, len(a.assigned), a.slots,
			a.policy.PortBase, a.policy.PortBlockSize, a.slots-1, EnvNetworkMaxWorkspaces)
	}
	plan, err := a.planForIndex(uuid, index)
	if err != nil {
		return WorkspaceNetworkPlan{}, err
	}
	a.assigned[uuid] = plan
	a.blockIndex[index] = uuid
	return plan, nil
}

// freeBlockIndex finds the first free block at or after the workspace-derived
// candidate, wrapping once. Deriving the candidate from the UUID spreads adjacent
// workspaces over the range; the linear probe keeps allocation unique.
func (a *PortAllocator) freeBlockIndex(uuid string) (int, bool) {
	start := int(uuidHash(uuid) % uint64(a.slots))
	for offset := 0; offset < a.slots; offset++ {
		index := (start + offset) % a.slots
		if _, taken := a.blockIndex[index]; taken {
			continue
		}
		if a.policy.blockOverlapsReserved(index) {
			continue
		}
		return index, true
	}
	return 0, false
}

func (p NetworkPolicy) blockOverlapsReserved(index int) bool {
	start := p.PortBase + index*p.PortBlockSize
	end := start + p.PortBlockSize - 1
	if start < MinAllocatablePort || end > MaxAllocatablePort {
		return true
	}
	for _, port := range p.ReservedPorts {
		if port >= start && port <= end {
			return true
		}
	}
	return false
}

// planForIndex builds the plan of one block. It is pure, so it can be tested
// (and cached/replayed) without any allocator state.
func (a *PortAllocator) planForIndex(uuid string, index int) (WorkspaceNetworkPlan, error) {
	policy := a.policy
	start := policy.PortBase + index*policy.PortBlockSize
	end := start + policy.PortBlockSize - 1
	loopback := "127.0.0.1"
	if policy.LoopbackAliases {
		alias, err := aliasPrefix(policy.LoopbackPrefix, index)
		if err != nil {
			return WorkspaceNetworkPlan{}, err
		}
		loopback = alias
	}
	port := strconv.Itoa(start)
	plan := WorkspaceNetworkPlan{
		WorkspaceUUID: uuid,
		Mode:          policy.Mode,
		PortStart:     start,
		PortEnd:       end,
		LoopbackHost:  loopback,
		BlockIndex:    index,
		Env: map[string]string{
			// The port a well-behaved dev/test server should take, so a fixed
			// 3000/8080 does not collide with the next workspace.
			"PORT":                   port,
			"DEV_SERVER_PORT":        port,
			"VITE_PORT":              port,
			"CODER_PORT_BLOCK_START": port,
			"CODER_PORT_BLOCK_END":   strconv.Itoa(end),
			// localhost semantics: the per-workspace host to bind and to call.
			"CODER_LOCALHOST":    loopback,
			"HOSTNAME_ALIAS":     loopback,
			"CODER_NETWORK_MODE": policy.Mode,
		},
	}
	plan.Notes = []string{
		fmt.Sprintf("bind and call dev servers on %s:%d-%d and read PORT; a literal 127.0.0.1:<port> is shared with peer workspaces in mode %s",
			loopback, start, end, policy.Mode),
		"ports outside the block are not reserved: a hard-coded 8080 can still collide, so take the port from PORT",
		fmt.Sprintf("isolation: %s mode shares processes and network between workspaces; sensitive repos/scope are refused and need %s or a dedicated sandbox",
			NetworkModeShared, NetworkModeNetns),
		"reaching the server from outside the pod needs the pool host and a published port, not localhost",
	}
	if policy.Mode == NetworkModeNetns {
		plan.Notes = append(plan.Notes,
			"mode netns: the workspace has its own network namespace, so 127.0.0.1 is private to it and DNS/egress still pass through the pool's ci#613 gate")
	}
	return plan, nil
}

// Release frees the workspace's block. It reports whether a block was freed and
// is what a lease end / workspace deletion calls; a released workspace that comes
// back gets a fresh plan (its peers may have taken the old block).
func (a *PortAllocator) Release(workspaceUUID string) bool {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, ok := a.assigned[uuid]
	if !ok {
		return false
	}
	delete(a.assigned, uuid)
	delete(a.blockIndex, plan.BlockIndex)
	return true
}

// Plan returns the plan of an allocated workspace.
func (a *PortAllocator) Plan(workspaceUUID string) (WorkspaceNetworkPlan, bool) {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return WorkspaceNetworkPlan{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, ok := a.assigned[uuid]
	return plan, ok
}

// ReportLines renders the allocator state: the policy, the bound and the live
// blocks. It is the pool-side half of the ci#546 report and carries no secret.
func (a *PortAllocator) ReportLines() []string {
	a.mu.Lock()
	allocated := make([]WorkspaceNetworkPlan, 0, len(a.assigned))
	for _, plan := range a.assigned {
		allocated = append(allocated, plan)
	}
	a.mu.Unlock()
	sort.Slice(allocated, func(i, j int) bool { return allocated[i].PortStart < allocated[j].PortStart })

	lines := []string{
		fmt.Sprintf("sandbox_network mode=%s loopback_aliases=%t port_base=%d block_size=%d slots=%d max_workspaces=%d loopback_prefix=%s reserved_ports=%s netns_available=%t",
			a.policy.Mode, a.policy.LoopbackAliases, a.policy.PortBase, a.policy.PortBlockSize,
			a.slots, a.policy.MaxWorkspaces, a.policy.LoopbackPrefix,
			intList(a.policy.ReservedPorts), a.policy.NetnsAvailable),
		fmt.Sprintf("sandbox_network live_workspaces=%d free_blocks=%d", len(allocated), a.slots-len(allocated)),
		fmt.Sprintf("sandbox_network isolation_repos=%s", strings.Join(a.policy.IsolationRepos, ",")),
		fmt.Sprintf("sandbox_network replay_env %s %s %s %s %s %s %s",
			EnvNetworkMode, EnvNetworkLoopbackAliases, EnvNetworkPortBase, EnvNetworkPortBlockSize,
			EnvNetworkMaxWorkspaces, EnvNetworkLoopbackPrefix, EnvNetworkIsolationRepos),
		fmt.Sprintf("sandbox_network dns_egress=ci#613_gate dns_gate_env=%s", EnvTargets),
	}
	for _, plan := range allocated {
		lines = append(lines, fmt.Sprintf("sandbox_network block=%d ports=%d-%d workspace=%s loopback=%s",
			plan.BlockIndex, plan.PortStart, plan.PortEnd, plan.WorkspaceUUID, plan.LoopbackHost))
	}
	return lines
}

// NetworkFailureModes returns the bounded, documented failure modes of the
// policy, for the admin surface and the tool error that explains a refusal.
func NetworkFailureModes(policy NetworkPolicy) []string {
	modes := []string{
		fmt.Sprintf("block exhaustion: more than %d live workspaces per pool -> lease refused (%s)", policy.MaxWorkspaces, ErrPoolNetworkCapacity),
		"reserved or privileged port inside a block -> block skipped, never handed out",
		"loopback alias collision -> impossible while blocks are unique (alias derives from the block index)",
		fmt.Sprintf("per-workspace netns requested but unavailable -> refused, never downgraded (%s)", ErrWorkspaceIsolationUnavailable),
		fmt.Sprintf("sensitive repo/scope in %s mode -> refused, needs %s or a dedicated sandbox (%s)", NetworkModeShared, NetworkModeNetns, ErrSharedNetnsDenied),
		"hard-coded port outside the block -> not preventable here; PORT and the block are reported so the agent can move the port",
		"localhost assumption -> injected as CODER_LOCALHOST; a literal 127.0.0.1 bind stays shared in shared mode",
	}
	return modes
}

// WorkspaceServiceAddress renders how a dev server of one workspace is reached
// from outside the pool. The pool host is the shared sandbox's target address
// (the SSH/target binding points at the pool pod, never per chat); the port is
// the workspace's own block. A port that the pool does not publish is not
// reachable, which the returned note states.
func WorkspaceServiceAddress(poolHost string, plan WorkspaceNetworkPlan) string {
	host := strings.TrimSpace(poolHost)
	if host == "" {
		host = "pool"
	}
	return fmt.Sprintf("%s (port %d of workspace %s, block %d-%d; only reachable when the pool publishes the port)",
		net.JoinHostPort(host, strconv.Itoa(plan.PortStart)), plan.PortStart, plan.WorkspaceUUID, plan.PortStart, plan.PortEnd)
}

// NormalizeWorkspaceUUID trims and validates a workspace UUID: non-empty, at most
// 128 bytes and free of whitespace/control characters. It keeps a UUID out of
// logs, env values and file paths that would break.
func NormalizeWorkspaceUUID(raw string) (string, error) {
	uuid := strings.TrimSpace(raw)
	if uuid == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidWorkspaceUUID)
	}
	if len(uuid) > 128 {
		return "", fmt.Errorf("%w: %d bytes exceeds 128", ErrInvalidWorkspaceUUID, len(uuid))
	}
	for _, r := range uuid {
		if r <= ' ' || r == 0x7f {
			return "", fmt.Errorf("%w: contains whitespace or a control character", ErrInvalidWorkspaceUUID)
		}
	}
	return uuid, nil
}

// aliasPrefix renders the loopback alias for an allocation index inside a
// two-octet IPv4 prefix ("127.64" -> 127.64.<a>.<b>). The index is split over the
// last two octets (1-based, so the alias is never the network or a .0 host).
func aliasPrefix(prefix string, index int) (string, error) {
	parts := strings.Split(strings.TrimSpace(prefix), ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: loopback prefix %q must have two octets", ErrInvalidNetworkPolicy, prefix)
	}
	for _, part := range parts {
		octet, err := strconv.Atoi(part)
		if err != nil || octet < 1 || octet > 255 {
			return "", fmt.Errorf("%w: loopback prefix %q is not a valid two-octet IPv4 prefix", ErrInvalidNetworkPolicy, prefix)
		}
	}
	if index < 0 || index >= 255*254 {
		return "", fmt.Errorf("%w: allocation index %d is outside the loopback alias range", ErrInvalidNetworkPolicy, index)
	}
	return fmt.Sprintf("%s.%d.%d", prefix, index/254+1, index%254+1), nil
}

func uuidHash(uuid string) uint64 {
	sum := sha256.Sum256([]byte(uuid))
	return binary.BigEndian.Uint64(sum[:8])
}

func intList(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
