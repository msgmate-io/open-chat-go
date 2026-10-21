package sandboxpool

import (
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// This file is follow-up #2 of the ci#792 batch of the ci#546 shared-sandbox
// networking design: it surfaces the resolved NetworkPolicy / PortAllocator plan
// (mode, port block geometry, loopback alias, workspace count) so an operator can
// see why a workspace got a given block and why a scope was refused. It is a pure
// read surface: it never allocates, releases or gates anything, and it carries no
// credential material (ports, aliases and env names only), exactly like the
// ci#613 pool egress view it sits next to.

// NetworkPlanStatus is the operator view of the resolved pool network plan. The
// Env* fields mirror the raw deployment levers so an operator can see which knob
// moved the plan without reading the pod environment.
type NetworkPlanStatus struct {
	GeneratedAt string `json:"generated_at"`
	// Mode is NetworkModeShared or NetworkModeNetns.
	Mode string `json:"mode"`
	// LoopbackAliases reports whether per-workspace loopback aliases are in use.
	LoopbackAliases bool `json:"loopback_aliases"`
	// PortBase is the first port of the first block.
	PortBase int `json:"port_base"`
	// PortBlockSize is how many ports one workspace owns.
	PortBlockSize int `json:"port_block_size"`
	// MaxWorkspaces is the workspace ceiling of the policy.
	MaxWorkspaces int `json:"max_workspaces"`
	// LoopbackPrefix is the two-octet prefix the aliases are derived from.
	LoopbackPrefix string `json:"loopback_prefix"`
	// ReservedPorts are the pool's own listeners no block may cover.
	ReservedPorts []int `json:"reserved_ports"`
	// NetnsAvailable reports whether the pool can create per-workspace netns.
	NetnsAvailable bool `json:"netns_available"`
	// IsolationRepos are the repos/owners that always require netns mode.
	IsolationRepos []string `json:"isolation_repos,omitempty"`

	// Slots is how many blocks the resolved policy provides (the effective
	// ceiling: min(port-range blocks, MaxWorkspaces)).
	Slots int `json:"slots"`
	// ActiveWorkspaces is how many blocks are currently allocated.
	ActiveWorkspaces int `json:"active_workspaces"`
	// FreeSlots is how many blocks a new workspace could still take.
	FreeSlots int `json:"free_slots"`
	// Exhausted is the ci#792 port-block exhaustion condition: no free block is
	// left, so the next lease in shared mode fails with ErrPoolNetworkCapacity.
	Exhausted bool `json:"exhausted"`
	// ExhaustionSignal is the stable, machine-readable name of the exhaustion
	// condition (PoolNetworkExhaustedSignal), set only when Exhausted is true. It
	// is the surfaceable half of the ci#792 signal: a status view, an event or a
	// metric labels a full pool with it, so automation does not have to parse a
	// message. Empty means "not full".
	ExhaustionSignal string `json:"exhaustion_signal,omitempty"`
	// ExhaustionRemediations are the documented ways out of a full pool, in the
	// order the lease path should try them (another pool pod, then a dedicated
	// sandbox). Set only when Exhausted is true.
	ExhaustionRemediations []string `json:"exhaustion_remediations,omitempty"`
	// PortGeometry renders the block layout (base+size*slots) for the logs.
	PortGeometry string `json:"port_geometry"`
	// Workspaces are the live plans, sorted by block index: the port block and
	// loopback alias each workspace currently owns.
	Workspaces []WorkspaceNetworkPlan `json:"workspaces,omitempty"`
	// Notes are the human-readable caveats of the plan (isolation semantics,
	// reserved ports, exhaustion remediation). Never a secret.
	Notes []string `json:"notes,omitempty"`
	// PolicyError is set when the resolved policy cannot be applied. The lease
	// path fails closed in that case (see NetworkPolicy.Validate), so this is the
	// operator-visible reason a pool refuses every workspace.
	PolicyError string `json:"policy_error,omitempty"`

	EnvMode            string `json:"env_mode"`
	EnvLoopbackAliases string `json:"env_loopback_aliases"`
	EnvPortBase        string `json:"env_port_base"`
	EnvPortBlockSize   string `json:"env_port_block_size"`
	EnvMaxWorkspaces   string `json:"env_max_workspaces"`
	EnvLoopbackPrefix  string `json:"env_loopback_prefix"`
	EnvIsolationRepos  string `json:"env_isolation_repos"`
}

// leversFromEnv mirrors the deployment levers of the plan.
func leversFromEnv() map[string]string {
	return map[string]string{
		"mode":      os.Getenv(EnvNetworkMode),
		"aliases":   os.Getenv(EnvNetworkLoopbackAliases),
		"port_base": os.Getenv(EnvNetworkPortBase),
		"block":     os.Getenv(EnvNetworkPortBlockSize),
		"workspace": os.Getenv(EnvNetworkMaxWorkspaces),
		"prefix":    os.Getenv(EnvNetworkLoopbackPrefix),
		"repos":     os.Getenv(EnvNetworkIsolationRepos),
	}
}

// baseNetworkPlanStatus renders the policy fields (and the raw levers) of a plan.
// It is pure and never touches allocator state.
func baseNetworkPlanStatus(policy NetworkPolicy, now time.Time) NetworkPlanStatus {
	levers := leversFromEnv()
	return NetworkPlanStatus{
		GeneratedAt:        now.UTC().Format(time.RFC3339),
		Mode:               policy.Mode,
		LoopbackAliases:    policy.LoopbackAliases,
		PortBase:           policy.PortBase,
		PortBlockSize:      policy.PortBlockSize,
		MaxWorkspaces:      policy.MaxWorkspaces,
		LoopbackPrefix:     policy.LoopbackPrefix,
		ReservedPorts:      append([]int(nil), policy.ReservedPorts...),
		NetnsAvailable:     policy.NetnsAvailable,
		IsolationRepos:     append([]string(nil), policy.IsolationRepos...),
		EnvMode:            levers["mode"],
		EnvLoopbackAliases: levers["aliases"],
		EnvPortBase:        levers["port_base"],
		EnvPortBlockSize:   levers["block"],
		EnvMaxWorkspaces:   levers["workspace"],
		EnvLoopbackPrefix:  levers["prefix"],
		EnvIsolationRepos:  levers["repos"],
	}
}

// planNotes are the documented caveats of a mode, mirroring the failure modes of
// the package doc. They are advisory text only.
func planNotes(policy NetworkPolicy) []string {
	notes := []string{
		"a hard-coded dev-server port can still collide; take the port from PORT / CODER_PORT_BLOCK_START",
		"reaching a workspace dev server from outside the pod needs a published port, not the loopback alias (ci#792 follow-up)",
	}
	if policy.Mode == NetworkModeShared {
		notes = append(notes,
			"shared mode shares processes and the network namespace between workspaces; sensitive repos/scope are refused and need mode netns or a dedicated sandbox")
	}
	if policy.Mode == NetworkModeNetns {
		notes = append(notes,
			"netns mode gives each workspace a private loopback; egress still passes through the ci#613 pool egress gate")
	}
	if !policy.NetnsAvailable && policy.Mode == NetworkModeNetns {
		notes = append(notes,
			"mode netns was requested but this pool reports no per-workspace namespace support: every lease fails closed with ErrWorkspaceIsolationUnavailable")
	}
	if len(policy.ReservedPorts) > 0 {
		notes = append(notes, "blocks overlapping a reserved pool port are skipped")
	}
	return notes
}

// StatusAt renders the live plan of the allocator: policy, allocated blocks and
// the ci#792 exhaustion condition. Safe for concurrent use; now is injected so the
// rendering is testable.
func (a *PortAllocator) StatusAt(now time.Time) NetworkPlanStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked(now)
}

