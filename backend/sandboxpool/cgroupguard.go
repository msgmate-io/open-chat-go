package sandboxpool

// ci#546 / ci#792 - per-workspace resource guard for the bounded shared mode.
//
// MaxWorkspaces (ci#546, default 8) bounds HOW MANY agents share one pool pod,
// not HOW MUCH one of them may consume. Without a second bound a single agent
// that compiles a large tree can starve every other workspace in the pod: the
// resource half of the shared-mode design (ci#662 memory-derived bound,
// extended to the shared case).
//
// This file is that bound as policy: it turns a configured pool budget into a
// per-workspace cgroup v2 slice (cpu.max / memory.max / pids.max) and injects
// the slice in the session environment, in the same "policy + validation first,
// provisioning later" shape as netisolation.go. Nothing here writes to cgroupfs;
// the guard refuses to hand a plan to a pool that cannot offer cgroup v2
// delegation, so the wiring only happens on a pool that can honour it.
//
// Agreement with the network bound. The two bounds must not disagree: whichever
// is smaller wins at the call site (EffectiveMaxWorkspaces), and an enabled guard
// whose per-workspace limits would oversubscribe the configured pool budget is
// refused (ErrInvalidCgroupPolicy) instead of silently overcommitting the pod.
//
// Failure modes (all fail closed, all named in the returned error):
//
//  1. cgroup v2 unavailable - an enabled guard returns
//     ErrWorkspaceCgroupUnavailable rather than running unbounded.
//  2. non-v2 hierarchy - cgroup v1 has no unified accounting; refused.
//  3. oversubscribed budget - per-workspace x MaxWorkspaces over the pool
//     budget: refused, naming both numbers.
//  4. degenerate limits - cpu/mem/pids below the MinCgroup* floors are refused.
//  5. slot exhaustion - more live workspaces than slices: CgroupExhaustedError,
//     so the lease path can spill to another pool pod or go dedicated.
//  6. unsafe slice root - a root that is not one plain path segment is refused,
//     so the derived slice path cannot escape cgroupfs.

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Deployment levers. All optional; with nothing set the bounded defaults of
// DefaultCgroupGuardPolicy apply (guard on, self-consistent budget).
const (
	// EnvCgroupGuard switches the guard on/off ("0"/"false"/"no"/"off" off).
	EnvCgroupGuard = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_GUARD"
	// EnvCgroupAvailable is what the pool reports after probing cgroup v2
	// delegation. "0" makes an enabled guard fail closed.
	EnvCgroupAvailable = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_AVAILABLE"
	// EnvCgroupCPUQuotaMillicores is the per-workspace cpu.max budget.
	EnvCgroupCPUQuotaMillicores = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_CPU_MILLICORES"
	// EnvCgroupMemoryMB is the per-workspace memory.max budget.
	EnvCgroupMemoryMB = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_MEMORY_MB"
	// EnvCgroupPidsMax is the per-workspace pids.max budget.
	EnvCgroupPidsMax = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_PIDS"
	// EnvCgroupMaxWorkspaces is the resource-side ceiling per pool pod.
	EnvCgroupMaxWorkspaces = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_MAX_WORKSPACES"
	// EnvCgroupPoolCPUQuotaMillicores is the pod budget the slices are carved from.
	EnvCgroupPoolCPUQuotaMillicores = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_POOL_CPU_MILLICORES"
	// EnvCgroupPoolMemoryMB is the pod memory budget.
	EnvCgroupPoolMemoryMB = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_POOL_MEMORY_MB"
	// EnvCgroupSliceRoot is the parent slice every workspace slice hangs under.
	EnvCgroupSliceRoot = "OCI_MSGMATE_CODER_SANDBOX_CGROUP_SLICE_ROOT"
)

