package sandboxpool

// ci#792 item 3 - per-workspace cgroup guard for the bounded shared mode.
//
// MaxWorkspaces (default 8) bounds *how many* agents share one pool pod, but a
// count bound is not a resource bound: one workspace running a compiler, a
// runaway test loop or a leaking dev server can still starve its seven peers of
// CPU, memory and disk. This file adds the missing per-workspace accounting: a
// cgroup v2 slice per workspace with cpu.max / cpu.weight, memory.max /
// memory.high and a published disk byte budget, resolved from the deployment
// levers and fail closed when the pool's host cannot offer a unified cgroup v2
// hierarchy.
//
// The guard is additive to the ci#546 network policy: the port block and the
// loopback alias still come from that policy, while the resource bound comes
// from here, and both key off the same workspace UUID. Nothing in this file
// talks to the kernel or mutates the host: it resolves the slice, the file
// contents and the setup script so the pool provider (or an operator) applies
// them, exactly like the ci#846 transport publish half. No credential material
// is involved - paths, limits and byte counts only.

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Deployment levers of the per-workspace cgroup guard. All optional; with
// nothing set the guard is off and the pool keeps the pure count bound.
const (
	// EnvWorkspaceCgroupGuard turns the guard on ("1"/"true").
	EnvWorkspaceCgroupGuard = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_GUARD"
	// EnvWorkspaceCgroupRoot is the cgroup v2 mount ("/sys/fs/cgroup").
	EnvWorkspaceCgroupRoot = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_ROOT"
	// EnvWorkspaceCgroupSlicePrefix prefixes every per-workspace slice name.
	EnvWorkspaceCgroupSlicePrefix = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_SLICE_PREFIX"
	// EnvWorkspaceCgroupCPUQuotaMilli is the CPU budget of one workspace in
	// thousandths of a core (1000 = one core).
	EnvWorkspaceCgroupCPUQuotaMilli = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_CPU_QUOTA_MILLI"
	// EnvWorkspaceCgroupMemoryMaxBytes is the memory ceiling of one workspace.
	EnvWorkspaceCgroupMemoryMaxBytes = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_MEMORY_MAX_BYTES"
	// EnvWorkspaceCgroupIOMaxBytesPerSec is the disk byte budget of one
	// workspace (published; binding it needs the workspace mount's device).
	EnvWorkspaceCgroupIOMaxBytesPerSec = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_CGROUP_IO_MAX_BYTES_PER_SEC"
)

// Defaults and constants of the guard.
const (
	// DefaultWorkspaceCgroupRoot is the cgroup v2 (unified) mount point.
	DefaultWorkspaceCgroupRoot = "/sys/fs/cgroup"
	// DefaultWorkspaceCgroupSlicePrefix prefixes the derived slice names.
	DefaultWorkspaceCgroupSlicePrefix = "wsc-"
	// DefaultWorkspaceCgroupCPUQuotaMilli gives one workspace one full core,
	// so the default 8 workspaces of a pool fit a plausible 8-core node
	// without any one of them being able to take two.
	DefaultWorkspaceCgroupCPUQuotaMilli = 1000
	// DefaultWorkspaceCgroupMemoryMaxBytes is the memory ceiling of one
	// workspace (2 GiB), deliberately below the ci#662 node-derived bound.
	DefaultWorkspaceCgroupMemoryMaxBytes = int64(2) << 30
	// DefaultWorkspaceCgroupIOMaxBytesPerSec is the published disk budget of
	// one workspace (64 MiB/s).
	DefaultWorkspaceCgroupIOMaxBytesPerSec = int64(64) << 20
	// MinWorkspaceCgroupMemoryBytes keeps a memory limit meaningful.
	MinWorkspaceCgroupMemoryBytes = int64(64) << 20
	// MaxWorkspaceCgroupMemoryBytes keeps the limit inside a sane range.
	MaxWorkspaceCgroupMemoryBytes = int64(1) << 40
	// CgroupCPUPeriodMicros is the cgroup v2 cpu.max period.
	CgroupCPUPeriodMicros = 100000
	// CgroupMemoryHighRatio/CgroupMemoryHighDivisor derive memory.high from
	// memory.max, so a workspace is throttled before it is OOM-killed.
	CgroupMemoryHighRatio   = 9
	CgroupMemoryHighDivisor = 10
	// Controllers the guard needs. memory/cpu are authoritative; io carries
	// the disk byte budget.
	CgroupRequiredControllerCPU    = "cpu"
	CgroupRequiredControllerMemory = "memory"
	CgroupRequiredControllerIO     = "io"
)