// Status renders the live plan of the allocator (see StatusAt).
func (a *PortAllocator) Status() NetworkPlanStatus { return a.StatusAt(time.Now()) }

func (a *PortAllocator) statusLocked(now time.Time) NetworkPlanStatus {
	policy := a.policy
	status := baseNetworkPlanStatus(policy, now)
	status.Slots = a.slots
	status.ActiveWorkspaces = len(a.assigned)
	status.FreeSlots = a.slots - len(a.assigned)
	if status.FreeSlots < 0 {
		status.FreeSlots = 0
	}
	status.Exhausted = status.FreeSlots == 0
	status.PortGeometry = portGeometry(policy, a.slots)
	status.Workspaces = plansByBlockIndex(a.assigned)
	status.Notes = planNotes(policy)
	if status.Exhausted {
		// The surfaceable half of the ci#792 signal, next to the advisory note:
		// automation reads the name and the remediations, a human reads the note.
		status.ExhaustionSignal = PoolNetworkExhaustedSignal
		status.ExhaustionRemediations = exhaustionRemediations()
		status.Notes = append(status.Notes, exhaustionNote(policy, a.slots))
	}
	return status
}

// exhaustionRemediations returns the documented ways out of a full pool in the
// order the lease path should try them. Copied per call so a caller cannot mutate
// a shared slice.
func exhaustionRemediations() []string {
	return []string{RemediationSpillPoolPod, RemediationDedicatedSandbox}
}

