package sandboxpool

// ci#792 item 5 (parent ci#546) - port-block exhaustion signalling.
//
// MaxWorkspaces bounds a pool pod, but "full" used to surface as the generic
// allocation error, which a caller could not distinguish from a policy or a
// geometry problem and could not act on. This file adds the distinct, surfaceable
// signal the lease path needs: PoolExhaustionSignal names the condition, carries
// the counts and the block geometry, states the spill remediation (another pool pod
// or a dedicated sandbox) and still wraps ErrPoolNetworkCapacity so errors.Is keeps
// working. It is pure reporting: it allocates nothing and carries no credential
// material, only counts, ports and lever names.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Exhaustion signal contract and the spill lever.
const (
	// PoolExhaustionKind is the machine-readable signal kind a lease path and the
	// admin surface switch on, distinct from a policy or geometry failure.
	PoolExhaustionKind = "pool_port_block_exhausted"
	// EnvPoolExhaustionSpill selects the remediation of a full pool:
	// "pod" (spill to another pool pod), "dedicated" (fall back to a dedicated
	// sandbox) or "refuse" (never spill). Defaults to "pod".
	EnvPoolExhaustionSpill = "OCI_MSGMATE_CODER_SANDBOX_POOL_EXHAUSTION_SPILL"
	// SpillActionNewPoolPod is the default remediation: the caller spills to a new
	// pool pod so the chat keeps getting a pooled workspace.
	SpillActionNewPoolPod = "new_pool_pod"
	// SpillActionDedicatedSandbox is the fallback remediation: start a dedicated
	// sandbox for the chat instead of sharing a full pool pod.
	SpillActionDedicatedSandbox = "dedicated_sandbox"
	// SpillActionRefuse is the fail-closed remediation: no spill, the lease fails.
	SpillActionRefuse = "refuse"
)

// PoolExhaustionSignal is the distinct "this pool is full" signal of shared mode.
// It is safe to surface to an operator and to log: counts, ports and lever names
// only.
type PoolExhaustionSignal struct {
	// Kind is PoolExhaustionKind.
	Kind string `json:"kind"`
	// Mode is the isolation mode the pool was full in.
	Mode string `json:"mode"`
	// Pool is the pool identity the signal was raised for (may be empty in a
	// single-pod deployment).
	Pool string `json:"pool,omitempty"`
	// Slots is how many workspace blocks the pool publishes.
	Slots int `json:"slots"`
	// ActiveWorkspaces is how many of them are in use.
	ActiveWorkspaces int `json:"active_workspaces"`
	// Block is the published span, for the operator to see the geometry.
	Block string `json:"block,omitempty"`
	// Spill is the remediation of the signal (see SpillAction*).
	Spill string `json:"spill"`
	// Message is the one-line operator-readable reason.
	Message string `json:"message"`
	// Remediation are the actionable next steps.
	Remediation []string `json:"remediation,omitempty"`
	// GeneratedAt is the signal time (RFC3339, UTC).
	GeneratedAt string `json:"generated_at"`
}

// NewPoolExhaustionSignal builds the signal of a full pool from its live counts.
func NewPoolExhaustionSignal(mode, pool string, slots, active int, block string, now time.Time) PoolExhaustionSignal {
	if slots < 0 {
		slots = 0
	}
	if active < 0 {
		active = 0
	}
	spill := ExhaustionSpillActionFromEnv()
	signal := PoolExhaustionSignal{
		Kind:             PoolExhaustionKind,
		Mode:             mode,
		Pool:             strings.TrimSpace(pool),
		Slots:            slots,
		ActiveWorkspaces: active,
		Block:            strings.TrimSpace(block),
		Spill:            spill,
		GeneratedAt:      now.UTC().Format(time.RFC3339),
	}
	signal.Message = fmt.Sprintf(
		"pool port-block exhaustion: all %d blocks are in use (%d live workspaces, span %s); this is a capacity condition, not a policy or geometry failure",
		slots, active, signal.Block)
	signal.Remediation = []string{
		fmt.Sprintf("release a lease, raise %s, or spill to another pool pod", EnvNetworkMaxWorkspaces),
		fmt.Sprintf("fall back to a dedicated sandbox: %s", SpillActionDedicatedSandbox),
		fmt.Sprintf("a caller that must not spill sets %s=%s and refuses the lease", EnvPoolExhaustionSpill, SpillActionRefuse),
	}
	return signal
}