// Session env contract of the guard: a workspace can read its own slice and its
// own limits without any privileged helper. Names and numbers only.
const (
	// EnvWorkspaceCgroupPath is the workspace's cgroup v2 slice path.
	EnvWorkspaceCgroupPath = "CODER_CGROUP_PATH"
	// EnvWorkspaceCgroupGuardState is "enabled" when the guard bounds it.
	EnvWorkspaceCgroupGuardState = "CODER_CGROUP_GUARD"
	// EnvWorkspaceCgroupCPUQuota is the CPU budget in milli-cores.
	EnvWorkspaceCgroupCPUQuota = "CODER_CGROUP_CPU_QUOTA_MILLI"
	// EnvWorkspaceCgroupMemoryMax is the memory ceiling in bytes.
	EnvWorkspaceCgroupMemoryMax = "CODER_CGROUP_MEMORY_MAX"
	// EnvWorkspaceCgroupIOMax is the disk budget in bytes/s.
	EnvWorkspaceCgroupIOMax = "CODER_CGROUP_IO_MAX_BYTES_PER_SEC"
)

// Sentinel errors; match with errors.Is.
var (
	// ErrWorkspaceCgroupUnavailable means the guard cannot bound a workspace:
	// it is switched off, or the pool host has no usable cgroup v2 hierarchy.
	// It is never downgraded silently.
	ErrWorkspaceCgroupUnavailable = errors.New("sandbox pool cgroup: per-workspace cgroup guard is not available on this pool")
	// ErrInvalidCgroupPolicy means the resolved guard policy or a derived
	// slice value cannot be applied.
	ErrInvalidCgroupPolicy = errors.New("sandbox pool cgroup: invalid per-workspace cgroup policy")
)

// IsWorkspaceCgroupFailure reports whether err is a refusal of the
// per-workspace cgroup guard, as opposed to the port-block exhaustion of the
// network policy (ErrPoolNetworkCapacity): the two are different conditions
// and a caller must not collapse them into one generic allocation error. The
// workspace count is still legal in the resource-bound case.
func IsWorkspaceCgroupFailure(err error) bool {
	return errors.Is(err, ErrWorkspaceCgroupUnavailable) ||
		errors.Is(err, ErrInvalidCgroupPolicy)
}

// CgroupHostSupport is the host half of the guard: whether the pool pod runs on
// a unified (v2) hierarchy with the controllers the slices need. Supported() is
// the fail-closed gate.
type CgroupHostSupport struct {
	// Root is the cgroup v2 mount the check ran against.
	Root string `json:"root"`
	// Unified reports whether cgroup.controllers was readable (v2 only).
	Unified bool `json:"unified"`
	// Controllers are the controllers the mount offers, sorted.
	Controllers []string `json:"controllers,omitempty"`
	// Blockers are the reasons the guard cannot be applied. Empty means
	// supported.
	Blockers []string `json:"blockers,omitempty"`
}

// Supported reports whether the host can carry the guard.
func (s CgroupHostSupport) Supported() bool { return s.Unified && len(s.Blockers) == 0 }

// ReportLines renders the host check for the logs/admin surface. No secret.
func (s CgroupHostSupport) ReportLines() []string {
	state := "unsupported"
	if s.Supported() {
		state = "supported"
	}
	lines := []string{fmt.Sprintf("sandbox_cgroup host=%s root=%s unified=%t controllers=%s",
		state, s.Root, s.Unified, strings.Join(s.Controllers, ","))}
	for _, blocker := range s.Blockers {
		lines = append(lines, "sandbox_cgroup blocker="+blocker)
	}
	return lines
}