// Fixed vocabulary and floors.
const (
	// CgroupVersionV2 is the only hierarchy with unified accounting.
	CgroupVersionV2 = "v2"
	// CgroupRootPath is the cgroup v2 mount point.
	CgroupRootPath = "/sys/fs/cgroup"
	// CgroupSliceSuffix ends every workspace slice.
	CgroupSliceSuffix = ".slice"
	// DefaultCgroupSliceRoot is the parent slice name in the pool pod.
	DefaultCgroupSliceRoot = "open-chat-pool"
	// CgroupExhaustedSignal is the machine-readable name of a full guard.
	CgroupExhaustedSignal = "pool_cgroup_exhausted"
	// EnvCgroupWorkspaceSlice is how the slice reaches the session.
	EnvCgroupWorkspaceSlice = "CODER_CGROUP_SLICE"
	// EnvCgroupWorkspaceCPUMillicores is how cpu.max reaches the session.
	EnvCgroupWorkspaceCPUMillicores = "CODER_CGROUP_CPU_MILLICORES"
	// EnvCgroupWorkspaceMemoryMaxBytes is how memory.max reaches the session.
	EnvCgroupWorkspaceMemoryMaxBytes = "CODER_CGROUP_MEMORY_MAX_BYTES"
	// EnvCgroupWorkspacePidsMax is how pids.max reaches the session.
	EnvCgroupWorkspacePidsMax = "CODER_CGROUP_PIDS_MAX"
	// DefaultCgroupCPUQuotaMillicores is half a core per workspace.
	DefaultCgroupCPUQuotaMillicores = 500
	// DefaultCgroupMemoryMB is 2 GiB per workspace.
	DefaultCgroupMemoryMB = 2048
	// DefaultCgroupPidsMax bounds the process/thread count per workspace.
	DefaultCgroupPidsMax = 512
	// DefaultCgroupMaxWorkspaces is the ci#546 ceiling unless configured lower.
	DefaultCgroupMaxWorkspaces = DefaultNetworkMaxWorkspaces
	// DefaultCgroupPoolCPUQuotaMillicores is the self-consistent pod budget
	// (per-workspace x ceiling); a deployment that raises the per-workspace
	// limit must raise this with it.
	DefaultCgroupPoolCPUQuotaMillicores = DefaultCgroupCPUQuotaMillicores * DefaultCgroupMaxWorkspaces
	// DefaultCgroupPoolMemoryMB is the same, for memory.
	DefaultCgroupPoolMemoryMB = DefaultCgroupMemoryMB * DefaultCgroupMaxWorkspaces
	// MinCgroupCPUQuotaMillicores keeps a slice schedulable.
	MinCgroupCPUQuotaMillicores = 100
	// MinCgroupMemoryMB keeps a slice from OOMing on start-up.
	MinCgroupMemoryMB = 128
	// MinCgroupPidsMax keeps a slice able to run a shell and a compiler.
	MinCgroupPidsMax = 32
	// bytesPerMB converts the human budget into cgroup bytes.
	bytesPerMB = 1 << 20
)

// Resource-guard sentinels; match with errors.Is.
var (
	// ErrWorkspaceCgroupUnavailable means the guard is enabled but the pool
	// cannot offer cgroup v2 delegation. Never a silent downgrade.
	ErrWorkspaceCgroupUnavailable = errors.New("sandbox pool resources: per-workspace cgroup v2 delegation is not available on this pool")
	// ErrInvalidCgroupPolicy means the resolved guard policy cannot be applied.
	ErrInvalidCgroupPolicy = errors.New("sandbox pool resources: invalid cgroup guard policy")
	// ErrCgroupCapacity means every slice of this pool pod is taken.
	ErrCgroupCapacity = errors.New("sandbox pool resources: no free cgroup slice for another workspace")
)

// IsPoolResourceFailure reports whether err came from this guard.
func IsPoolResourceFailure(err error) bool {
	return errors.Is(err, ErrWorkspaceCgroupUnavailable) ||
		errors.Is(err, ErrInvalidCgroupPolicy) ||
		errors.Is(err, ErrCgroupCapacity)
}

// CgroupGuardPolicy is the bounded resource policy of one pool pod. The guard is
// on by default - the bounded default is the point - and the opt-out is explicit
// and recorded (EnvCgroupGuard=0), exactly like the shared network mode.
type CgroupGuardPolicy struct {
	// Enabled turns the whole guard on/off.
	Enabled bool
	// Available is the pool's probe result for cgroup v2 delegation.
	Available bool
	// Version is the cgroup hierarchy; only CgroupVersionV2 is accepted.
	Version string
	// SliceRoot is the parent slice all workspace slices hang under.
	SliceRoot string
	// CPUQuotaMillicores is the per-workspace cpu budget.
	CPUQuotaMillicores int
	// MemoryMB is the per-workspace memory budget.
	MemoryMB int
	// PidsMax is the per-workspace process/thread budget.
	PidsMax int
	// MaxWorkspaces is the resource-side ceiling per pool pod.
	MaxWorkspaces int
	// PoolCPUQuotaMillicores is the pod cpu budget (0 = unbounded, refused).
	PoolCPUQuotaMillicores int
	// PoolMemoryMB is the pod memory budget (0 = unbounded, refused).
	PoolMemoryMB int
}