// ExhaustionSpillActionFromEnv resolves the configured spill remediation.
func ExhaustionSpillActionFromEnv() string {
	switch strings.ToLower(strings.TrimSpace(getenvPoolExhaustionSpill())) {
	case SpillActionRefuse, "none", "never":
		return SpillActionRefuse
	case SpillActionDedicatedSandbox, "dedicated", "sandbox":
		return SpillActionDedicatedSandbox
	default:
		return SpillActionNewPoolPod
	}
}

// IsZero reports whether the signal is unset.
func (s PoolExhaustionSignal) IsZero() bool { return s.Kind == "" }

// Spills reports whether the signal wants the lease path to spill at all.
func (s PoolExhaustionSignal) Spills() bool {
	return s.Spill == SpillActionNewPoolPod || s.Spill == SpillActionDedicatedSandbox
}

// Err renders the signal as an error that still matches ErrPoolNetworkCapacity, so
// existing callers keep failing fast while a new caller can switch on the signal.
func (s PoolExhaustionSignal) Err() error {
	if s.IsZero() {
		return nil
	}
	return fmt.Errorf("%w: %s (kind=%s spill=%s)", ErrPoolNetworkCapacity, s.Message, s.Kind, s.Spill)
}

// Lines renders the signal as the CI/admin report block of ci#792 item 5.
func (s PoolExhaustionSignal) Lines() []string {
	if s.IsZero() {
		return []string{"sandbox_pool_exhaustion exhausted=false"}
	}
	lines := []string{
		fmt.Sprintf("sandbox_pool_exhaustion exhausted=true kind=%s mode=%s pool=%s slots=%d active=%d block=%s spill=%s",
			s.Kind, s.Mode, s.Pool, s.Slots, s.ActiveWorkspaces, s.Block, s.Spill),
		"sandbox_pool_exhaustion message=" + s.Message,
	}
	for _, step := range s.Remediation {
		lines = append(lines, "sandbox_pool_exhaustion remediation="+step)
	}
	return lines
}

// ExhaustionSignalFromStatus derives the signal from a rendered plan status. It
// reports false while the pool still has a free block, so an admin render never
// signals a condition that is not there.
func ExhaustionSignalFromStatus(status NetworkPlanStatus, now time.Time) (PoolExhaustionSignal, bool) {
	if !status.Exhausted {
		return PoolExhaustionSignal{}, false
	}
	block := ""
	if status.PublishedBlock != nil {
		block = status.PublishedBlock.String()
	}
	return NewPoolExhaustionSignal(status.Mode, "", status.Slots, status.ActiveWorkspaces, block, now), true
}

// AllocateOrSignal is the lease-path entry point: it allocates as usual but hands
// back the distinct exhaustion signal instead of the generic capacity error when the
// pool is full, so the caller can spill to a new pool pod or fall back to a dedicated
// sandbox. Every other failure keeps its own error.
func (a *PortAllocator) AllocateOrSignal(workspaceUUID, repo, scope string) (WorkspaceNetworkPlan, *PoolExhaustionSignal, error) {
	plan, err := a.Allocate(workspaceUUID, repo, scope)
	if err == nil {
		return plan, nil, nil
	}
	if !errors.Is(err, ErrPoolNetworkCapacity) {
		return WorkspaceNetworkPlan{}, nil, err
	}
	status := a.Status()
	signal, ok := ExhaustionSignalFromStatus(status, time.Now())
	if !ok {
		// The pool is not full any more (a lease was released in between), so the
		// generic error is the honest answer; never signal a stale condition.
		return WorkspaceNetworkPlan{}, nil, err
	}
	return WorkspaceNetworkPlan{}, &signal, signal.Err()
}

// getenvPoolExhaustionSpill is the single source of truth of the spill lever.
func getenvPoolExhaustionSpill() string { return os.Getenv(EnvPoolExhaustionSpill) }