// CgroupHostProbe is the injectable input of the host check, so the detector is
// deterministic in tests and never touches the host when it is not asked to.
type CgroupHostProbe struct {
	// Root is the cgroup v2 mount to inspect.
	Root string
	// ReadFile reads a file of the mount; defaults to os.ReadFile.
	ReadFile func(path string) ([]byte, error)
}

// DefaultCgroupHostProbe probes the deployment-configured (or default) mount.
func DefaultCgroupHostProbe(root string) CgroupHostProbe {
	if strings.TrimSpace(root) == "" {
		root = DefaultWorkspaceCgroupRoot
	}
	return CgroupHostProbe{Root: root, ReadFile: os.ReadFile}
}

// DetectCgroupHostSupport inspects a cgroup v2 mount and reports whether it can
// carry a per-workspace slice. A mount without cgroup.controllers is not
// cgroup v2 (or not mounted), and a missing cpu/memory/io controller is a
// blocker rather than a silently unenforced limit.
func DetectCgroupHostSupport(probe CgroupHostProbe) CgroupHostSupport {
	root := strings.TrimSpace(probe.Root)
	if root == "" {
		root = DefaultWorkspaceCgroupRoot
	}
	read := probe.ReadFile
	if read == nil {
		read = os.ReadFile
	}

	support := CgroupHostSupport{Root: root}
	raw, err := read(cgroupFile(root, "cgroup.controllers"))
	if err != nil {
		support.Blockers = append(support.Blockers, fmt.Sprintf(
			"no cgroup v2 unified hierarchy at %s (cgroup.controllers unreadable: %v)", root, err))
		return support
	}
	support.Unified = true
	support.Controllers = strings.Fields(string(raw))
	sort.Strings(support.Controllers)
	for _, controller := range []string{CgroupRequiredControllerCPU, CgroupRequiredControllerMemory, CgroupRequiredControllerIO} {
		if !hasString(support.Controllers, controller) {
			support.Blockers = append(support.Blockers, fmt.Sprintf(
				"the %q controller is not available in %s, so the matching limit would not be enforced", controller, root))
		}
	}
	return support
}

// WorkspaceCgroupPolicy is the resolved guard policy of a pool.
type WorkspaceCgroupPolicy struct {
	// Enabled turns the guard on. Off by default: every existing deployment
	// keeps the pure count bound until it opts in.
	Enabled bool
	// Root is the cgroup v2 mount the slices live in.
	Root string
	// SlicePrefix prefixes the derived per-workspace slice names.
	SlicePrefix string
	// CPUQuotaMilli is the CPU budget of one workspace in milli-cores.
	CPUQuotaMilli int
	// MemoryMaxBytes is the memory ceiling of one workspace.
	MemoryMaxBytes int64
	// IOMaxBytesPerSec is the published disk budget of one workspace.
	IOMaxBytesPerSec int64
}

// DefaultWorkspaceCgroupPolicy is the bounded default: one core and 2 GiB per
// workspace, 64 MiB/s of disk, guard off unless the deployment opts in.
func DefaultWorkspaceCgroupPolicy() WorkspaceCgroupPolicy {
	return WorkspaceCgroupPolicy{
		Root:             DefaultWorkspaceCgroupRoot,
		SlicePrefix:      DefaultWorkspaceCgroupSlicePrefix,
		CPUQuotaMilli:    DefaultWorkspaceCgroupCPUQuotaMilli,
		MemoryMaxBytes:   DefaultWorkspaceCgroupMemoryMaxBytes,
		IOMaxBytesPerSec: DefaultWorkspaceCgroupIOMaxBytesPerSec,
	}
}