// DefaultCgroupGuardPolicy returns the bounded default: on, cgroup v2, half a
// core and 2 GiB per workspace, at most 8 workspaces, carved from a pod budget
// that is exactly per-workspace x ceiling.
func DefaultCgroupGuardPolicy() CgroupGuardPolicy {
	return CgroupGuardPolicy{
		Enabled:                true,
		Available:              true,
		Version:                CgroupVersionV2,
		SliceRoot:              DefaultCgroupSliceRoot,
		CPUQuotaMillicores:     DefaultCgroupCPUQuotaMillicores,
		MemoryMB:               DefaultCgroupMemoryMB,
		PidsMax:                DefaultCgroupPidsMax,
		MaxWorkspaces:          DefaultCgroupMaxWorkspaces,
		PoolCPUQuotaMillicores: DefaultCgroupPoolCPUQuotaMillicores,
		PoolMemoryMB:           DefaultCgroupPoolMemoryMB,
	}
}

// CgroupGuardPolicyFromEnv resolves the policy from the deployment levers on top
// of DefaultCgroupGuardPolicy. An invalid count or boolean keeps the default and
// is then caught by Validate, so a typo can never widen a bound.
func CgroupGuardPolicyFromEnv() CgroupGuardPolicy {
	p := DefaultCgroupGuardPolicy()
	p.Enabled = cgroupEnvBool(EnvCgroupGuard, p.Enabled)
	p.Available = cgroupEnvBool(EnvCgroupAvailable, p.Available)
	if v, ok := envPositiveInt(EnvCgroupCPUQuotaMillicores); ok {
		p.CPUQuotaMillicores = v
	}
	if v, ok := envPositiveInt(EnvCgroupMemoryMB); ok {
		p.MemoryMB = v
	}
	if v, ok := envPositiveInt(EnvCgroupPidsMax); ok {
		p.PidsMax = v
	}
	if v, ok := envPositiveInt(EnvCgroupMaxWorkspaces); ok {
		p.MaxWorkspaces = v
	}
	if v, ok := envPositiveInt(EnvCgroupPoolCPUQuotaMillicores); ok {
		p.PoolCPUQuotaMillicores = v
	}
	if v, ok := envPositiveInt(EnvCgroupPoolMemoryMB); ok {
		p.PoolMemoryMB = v
	}
	if raw := strings.TrimSpace(os.Getenv(EnvCgroupSliceRoot)); raw != "" {
		p.SliceRoot = raw
	}
	return p
}