// plansByBlockIndex returns the live plans sorted by block index so the admin view
// is stable. Values are copied, so a caller cannot mutate allocator state.
func plansByBlockIndex(assigned map[string]WorkspaceNetworkPlan) []WorkspaceNetworkPlan {
	out := make([]WorkspaceNetworkPlan, 0, len(assigned))
	for _, plan := range assigned {
		out = append(out, plan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BlockIndex < out[j].BlockIndex })
	return out
}

func portGeometry(policy NetworkPolicy, slots int) string {
	if slots <= 0 {
		return ""
	}
	last := policy.PortBase + slots*policy.PortBlockSize - 1
	return "[" + strconv.Itoa(policy.PortBase) + "+" + strconv.Itoa(policy.PortBlockSize) + "*" + strconv.Itoa(slots) + " => " + strconv.Itoa(last) + "]"
}

func exhaustionNote(policy NetworkPolicy, slots int) string {
	if policy.Mode == NetworkModeNetns {
		return "all " + strconv.Itoa(slots) + " blocks are in use: the pool is full, free a lease or raise " + EnvNetworkMaxWorkspaces
	}
	return "all " + strconv.Itoa(slots) + " blocks are in use: the next shared-mode lease fails with ErrPoolNetworkCapacity; free a lease, raise " + EnvNetworkMaxWorkspaces + ", or start another pool pod"
}

// NetworkPlanStatusFromEnv renders the plan that the deployment env resolves to,
// without touching the live allocator. It is what an operator sees for a pool
// whose lease path has not attached an allocator yet, and it is where an invalid
// deployment lever shows up as PolicyError.
func NetworkPlanStatusFromEnv(now time.Time) NetworkPlanStatus {
	policy := NetworkPolicyFromEnv()
	status := baseNetworkPlanStatus(policy, now)
	alloc, err := NewPortAllocator(policy)
	if err != nil {
		status.PolicyError = err.Error()
		status.Notes = append(planNotes(policy),
			"the resolved policy cannot be applied, so the pool refuses every lease until the deployment levers are fixed")
		return status
	}
	status.Slots = alloc.slots
	status.FreeSlots = alloc.slots
	status.PortGeometry = portGeometry(policy, alloc.slots)
	status.Notes = planNotes(policy)
	return status
}

var (
	defaultAllocatorMu       sync.Mutex
	defaultAllocatorResolved bool
	defaultAllocator         *PortAllocator
	defaultAllocatorErr      error
)

// DefaultAllocator returns the process-wide allocator the pool lease path uses. It
// resolves the policy from the deployment env once (like the ci#613 DefaultGate)
// and remembers the resolution error, so an invalid mode fails closed instead of
// silently degrading to shared. Allocate/Release are safe for concurrent use.
func DefaultAllocator() (*PortAllocator, error) {
	defaultAllocatorMu.Lock()
	defer defaultAllocatorMu.Unlock()
	if !defaultAllocatorResolved || defaultAllocator == nil {
		defaultAllocator, defaultAllocatorErr = NewPortAllocator(NetworkPolicyFromEnv())
		defaultAllocatorResolved = true
	}
	return defaultAllocator, defaultAllocatorErr
}

// SetDefaultAllocator attaches the allocator the pool start path (and tests) use.
// Passing nil clears the attachment so the next DefaultAllocator call re-resolves
// the deployment env.
func SetDefaultAllocator(alloc *PortAllocator) {
	defaultAllocatorMu.Lock()
	defer defaultAllocatorMu.Unlock()
	defaultAllocator = alloc
	defaultAllocatorErr = nil
	defaultAllocatorResolved = alloc != nil
}

// ResolveNetworkPlanStatus renders the operator view of the process-wide plan: the
// attached live allocator when one exists, otherwise the env-resolved policy with
// the resolution error so a broken deployment is visible before the first lease.
// It never allocates, releases or lazily creates a block.
func ResolveNetworkPlanStatus(now time.Time) NetworkPlanStatus {
	defaultAllocatorMu.Lock()
	alloc, err := defaultAllocator, defaultAllocatorErr
	attached := defaultAllocatorResolved && alloc != nil
	defaultAllocatorMu.Unlock()

	if attached {
		return alloc.StatusAt(now)
	}

	status := NetworkPlanStatusFromEnv(now)
	if err != nil && status.PolicyError == "" {
		status.PolicyError = err.Error()
	}
	status.Notes = append(status.Notes,
		"no live allocator is attached yet: this is the plan the deployment env resolves to")
	return status
}