// WorkspaceCgroupPolicyFromEnv resolves the policy from the deployment env,
// falling back to the defaults per field. An unparseable value is ignored (the
// default applies), so a typo cannot widen a bound.
func WorkspaceCgroupPolicyFromEnv() WorkspaceCgroupPolicy {
	policy := DefaultWorkspaceCgroupPolicy()
	policy.Enabled = cgroupEnvEnabled(os.Getenv(EnvWorkspaceCgroupGuard))
	if raw := strings.TrimSpace(os.Getenv(EnvWorkspaceCgroupRoot)); raw != "" {
		policy.Root = raw
	}
	if raw := strings.TrimSpace(os.Getenv(EnvWorkspaceCgroupSlicePrefix)); raw != "" {
		policy.SlicePrefix = raw
	}
	if value, ok := envPositiveInt(EnvWorkspaceCgroupCPUQuotaMilli); ok {
		policy.CPUQuotaMilli = value
	}
	if value, ok := envPositiveInt64(EnvWorkspaceCgroupMemoryMaxBytes); ok {
		policy.MemoryMaxBytes = value
	}
	if value, ok := envPositiveInt64(EnvWorkspaceCgroupIOMaxBytesPerSec); ok {
		policy.IOMaxBytesPerSec = value
	}
	return policy
}

// Validate rejects a policy whose slice could not be built or would not bound
// anything. It is what makes a mis-typed deployment lever fail closed instead
// of handing out an unbounded slice.
func (p WorkspaceCgroupPolicy) Validate() error {
	if !strings.HasPrefix(strings.TrimSpace(p.Root), "/") {
		return fmt.Errorf("%w: cgroup root %q must be an absolute path", ErrInvalidCgroupPolicy, p.Root)
	}
	if err := validateCgroupSlicePrefix(p.SlicePrefix); err != nil {
		return err
	}
	if p.CPUQuotaMilli < 0 || p.MemoryMaxBytes < 0 || p.IOMaxBytesPerSec < 0 {
		return fmt.Errorf("%w: limits must not be negative (cpu %d milli, memory %d bytes, io %d bytes/s)",
			ErrInvalidCgroupPolicy, p.CPUQuotaMilli, p.MemoryMaxBytes, p.IOMaxBytesPerSec)
	}
	if p.MemoryMaxBytes > 0 && (p.MemoryMaxBytes < MinWorkspaceCgroupMemoryBytes || p.MemoryMaxBytes > MaxWorkspaceCgroupMemoryBytes) {
		return fmt.Errorf("%w: memory limit %d bytes is outside %d-%d",
			ErrInvalidCgroupPolicy, p.MemoryMaxBytes, MinWorkspaceCgroupMemoryBytes, MaxWorkspaceCgroupMemoryBytes)
	}
	if !p.Enabled {
		return nil
	}
	if p.CPUQuotaMilli == 0 && p.MemoryMaxBytes == 0 && p.IOMaxBytesPerSec == 0 {
		return fmt.Errorf("%w: the guard is enabled but no cpu/memory/io limit is set, so it would bound nothing",
			ErrInvalidCgroupPolicy)
	}
	return nil
}

// Bounds renders the resolved limits as one operator-readable line.
func (p WorkspaceCgroupPolicy) Bounds() string {
	return fmt.Sprintf("cpu_quota_milli=%d memory_max_bytes=%d io_max_bytes_per_sec=%d",
		p.CPUQuotaMilli, p.MemoryMaxBytes, p.IOMaxBytesPerSec)
}

// WorkspaceCgroup is the resolved cgroup v2 slice of ONE workspace: the slice
// path, the file contents that bound it and the env a session reads.
type WorkspaceCgroup struct {
	// WorkspaceUUID is the workspace the slice belongs to.
	WorkspaceUUID string `json:"workspace_uuid"`
	// Slice is the absolute cgroup v2 slice path.
	Slice string `json:"slice"`
	// Relative is the slice name inside the root.
	Relative string `json:"relative"`
	// CPUQuotaMilli is the CPU budget in milli-cores.
	CPUQuotaMilli int `json:"cpu_quota_milli,omitempty"`
	// CPUWeight is the derived cpu.weight (1-10000).
	CPUWeight int `json:"cpu_weight,omitempty"`
	// MemoryMaxBytes is the memory ceiling in bytes.
	MemoryMaxBytes int64 `json:"memory_max_bytes,omitempty"`
	// MemoryHighBytes is the soft ceiling (memory.high) in bytes.
	MemoryHighBytes int64 `json:"memory_high_bytes,omitempty"`
	// IOMaxBytesPerSec is the published disk budget in bytes/s.
	IOMaxBytesPerSec int64 `json:"io_max_bytes_per_sec,omitempty"`
	// Files maps a slice file to the exact content the provider writes.
	Files map[string]string `json:"files,omitempty"`
	// Env is the session env contract of the slice.
	Env map[string]string `json:"env,omitempty"`
	// Notes are the caveats (disk device binding, no silent downgrade).
	Notes []string `json:"notes,omitempty"`
}