// Validate applies the guard's floors and the budget agreement. A disabled guard
// is always valid: it is the explicit, documented escape from the bound.
func (p CgroupGuardPolicy) Validate() error {
	if !p.Enabled {
		return nil
	}
	if !p.Available {
		return fmt.Errorf("%w (mode %s, slice root %q)", ErrWorkspaceCgroupUnavailable, p.Version, p.SliceRoot)
	}
	if p.Version != CgroupVersionV2 {
		return fmt.Errorf("%w: cgroup version %q is not supported, need %q", ErrInvalidCgroupPolicy, p.Version, CgroupVersionV2)
	}
	if err := validateSliceRoot(p.SliceRoot); err != nil {
		return err
	}
	if p.CPUQuotaMillicores < MinCgroupCPUQuotaMillicores {
		return fmt.Errorf("%w: cpu %dm per workspace is below the %dm floor", ErrInvalidCgroupPolicy, p.CPUQuotaMillicores, MinCgroupCPUQuotaMillicores)
	}
	if p.MemoryMB < MinCgroupMemoryMB {
		return fmt.Errorf("%w: memory %dMB per workspace is below the %dMB floor", ErrInvalidCgroupPolicy, p.MemoryMB, MinCgroupMemoryMB)
	}
	if p.PidsMax < MinCgroupPidsMax {
		return fmt.Errorf("%w: pids %d per workspace is below the %d floor", ErrInvalidCgroupPolicy, p.PidsMax, MinCgroupPidsMax)
	}
	if p.MaxWorkspaces < 1 {
		return fmt.Errorf("%w: max workspaces %d must be at least 1", ErrInvalidCgroupPolicy, p.MaxWorkspaces)
	}
	if p.PoolCPUQuotaMillicores < 1 || p.PoolMemoryMB < 1 {
		return fmt.Errorf("%w: the pod budget must be configured (cpu %dm, memory %dMB)", ErrInvalidCgroupPolicy, p.PoolCPUQuotaMillicores, p.PoolMemoryMB)
	}
	if want := p.CPUQuotaMillicores * p.MaxWorkspaces; want > p.PoolCPUQuotaMillicores {
		return fmt.Errorf("%w: %dm x %d workspaces = %dm over the %dm pod cpu budget", ErrInvalidCgroupPolicy, p.CPUQuotaMillicores, p.MaxWorkspaces, want, p.PoolCPUQuotaMillicores)
	}
	if want := p.MemoryMB * p.MaxWorkspaces; want > p.PoolMemoryMB {
		return fmt.Errorf("%w: %dMB x %d workspaces = %dMB over the %dMB pod memory budget", ErrInvalidCgroupPolicy, p.MemoryMB, p.MaxWorkspaces, want, p.PoolMemoryMB)
	}
	return nil
}

// validateSliceRoot keeps the derived slice path inside cgroupfs: the root must
// be one plain path segment.
func validateSliceRoot(root string) error {
	if root == "" {
		return fmt.Errorf("%w: slice root is empty", ErrInvalidCgroupPolicy)
	}
	if strings.ContainsAny(root, "/\\") || root == "." || root == ".." || strings.Contains(root, "..") {
		return fmt.Errorf("%w: slice root %q must be a single plain path segment", ErrInvalidCgroupPolicy, root)
	}
	return nil
}

// SliceName is the systemd-style unit name of the workspace slice.
func (p CgroupGuardPolicy) SliceName(workspaceUUID string) string {
	return p.SliceRoot + "-workspace-" + workspaceUUID + CgroupSliceSuffix
}

// SlicePath is the cgroupfs path of the workspace slice.
func (p CgroupGuardPolicy) SlicePath(workspaceUUID string) string {
	return CgroupRootPath + "/" + p.SliceName(workspaceUUID)
}

// EffectiveMaxWorkspaces is the number of agents one pool pod may host: the
// smaller of the ci#546 network bound and the ci#792 resource bound, as the
// network design requires ("whichever is smaller wins at the call site"). A
// disabled or unset guard leaves the network bound alone.
func EffectiveMaxWorkspaces(network NetworkPolicy, guard CgroupGuardPolicy) int {
	bound := network.MaxWorkspaces
	if guard.Enabled && guard.MaxWorkspaces > 0 && (bound < 1 || guard.MaxWorkspaces < bound) {
		bound = guard.MaxWorkspaces
	}
	if bound < 1 {
		return 0
	}
	return bound
}

// CgroupExhaustedError is the distinct "this guard is full" signal, so a lease
// path reads a name instead of matching a sentinel or parsing a message. It
// wraps ErrCgroupCapacity and shares the ci#546 remedy vocabulary.
type CgroupExhaustedError struct {
	// WorkspaceUUID is the workspace that could not be placed.
	WorkspaceUUID string
	// Slots is the configured number of workspaces.
	Slots int
	// ActiveWorkspaces is how many slices are in use.
	ActiveWorkspaces int
	// SliceRoot names the exhausted hierarchy.
	SliceRoot string
}

func (e *CgroupExhaustedError) Error() string {
	return fmt.Sprintf("%v: %s (%d/%d slices in use under %s)", ErrCgroupCapacity, e.WorkspaceUUID, e.ActiveWorkspaces, e.Slots, e.SliceRoot)
}

// Unwrap keeps ErrCgroupCapacity the sentinel errors.Is matches.
func (e *CgroupExhaustedError) Unwrap() error { return ErrCgroupCapacity }

// Signal is the stable condition name for status views and metrics.
func (e *CgroupExhaustedError) Signal() string { return CgroupExhaustedSignal }