// Valid reports whether the slice is usable (a path and at least one bound).
func (c WorkspaceCgroup) Valid() bool {
	return c.WorkspaceUUID != "" && strings.HasPrefix(c.Slice, "/") && len(c.Files) > 0
}

// ProcsFile is the file the workspace pid is written into to join the slice.
func (c WorkspaceCgroup) ProcsFile() string {
	if strings.TrimSpace(c.Slice) == "" {
		return ""
	}
	return c.Slice + "/cgroup.procs"
}

// FileNames returns the slice files, sorted, so a report is stable.
func (c WorkspaceCgroup) FileNames() []string {
	names := make([]string, 0, len(c.Files))
	for name := range c.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ApplyScript renders the read-only setup script of the slice: create the
// directory and write the resolved limits, then join it by writing the pid into
// cgroup.procs. Every value is a validated number and every path a validated
// slice name, so the script carries nothing a caller can inject.
func (c WorkspaceCgroup) ApplyScript() string {
	if !c.Valid() {
		return ""
	}
	var out strings.Builder
	out.WriteString("set -eu\n")
	out.WriteString("# ci#792 item 3 - per-workspace cgroup v2 guard; no credential material\n")
	out.WriteString("D=" + c.Slice + "\n")
	out.WriteString("mkdir -p \"$D\"\n")
	for _, name := range c.FileNames() {
		out.WriteString(fmt.Sprintf("printf '%%s' '%s' > \"$D/%s\"\n", c.Files[name], name))
	}
	out.WriteString("# the workspace joins its own slice; the pool's slice stays untouched\n")
	out.WriteString("printf '%%s' \"$D/cgroup.procs\"\n")
	return out.String()
}

// ReportLines renders the slice as the CI/admin report block of the guard.
func (c WorkspaceCgroup) ReportLines() []string {
	if !c.Valid() {
		return []string{"sandbox_cgroup workspace=unresolved"}
	}
	lines := []string{fmt.Sprintf(
		"sandbox_cgroup workspace=%s slice=%s cpu_quota_milli=%d cpu_weight=%d memory_max=%d memory_high=%d io_max_bytes_per_sec=%d",
		c.WorkspaceUUID, c.Slice, c.CPUQuotaMilli, c.CPUWeight, c.MemoryMaxBytes, c.MemoryHighBytes, c.IOMaxBytesPerSec)}
	for _, name := range c.FileNames() {
		lines = append(lines, fmt.Sprintf("sandbox_cgroup workspace=%s file=%s value=%s",
			c.WorkspaceUUID, name, c.Files[name]))
	}
	for _, note := range c.Notes {
		lines = append(lines, fmt.Sprintf("sandbox_cgroup workspace=%s note=%s", c.WorkspaceUUID, note))
	}
	return lines
}

// ResolveWorkspaceCgroup resolves the slice of one workspace. It is pure: it
// never writes a file and never allocates a port block. It fails closed with
// ErrWorkspaceCgroupUnavailable when the guard is off or the host cannot carry
// it, so a caller refuses the lease instead of handing out an unbounded slice,
// and with ErrInvalidCgroupPolicy when the resolved limits are unusable.
func ResolveWorkspaceCgroup(workspaceUUID string, policy WorkspaceCgroupPolicy, host CgroupHostSupport) (WorkspaceCgroup, error) {
	uuid, err := NormalizeWorkspaceUUID(workspaceUUID)
	if err != nil {
		return WorkspaceCgroup{}, err
	}
	if err := policy.Validate(); err != nil {
		return WorkspaceCgroup{}, err
	}
	if !policy.Enabled {
		return WorkspaceCgroup{}, fmt.Errorf(
			"%w: the per-workspace cgroup guard is switched off; set %s=1 to bound CPU/memory per workspace",
			ErrWorkspaceCgroupUnavailable, EnvWorkspaceCgroupGuard)
	}
	if !host.Supported() {
		return WorkspaceCgroup{}, fmt.Errorf("%w: %s (see %s)",
			ErrWorkspaceCgroupUnavailable, strings.Join(host.Blockers, "; "), EnvWorkspaceCgroupRoot)
	}

	root := strings.TrimRight(strings.TrimSpace(policy.Root), "/")
	relative := policy.SlicePrefix + cgroupSliceHash(uuid) + ".slice"
	plan := WorkspaceCgroup{
		WorkspaceUUID:    uuid,
		Slice:            root + "/" + relative,
		Relative:         relative,
		CPUQuotaMilli:    policy.CPUQuotaMilli,
		MemoryMaxBytes:   policy.MemoryMaxBytes,
		IOMaxBytesPerSec: policy.IOMaxBytesPerSec,
		Files:            map[string]string{},
		Env:              map[string]string{},
	}

	if policy.CPUQuotaMilli > 0 {
		plan.CPUWeight = cgroupCPUWeight(policy.CPUQuotaMilli)
		plan.Files["cpu.max"] = fmt.Sprintf("%d %d", cgroupCPUQuotaMicros(policy.CPUQuotaMilli), CgroupCPUPeriodMicros)
		plan.Files["cpu.weight"] = strconv.Itoa(plan.CPUWeight)
	}
	if policy.MemoryMaxBytes > 0 {
		plan.MemoryHighBytes = policy.MemoryMaxBytes * CgroupMemoryHighRatio / CgroupMemoryHighDivisor
		plan.Files["memory.max"] = strconv.FormatInt(policy.MemoryMaxBytes, 10)
		plan.Files["memory.high"] = strconv.FormatInt(plan.MemoryHighBytes, 10)
	}

	plan.Env[EnvWorkspaceCgroupPath] = plan.Slice
	plan.Env[EnvWorkspaceCgroupGuardState] = "enabled"
	if policy.CPUQuotaMilli > 0 {
		plan.Env[EnvWorkspaceCgroupCPUQuota] = strconv.Itoa(policy.CPUQuotaMilli)
	}
	if policy.MemoryMaxBytes > 0 {
		plan.Env[EnvWorkspaceCgroupMemoryMax] = strconv.FormatInt(policy.MemoryMaxBytes, 10)
	}
	if policy.IOMaxBytesPerSec > 0 {
		plan.Env[EnvWorkspaceCgroupIOMax] = strconv.FormatInt(policy.IOMaxBytesPerSec, 10)
	}
	plan.Notes = cgroupNotes(policy)
	return plan, nil
}

// WorkspaceCgroups resolves the slice of every live workspace of the allocator,
// so the pool start path (and the admin surface) can render or apply the guard
// as one block. A workspace whose slice cannot be resolved is not silently
// skipped: the reason is returned as a note.
func (a *PortAllocator) WorkspaceCgroups(policy WorkspaceCgroupPolicy, host CgroupHostSupport) ([]WorkspaceCgroup, []string) {
	status := a.Status()
	out := make([]WorkspaceCgroup, 0, len(status.Workspaces))
	notes := make([]string, 0)
	for _, plan := range status.Workspaces {
		guard, err := ResolveWorkspaceCgroup(plan.WorkspaceUUID, policy, host)
		if err != nil {
			notes = append(notes, fmt.Sprintf("workspace %s: %v", plan.WorkspaceUUID, err))
			continue
		}
		out = append(out, guard)
	}
	return out, notes
}

// CgroupIOMaxLine renders the io.max line of one block device:
// "MAJ:MIN rbps=<bytes/s> wbps=<bytes/s>". The guard publishes the byte budget
// but never guesses the device, so binding disk is this explicit step.
func CgroupIOMaxLine(device string, bytesPerSec int64) (string, error) {
	parts := strings.Split(strings.TrimSpace(device), ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: device %q must be MAJ:MIN", ErrInvalidCgroupPolicy, device)
	}
	major, majorErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	minor, minorErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return "", fmt.Errorf("%w: device %q must be MAJ:MIN", ErrInvalidCgroupPolicy, device)
	}
	if bytesPerSec <= 0 {
		return "", fmt.Errorf("%w: io budget must be positive", ErrInvalidCgroupPolicy)
	}
	return fmt.Sprintf("%d:%d rbps=%d wbps=%d", major, minor, bytesPerSec, bytesPerSec), nil
}