// Remediations is the ordered way out of a full guard: spill to another pool pod,
// then give the workspace a dedicated sandbox (and, if the bound itself is the
// problem, raise the configured ceiling).
func (e *CgroupExhaustedError) Remediations() []string {
	return []string{RemediationSpillPoolPod, RemediationDedicatedSandbox, "raise " + EnvCgroupMaxWorkspaces}
}

// AsCgroupExhausted extracts the exhaustion signal from err.
func AsCgroupExhausted(err error) (*CgroupExhaustedError, bool) {
	var target *CgroupExhaustedError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// IsCgroupExhausted reports whether err means "this guard is full". A bare
// ErrCgroupCapacity counts too: it is the pre-signal spelling of the same state.
func IsCgroupExhausted(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := AsCgroupExhausted(err); ok {
		return true
	}
	return errors.Is(err, ErrCgroupCapacity)
}

// WorkspaceCgroupPlan is one workspace's slice and limits.
type WorkspaceCgroupPlan struct {
	// WorkspaceUUID is the workspace the slice belongs to.
	WorkspaceUUID string
	// SlicePath is the cgroupfs path of the slice.
	SlicePath string
	// CPUQuotaMillicores is the workspace cpu budget.
	CPUQuotaMillicores int
	// MemoryMaxBytes is the workspace memory budget in bytes.
	MemoryMaxBytes int64
	// PidsMax is the workspace process/thread budget.
	PidsMax int
	// MaxWorkspaces is the pool ceiling the plan was carved from.
	MaxWorkspaces int
}

// Env is what is injected into the session, so a container-aware tool can write
// or read its own slice instead of guessing.
func (p WorkspaceCgroupPlan) Env() map[string]string {
	return map[string]string{
		EnvCgroupWorkspaceSlice:          p.SlicePath,
		EnvCgroupWorkspaceCPUMillicores:  strconv.Itoa(p.CPUQuotaMillicores),
		EnvCgroupWorkspaceMemoryMaxBytes: strconv.FormatInt(p.MemoryMaxBytes, 10),
		EnvCgroupWorkspacePidsMax:        strconv.Itoa(p.PidsMax),
	}
}

// Describe renders the plan as one line.
func (p WorkspaceCgroupPlan) Describe() string {
	return fmt.Sprintf("cgroup %s: %dm cpu, %dMB mem, %d pids (%s)", p.WorkspaceUUID, p.CPUQuotaMillicores, p.MemoryMaxBytes/bytesPerMB, p.PidsMax, p.SlicePath)
}

// ReportLines renders the plan for a human report, env names only.
func (p WorkspaceCgroupPlan) ReportLines() []string {
	return []string{
		p.Describe(),
		"injected: " + sortedEnvNames(p.Env()),
	}
}

// CgroupGuard tracks the live slices of one pool pod. It mirrors PortAllocator:
// process-local, addressed by workspace UUID, idempotent for a repeated apply and
// never writing to cgroupfs itself.
type CgroupGuard struct {
	mu     sync.Mutex
	policy CgroupGuardPolicy
	active map[string]WorkspaceCgroupPlan
}

// NewCgroupGuard validates the policy and returns a guard for it. An enabled but
// unusable policy fails closed here, before any workspace is provisioned.
func NewCgroupGuard(policy CgroupGuardPolicy) (*CgroupGuard, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &CgroupGuard{policy: policy, active: map[string]WorkspaceCgroupPlan{}}, nil
}

// Policy returns the resolved policy.
func (g *CgroupGuard) Policy() CgroupGuardPolicy { return g.policy }

// Enabled reports whether the guard bounds anything.
func (g *CgroupGuard) Enabled() bool { return g.policy.Enabled }

// Slots is how many workspaces fit, 0 when the guard is off.
func (g *CgroupGuard) Slots() int {
	if !g.policy.Enabled {
		return 0
	}
	return g.policy.MaxWorkspaces
}

// ActiveWorkspaces lists the workspaces holding a slice, sorted.
func (g *CgroupGuard) ActiveWorkspaces() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	uuids := make([]string, 0, len(g.active))
	for uuid := range g.active {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)
	return uuids
}