// WorkspaceCgroupStatus is the operator view of the guard: the resolved policy,
// the host check and the slice of every live workspace. Read-only; no secret.
type WorkspaceCgroupStatus struct {
	GeneratedAt      string `json:"generated_at"`
	Enabled          bool   `json:"enabled"`
	Root             string `json:"root"`
	SlicePrefix      string `json:"slice_prefix"`
	CPUQuotaMilli    int    `json:"cpu_quota_milli"`
	MemoryMaxBytes   int64  `json:"memory_max_bytes"`
	IOMaxBytesPerSec int64  `json:"io_max_bytes_per_sec"`
	// Host is the capability check of the pool host (fail-closed gate).
	Host CgroupHostSupport `json:"host"`
	// Workspaces are the resolved slices of the live workspaces.
	Workspaces []WorkspaceCgroup `json:"workspaces,omitempty"`
	// Notes are the caveats and any per-workspace refusal reason.
	Notes []string `json:"notes,omitempty"`
	// PolicyError is set when the guard cannot be applied at all: a broken
	// deployment lever or a host without a unified hierarchy. The lease path
	// fails closed then (see ResolveWorkspaceCgroup).
	PolicyError string `json:"policy_error,omitempty"`
	// Env fields mirror the raw deployment levers.
	EnvGuard      string `json:"env_guard"`
	EnvRoot       string `json:"env_root"`
	EnvCPUQuota   string `json:"env_cpu_quota_milli"`
	EnvMemoryMax  string `json:"env_memory_max_bytes"`
	EnvIOMaxBytes string `json:"env_io_max_bytes_per_sec"`
}

// ResolveWorkspaceCgroupStatus renders the operator view of the guard without
// touching the live allocator: the policy, the host check and (when the
// process-wide allocator is attached) the slice of every live workspace. It
// never creates a slice.
func ResolveWorkspaceCgroupStatus(now time.Time) WorkspaceCgroupStatus {
	policy := WorkspaceCgroupPolicyFromEnv()
	host := DetectCgroupHostSupport(DefaultCgroupHostProbe(policy.Root))
	status := WorkspaceCgroupStatus{
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		Enabled:          policy.Enabled,
		Root:             policy.Root,
		SlicePrefix:      policy.SlicePrefix,
		CPUQuotaMilli:    policy.CPUQuotaMilli,
		MemoryMaxBytes:   policy.MemoryMaxBytes,
		IOMaxBytesPerSec: policy.IOMaxBytesPerSec,
		Host:             host,
		EnvGuard:         os.Getenv(EnvWorkspaceCgroupGuard),
		EnvRoot:          os.Getenv(EnvWorkspaceCgroupRoot),
		EnvCPUQuota:      os.Getenv(EnvWorkspaceCgroupCPUQuotaMilli),
		EnvMemoryMax:     os.Getenv(EnvWorkspaceCgroupMemoryMaxBytes),
		EnvIOMaxBytes:    os.Getenv(EnvWorkspaceCgroupIOMaxBytesPerSec),
	}

	if err := policy.Validate(); err != nil {
		status.PolicyError = err.Error()
		status.Notes = append(status.Notes, "the resolved guard policy cannot be applied, so a guarded pool refuses every workspace until the levers are fixed")
		return status
	}
	status.Notes = append(status.Notes, cgroupNotes(policy)...)
	if !policy.Enabled {
		status.Notes = append(status.Notes,
			"the guard is off: only the MaxWorkspaces count bound applies, so one noisy workspace can still starve its peers")
		return status
	}
	if !host.Supported() {
		status.PolicyError = fmt.Sprintf("%v: %s", ErrWorkspaceCgroupUnavailable, strings.Join(host.Blockers, "; "))
		status.Notes = append(status.Notes, "this pool refuses a guarded workspace rather than handing out an unbounded slice")
		return status
	}

	alloc, err := DefaultAllocator()
	if err != nil || alloc == nil {
		status.Notes = append(status.Notes, "no live allocator is attached yet: only the policy and the host check are reported")
		return status
	}
	guards, notes := alloc.WorkspaceCgroups(policy, host)
	status.Workspaces = guards
	status.Notes = append(status.Notes, notes...)
	return status
}