// Apply returns the workspace's slice plan, carving a slot on first call and
// replaying the same plan afterwards (a pool restart re-derives, it does not
// double-account). With the guard disabled it returns a zero plan and no error:
// the workspace simply runs unbounded, which is the explicit opt-out.
func (g *CgroupGuard) Apply(workspaceUUID string) (WorkspaceCgroupPlan, error) {
	if !g.policy.Enabled {
		return WorkspaceCgroupPlan{}, nil
	}
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return WorkspaceCgroupPlan{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if plan, ok := g.active[uuid]; ok {
		return plan, nil
	}
	if len(g.active) >= g.policy.MaxWorkspaces {
		return WorkspaceCgroupPlan{}, &CgroupExhaustedError{
			WorkspaceUUID:    uuid,
			Slots:            g.policy.MaxWorkspaces,
			ActiveWorkspaces: len(g.active),
			SliceRoot:        g.policy.SliceRoot,
		}
	}
	plan := WorkspaceCgroupPlan{
		WorkspaceUUID:      uuid,
		SlicePath:          g.policy.SlicePath(uuid),
		CPUQuotaMillicores: g.policy.CPUQuotaMillicores,
		MemoryMaxBytes:     int64(g.policy.MemoryMB) * bytesPerMB,
		PidsMax:            g.policy.PidsMax,
		MaxWorkspaces:      g.policy.MaxWorkspaces,
	}
	g.active[uuid] = plan
	return plan, nil
}

// Release frees the workspace's slice; it reports whether a slice was held.
func (g *CgroupGuard) Release(workspaceUUID string) bool {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.active[uuid]; !ok {
		return false
	}
	delete(g.active, uuid)
	return true
}

// Plan returns the workspace's slice without carving one.
func (g *CgroupGuard) Plan(workspaceUUID string) (WorkspaceCgroupPlan, bool) {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return WorkspaceCgroupPlan{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	plan, ok := g.active[uuid]
	return plan, ok
}

// ReportLines renders the guard for a human report.
func (g *CgroupGuard) ReportLines() []string {
	p := g.policy
	lines := []string{
		fmt.Sprintf("cgroup guard: enabled=%t version=%s slice root=%s", p.Enabled, p.Version, p.SliceRoot),
		fmt.Sprintf("limits per workspace: %dm cpu, %dMB mem, %d pids", p.CPUQuotaMillicores, p.MemoryMB, p.PidsMax),
		fmt.Sprintf("pod budget: %dm cpu, %dMB mem -> ceiling %d workspaces", p.PoolCPUQuotaMillicores, p.PoolMemoryMB, EffectiveMaxWorkspaces(NetworkPolicy{MaxWorkspaces: p.MaxWorkspaces}, p)),
	}
	if !p.Enabled {
		lines = append(lines, "guard disabled by "+EnvCgroupGuard+": workspaces run unbounded (explicit opt-out)")
	} else if !p.Available {
		lines = append(lines, "cgroup v2 unavailable on this pool -> lease refused ("+ErrWorkspaceCgroupUnavailable.Error()+")")
	}
	active := g.ActiveWorkspaces()
	if len(active) == 0 {
		return append(lines, "no slice in use")
	}
	lines = append(lines, "slices in use: "+strconv.Itoa(len(active))+" ("+strings.Join(active, ", ")+")")
	for _, uuid := range active {
		if plan, ok := g.Plan(uuid); ok {
			lines = append(lines, "  "+plan.Describe())
		}
	}
	return lines
}

// The process-wide guard, mirroring the ci#613 DefaultGate seam, so the admin
// surface can report the live resource state of the pool.
var (
	defaultCgroupGuardMu sync.RWMutex
	defaultCgroupGuard   *CgroupGuard
)

// DefaultCgroupGuard returns the process-wide guard, or nil when none is set.
func DefaultCgroupGuard() *CgroupGuard {
	defaultCgroupGuardMu.RLock()
	defer defaultCgroupGuardMu.RUnlock()
	return defaultCgroupGuard
}

// SetDefaultCgroupGuard installs the process-wide guard (nil clears it).
func SetDefaultCgroupGuard(guard *CgroupGuard) {
	defaultCgroupGuardMu.Lock()
	defer defaultCgroupGuardMu.Unlock()
	defaultCgroupGuard = guard
}

// cgroupEnvBool reads a boolean lever, keeping def for unset/invalid values so a
// typo can never widen a bound.
func cgroupEnvBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