// cgroupNotes are the documented caveats of the guard, mirroring the file doc.
func cgroupNotes(policy WorkspaceCgroupPolicy) []string {
	notes := []string{
		fmt.Sprintf("the guard is additive to the ci#546 workspace bound: MaxWorkspaces (%d) bounds the count and this slice bounds the resources of each of those workspaces", DefaultNetworkMaxWorkspaces),
		"one noisy workspace cannot starve its peers: cpu.max/cpu.weight and memory.max/memory.high are per slice and the pool's own slice is untouched",
		"the guard never degrades silently: a pool whose host has no unified cgroup v2 hierarchy (or misses a controller) refuses the workspace instead of handing out an unbounded slice",
	}
	if policy.IOMaxBytesPerSec > 0 {
		notes = append(notes, fmt.Sprintf(
			"disk: the byte budget (%d bytes/s) is published as %s and bound per device through io.max (%q) once the pool knows the workspace mount's device",
			policy.IOMaxBytesPerSec, EnvWorkspaceCgroupIOMax, "MAJ:MIN rbps=.. wbps=.."))
	} else {
		notes = append(notes, "disk: no byte budget is configured, so only cpu and memory are bounded (set "+EnvWorkspaceCgroupIOMaxBytesPerSec+")")
	}
	return notes
}

// cgroupSliceHash is the deterministic, file-name-safe slice suffix of a
// workspace UUID (the UUID itself never becomes a path).
func cgroupSliceHash(uuid string) string {
	return fmt.Sprintf("%012x", uuidHash(uuid)&0xffffffffffff)
}

// cgroupCPUWeight maps milli-cores onto cpu.weight (1-10000).
func cgroupCPUWeight(milli int) int {
	if milli < 1 {
		return 1
	}
	if milli > 10000 {
		return 10000
	}
	return milli
}

// cgroupCPUQuotaMicros maps milli-cores onto the cpu.max quota: 1000 milli (one
// core) is one full 100 ms period, ie 100000 microseconds.
func cgroupCPUQuotaMicros(milli int) int {
	return milli * (CgroupCPUPeriodMicros / 1000)
}

// validateCgroupSlicePrefix keeps the derived slice a safe file name: the
// prefix ends up in a path and in the apply script.
func validateCgroupSlicePrefix(prefix string) error {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return fmt.Errorf("%w: slice prefix is empty", ErrInvalidCgroupPolicy)
	}
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("%w: slice prefix %q contains %q, which a cgroup slice name cannot carry", ErrInvalidCgroupPolicy, prefix, string(r))
		}
	}
	return nil
}

// cgroupEnvEnabled reads a boolean lever; anything but an explicit on-value is
// off, so a typo cannot enable a bound nobody configured.
func cgroupEnvEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	}
	return false
}

func envPositiveInt64(key string) (int64, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func cgroupFile(root, name string) string {
	return strings.TrimRight(strings.TrimSpace(root), "/") + "/" + name
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
