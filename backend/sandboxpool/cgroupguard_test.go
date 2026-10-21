package sandboxpool

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// ci#792 follow-up 3: the per-workspace cgroup guard of the bounded shared mode.

func TestDefaultCgroupGuardPolicyIsSelfConsistent(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	if !p.Enabled {
		t.Fatal("the bounded default must have the guard on")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("the default policy must validate: %v", err)
	}
	if p.PoolCPUQuotaMillicores != p.CPUQuotaMillicores*p.MaxWorkspaces {
		t.Errorf("default pod cpu budget %d is not per-workspace x ceiling", p.PoolCPUQuotaMillicores)
	}
	if p.PoolMemoryMB != p.MemoryMB*p.MaxWorkspaces {
		t.Errorf("default pod memory budget %d is not per-workspace x ceiling", p.PoolMemoryMB)
	}
	if p.PoolDiskMB != p.DiskMB*p.MaxWorkspaces {
		t.Errorf("default pod disk budget %d is not per-workspace x ceiling", p.PoolDiskMB)
	}
	if !IsKnownDiskQuotaBackend(p.DiskQuotaBackend) {
		t.Errorf("default disk quota backend %q must be usable", p.DiskQuotaBackend)
	}
	if int64(p.MemoryMB)*bytesPerMB != int64(DefaultCgroupMemoryMB)*bytesPerMB {
		t.Errorf("memory budget does not convert to bytes")
	}
}

func TestCgroupGuardPolicyFailsClosed(t *testing.T) {
	base := DefaultCgroupGuardPolicy()

	cases := []struct {
		name   string
		mutate func(p *CgroupGuardPolicy)
		want   error
		substr string
	}{
		{
			name:   "cgroup v2 unavailable",
			mutate: func(p *CgroupGuardPolicy) { p.Available = false },
			want:   ErrWorkspaceCgroupUnavailable,
			substr: "not available",
		},
		{
			name:   "non-unified hierarchy",
			mutate: func(p *CgroupGuardPolicy) { p.Version = "v1" },
			want:   ErrInvalidCgroupPolicy,
			substr: "v1",
		},
		{
			name:   "empty slice root",
			mutate: func(p *CgroupGuardPolicy) { p.SliceRoot = "" },
			want:   ErrInvalidCgroupPolicy,
			substr: "slice root is empty",
		},
		{
			name:   "slice root escapes cgroupfs",
			mutate: func(p *CgroupGuardPolicy) { p.SliceRoot = "../escape" },
			want:   ErrInvalidCgroupPolicy,
			substr: "single plain path segment",
		},
		{
			name:   "degenerate cpu limit",
			mutate: func(p *CgroupGuardPolicy) { p.CPUQuotaMillicores = MinCgroupCPUQuotaMillicores - 1 },
			want:   ErrInvalidCgroupPolicy,
			substr: "below the",
		},
		{
			name:   "degenerate memory limit",
			mutate: func(p *CgroupGuardPolicy) { p.MemoryMB = MinCgroupMemoryMB - 1 },
			want:   ErrInvalidCgroupPolicy,
			substr: "below the",
		},
		{
			name:   "degenerate pids limit",
			mutate: func(p *CgroupGuardPolicy) { p.PidsMax = MinCgroupPidsMax - 1 },
			want:   ErrInvalidCgroupPolicy,
			substr: "below the",
		},
		{
			name:   "no ceiling",
			mutate: func(p *CgroupGuardPolicy) { p.MaxWorkspaces = 0 },
			want:   ErrInvalidCgroupPolicy,
			substr: "at least 1",
		},
		{
			name:   "unbounded pod",
			mutate: func(p *CgroupGuardPolicy) { p.PoolCPUQuotaMillicores = 0 },
			want:   ErrInvalidCgroupPolicy,
			substr: "pod budget must be configured",
		},
		{
			name: "oversubscribed cpu budget",
			mutate: func(p *CgroupGuardPolicy) {
				p.CPUQuotaMillicores = 1000
				p.MaxWorkspaces = 8
				p.PoolCPUQuotaMillicores = 4000
			},
			want:   ErrInvalidCgroupPolicy,
			substr: "over the 4000m pod cpu budget",
		},
		{
			name: "oversubscribed memory budget",
			mutate: func(p *CgroupGuardPolicy) {
				p.MemoryMB = 4096
				p.MaxWorkspaces = 8
				p.PoolMemoryMB = 8192
			},
			want:   ErrInvalidCgroupPolicy,
			substr: "over the 8192MB pod memory budget",
		},
		{
			name:   "no disk quota backend",
			mutate: func(p *CgroupGuardPolicy) { p.DiskQuotaBackend = "" },
			want:   ErrWorkspaceDiskQuotaUnavailable,
			substr: "no per-workspace disk quota backend",
		},
		{
			name:   "unknown disk quota backend",
			mutate: func(p *CgroupGuardPolicy) { p.DiskQuotaBackend = "nfs-xattr" },
			want:   ErrWorkspaceDiskQuotaUnavailable,
			substr: "nfs-xattr",
		},
		{
			name:   "relative disk root",
			mutate: func(p *CgroupGuardPolicy) { p.DiskRoot = "workspaces" },
			want:   ErrInvalidCgroupPolicy,
			substr: "must be absolute",
		},
		{
			name:   "disk root escapes its parent",
			mutate: func(p *CgroupGuardPolicy) { p.DiskRoot = "/workspaces/../etc" },
			want:   ErrInvalidCgroupPolicy,
			substr: "must not contain navigation",
		},
		{
			name:   "degenerate disk limit",
			mutate: func(p *CgroupGuardPolicy) { p.DiskMB = MinCgroupDiskMB - 1 },
			want:   ErrInvalidCgroupPolicy,
			substr: "disk 1023MB per workspace is below the 1024MB floor",
		},
		{
			name: "oversubscribed disk budget",
			mutate: func(p *CgroupGuardPolicy) {
				p.DiskMB = 4096
				p.MaxWorkspaces = 8
				p.PoolDiskMB = 8192
			},
			want:   ErrInvalidCgroupPolicy,
			substr: "over the 8192MB pod disk budget",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			err := p.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("error %q must name the cause (%q)", err, tc.substr)
			}
			if _, ctorErr := NewCgroupGuard(p); !errors.Is(ctorErr, tc.want) {
				t.Errorf("NewCgroupGuard must fail closed too, got %v", ctorErr)
			}
		})
	}
}

func TestDisabledCgroupGuardIsTheExplicitOptOut(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	p.Enabled = false
	// Even a policy that would never validate is fine while disabled: the
	// opt-out is explicit and recorded, and the workspace runs unbounded.
	p.Available = false
	p.SliceRoot = ""
	p.PoolMemoryMB = 0
	if err := p.Validate(); err != nil {
		t.Fatalf("a disabled guard must validate: %v", err)
	}
	guard, err := NewCgroupGuard(p)
	if err != nil {
		t.Fatalf("NewCgroupGuard: %v", err)
	}
	if guard.Enabled() {
		t.Error("Enabled() must report false")
	}
	if guard.Slots() != 0 {
		t.Errorf("Slots() = %d, want 0 while disabled", guard.Slots())
	}
	plan, err := guard.Apply("chat-a")
	if err != nil {
		t.Fatalf("Apply on a disabled guard must not bound: %v", err)
	}
	if plan != (WorkspaceCgroupPlan{}) {
		t.Errorf("a disabled guard must return a zero plan, got %+v", plan)
	}
	if lines := strings.Join(guard.ReportLines(), " "); !strings.Contains(lines, "unbounded") {
		t.Errorf("the report must record the opt-out, got %q", lines)
	}
}

func TestCgroupGuardAppliesOneSlicePerWorkspace(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	p.MaxWorkspaces = 3
	p.PoolCPUQuotaMillicores = p.CPUQuotaMillicores * 3
	p.PoolMemoryMB = p.MemoryMB * 3
	guard, err := NewCgroupGuard(p)
	if err != nil {
		t.Fatalf("NewCgroupGuard: %v", err)
	}

	plan, err := guard.Apply("chat-a")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if plan.WorkspaceUUID != "chat-a" {
		t.Errorf("workspace = %q", plan.WorkspaceUUID)
	}
	if plan.SlicePath != "/sys/fs/cgroup/open-chat-pool-workspace-chat-a.slice" {
		t.Errorf("slice path = %q", plan.SlicePath)
	}
	if plan.CPUQuotaMillicores != p.CPUQuotaMillicores || plan.PidsMax != p.PidsMax {
		t.Errorf("plan limits %+v do not match the policy", plan)
	}
	if plan.MemoryMaxBytes != int64(p.MemoryMB)*bytesPerMB {
		t.Errorf("MemoryMaxBytes = %d, want %d", plan.MemoryMaxBytes, int64(p.MemoryMB)*bytesPerMB)
	}
	if plan.DiskQuotaBytes != int64(p.DiskMB)*bytesPerMB {
		t.Errorf("DiskQuotaBytes = %d, want %d", plan.DiskQuotaBytes, int64(p.DiskMB)*bytesPerMB)
	}
	if plan.DiskQuotaBackend != p.DiskQuotaBackend {
		t.Errorf("DiskQuotaBackend = %q, want %q", plan.DiskQuotaBackend, p.DiskQuotaBackend)
	}
	if want := "/workspaces/chat-a"; plan.DiskQuotaTarget != want {
		t.Errorf("DiskQuotaTarget = %q, want %q", plan.DiskQuotaTarget, want)
	}
	if plan.MaxWorkspaces != 3 {
		t.Errorf("MaxWorkspaces = %d, want 3", plan.MaxWorkspaces)
	}

	if _, err := guard.Apply("chat-a"); err != nil {
		t.Fatalf("a repeated Apply must be idempotent: %v", err)
	}
	if got := len(guard.ActiveWorkspaces()); got != 1 {
		t.Errorf("active = %d, want 1 after a repeated Apply", got)
	}

	env := plan.Env()
	wantEnv := map[string]string{
		EnvCgroupWorkspaceSlice:          plan.SlicePath,
		EnvCgroupWorkspaceCPUMillicores:  "500",
		EnvCgroupWorkspaceMemoryMaxBytes: "2147483648",
		EnvCgroupWorkspacePidsMax:        "512",
		EnvDiskWorkspaceQuotaBytes:       "4294967296",
		EnvDiskWorkspaceBackend:          DiskQuotaBackendProjectQuota,
		EnvDiskWorkspaceTarget:           "/workspaces/chat-a",
	}
	for key, want := range wantEnv {
		if env[key] != want {
			t.Errorf("env %s = %q, want %q", key, env[key], want)
		}
	}
	if lines := strings.Join(plan.ReportLines(), " "); !strings.Contains(lines, EnvCgroupWorkspaceSlice) {
		t.Errorf("the report must name the injected env, got %q", lines)
	}

	if _, err := guard.Apply("bad uuid"); !errors.Is(err, ErrInvalidWorkspaceUUID) {
		t.Errorf("an invalid workspace id must be refused, got %v", err)
	}

	if !guard.Release("chat-a") {
		t.Error("Release must report the freed slice")
	}
	if guard.Release("chat-a") {
		t.Error("a second Release must not report a slice")
	}
	if _, ok := guard.Plan("chat-a"); ok {
		t.Error("Plan must not find a released slice")
	}
}

func TestCgroupGuardExhaustionSignal(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	p.MaxWorkspaces = 1
	p.PoolCPUQuotaMillicores = p.CPUQuotaMillicores
	p.PoolMemoryMB = p.MemoryMB
	guard, err := NewCgroupGuard(p)
	if err != nil {
		t.Fatalf("NewCgroupGuard: %v", err)
	}
	if _, err := guard.Apply("chat-a"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	_, err = guard.Apply("chat-b")
	if err == nil {
		t.Fatal("the second workspace must not fit")
	}
	if !errors.Is(err, ErrCgroupCapacity) {
		t.Errorf("the refusal must match ErrCgroupCapacity: %v", err)
	}
	if !IsCgroupExhausted(err) {
		t.Errorf("IsCgroupExhausted must match the refusal: %v", err)
	}
	if !IsPoolResourceFailure(err) {
		t.Errorf("IsPoolResourceFailure must match the refusal: %v", err)
	}
	signal, ok := AsCgroupExhausted(err)
	if !ok {
		t.Fatalf("AsCgroupExhausted must extract the signal from %v", err)
	}
	if signal.Signal() != CgroupExhaustedSignal {
		t.Errorf("Signal() = %q, want %q", signal.Signal(), CgroupExhaustedSignal)
	}
	if signal.WorkspaceUUID != "chat-b" || signal.Slots != 1 || signal.ActiveWorkspaces != 1 {
		t.Errorf("signal fields %+v do not describe the refusal", signal)
	}
	if signal.SliceRoot != p.SliceRoot {
		t.Errorf("slice root = %q, want %q", signal.SliceRoot, p.SliceRoot)
	}
	remediations := signal.Remediations()
	if len(remediations) < 2 || remediations[0] != RemediationSpillPoolPod || remediations[1] != RemediationDedicatedSandbox {
		t.Errorf("remediations %v must lead with the shared vocabulary", remediations)
	}
	if !strings.Contains(err.Error(), ErrCgroupCapacity.Error()) {
		t.Errorf("error %q must keep the sentinel prefix", err)
	}

	if IsCgroupExhausted(nil) {
		t.Error("IsCgroupExhausted(nil) must be false")
	}
	if IsCgroupExhausted(errors.New("other")) {
		t.Error("IsCgroupExhausted must not match an unrelated error")
	}
	if IsCgroupExhausted(fmt.Errorf("%w: workspace x", ErrInvalidCgroupPolicy)) {
		t.Error("IsCgroupExhausted must not match an unrelated guard error")
	}
	if _, ok := AsCgroupExhausted(fmt.Errorf("%w: workspace x", ErrInvalidCgroupPolicy)); ok {
		t.Error("AsCgroupExhausted must not extract a signal from a policy error")
	}
	wrapped := fmt.Errorf("lease: %w", err)
	if !IsCgroupExhausted(wrapped) {
		t.Errorf("IsCgroupExhausted must see through wrapping: %v", wrapped)
	}
	if _, ok := AsCgroupExhausted(wrapped); !ok {
		t.Errorf("AsCgroupExhausted must see through wrapping: %v", wrapped)
	}
}

func TestDiskQuotaExceededSignal(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	plan := WorkspaceCgroupPlan{
		WorkspaceUUID:    "chat-a",
		DiskQuotaBytes:   int64(p.DiskMB) * bytesPerMB,
		DiskQuotaBackend: p.DiskQuotaBackend,
		DiskQuotaTarget:  p.diskTarget("chat-a"),
	}
	err := &DiskQuotaExceededError{
		WorkspaceUUID: plan.WorkspaceUUID,
		QuotaBytes:    plan.DiskQuotaBytes,
		Backend:       plan.DiskQuotaBackend,
		Target:        plan.DiskQuotaTarget,
	}
	if !errors.Is(err, ErrDiskQuotaExceeded) {
		t.Errorf("the signal must match its sentinel: %v", err)
	}
	if !IsDiskQuotaExceeded(err) || !IsPoolResourceFailure(err) {
		t.Errorf("IsDiskQuotaExceeded/IsPoolResourceFailure must match the signal: %v", err)
	}
	signal, ok := AsDiskQuotaExceeded(fmt.Errorf("write /workspaces/chat-a/out: %w", err))
	if !ok {
		t.Fatalf("AsDiskQuotaExceeded must see through wrapping: %v", err)
	}
	if signal.WorkspaceUUID != "chat-a" || signal.QuotaBytes != plan.DiskQuotaBytes {
		t.Errorf("signal fields %+v do not describe the workspace", signal)
	}
	if signal.Signal() != DiskQuotaExceededSignal {
		t.Errorf("Signal() = %q, want %q", signal.Signal(), DiskQuotaExceededSignal)
	}
	remediations := signal.Remediations()
	if len(remediations) != 3 || remediations[0] != "raise "+EnvDiskQuotaMB ||
		remediations[1] != RemediationSpillPoolPod || remediations[2] != RemediationDedicatedSandbox {
		t.Errorf("remediations %v must lead with raising the quota, then the shared vocabulary", remediations)
	}
	if text := err.Error(); !strings.Contains(text, DiskQuotaExceededSignal+"") && !strings.Contains(text, ErrDiskQuotaExceeded.Error()) {
		t.Errorf("error %q must keep the sentinel prefix", text)
	}
	if !strings.Contains(err.Error(), "/workspaces/chat-a") || !strings.Contains(err.Error(), p.DiskQuotaBackend) {
		t.Errorf("error %q must name the target and the backend", err)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "token") {
		t.Errorf("the signal must carry no credential material: %q", err)
	}

	if IsDiskQuotaExceeded(nil) {
		t.Error("IsDiskQuotaExceeded(nil) must be false")
	}
	if IsDiskQuotaExceeded(errors.New("other")) {
		t.Error("IsDiskQuotaExceeded must not match an unrelated error")
	}
	if IsDiskQuotaExceeded(fmt.Errorf("%w: quota", ErrInvalidCgroupPolicy)) {
		t.Error("IsDiskQuotaExceeded must not match a policy error")
	}
	if _, ok := AsDiskQuotaExceeded(fmt.Errorf("%w: quota", ErrInvalidCgroupPolicy)); ok {
		t.Error("AsDiskQuotaExceeded must not extract a signal from a policy error")
	}
	// The bare sentinel counts too: the pre-signal spelling of the same state.
	if !IsDiskQuotaExceeded(fmt.Errorf("quota tool: %w", ErrDiskQuotaExceeded)) {
		t.Error("IsDiskQuotaExceeded must match the bare sentinel through wrapping")
	}
	if IsCgroupExhausted(err) {
		t.Error("IsCgroupExhausted must not match a disk signal")
	}
	if IsDiskQuotaExceeded(fmt.Errorf("%w: x", ErrCgroupCapacity)) {
		t.Error("IsDiskQuotaExceeded must not match a cgroup exhaustion")
	}
}

func TestEffectiveMaxWorkspacesIsTheSmallerBound(t *testing.T) {
	guard := DefaultCgroupGuardPolicy()
	cases := []struct {
		name    string
		network int
		guard   CgroupGuardPolicy
		want    int
	}{
		{name: "equal bounds", network: 8, guard: guard, want: 8},
		{name: "guard smaller wins", network: 8, guard: func() CgroupGuardPolicy { g := guard; g.MaxWorkspaces = 4; return g }(), want: 4},
		{name: "network smaller wins", network: 3, guard: guard, want: 3},
		{name: "guard disabled leaves the network bound", network: 8, guard: func() CgroupGuardPolicy { g := guard; g.Enabled = false; return g }(), want: 8},
		{name: "unbounded network falls back to the guard", network: 0, guard: func() CgroupGuardPolicy { g := guard; g.MaxWorkspaces = 2; return g }(), want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveMaxWorkspaces(NetworkPolicy{MaxWorkspaces: tc.network}, tc.guard); got != tc.want {
				t.Errorf("EffectiveMaxWorkspaces = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCgroupGuardPolicyFromEnv(t *testing.T) {
	keys := []string{
		EnvCgroupGuard, EnvCgroupAvailable, EnvCgroupCPUQuotaMillicores,
		EnvCgroupMemoryMB, EnvCgroupPidsMax, EnvCgroupMaxWorkspaces,
		EnvCgroupPoolCPUQuotaMillicores, EnvCgroupPoolMemoryMB, EnvCgroupSliceRoot,
		EnvDiskQuotaMB, EnvDiskQuotaBackend, EnvCgroupPoolDiskMB,
	}
	for _, key := range keys {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	def := CgroupGuardPolicyFromEnv()
	if def != DefaultCgroupGuardPolicy() {
		t.Errorf("an empty environment must resolve to the default, got %+v", def)
	}

	t.Setenv(EnvCgroupGuard, "off")
	t.Setenv(EnvCgroupCPUQuotaMillicores, "not-a-number")
	t.Setenv(EnvCgroupSliceRoot, " pool-a ")
	got := CgroupGuardPolicyFromEnv()
	if got.Enabled {
		t.Error("EnvCgroupGuard=off must disable the guard")
	}
	if got.CPUQuotaMillicores != DefaultCgroupCPUQuotaMillicores {
		t.Errorf("an unparsable lever must keep the bounded default, got %d", got.CPUQuotaMillicores)
	}
	if got.SliceRoot != "pool-a" {
		t.Errorf("slice root = %q, want the trimmed value", got.SliceRoot)
	}

	t.Setenv(EnvCgroupGuard, "on")
	t.Setenv(EnvCgroupMemoryMB, "1024")
	t.Setenv(EnvCgroupMaxWorkspaces, "4")
	t.Setenv(EnvCgroupPoolMemoryMB, "4096")
	t.Setenv(EnvCgroupPoolCPUQuotaMillicores, "2000")
	resolved := CgroupGuardPolicyFromEnv()
	if resolved.MemoryMB != 1024 || resolved.MaxWorkspaces != 4 {
		t.Errorf("resolved policy %+v did not pick up the levers", resolved)
	}
	if err := resolved.Validate(); err != nil {
		t.Errorf("a lever-consistent policy must validate: %v", err)
	}

	// Raising the per-workspace limit without raising the pod budget is caught.
	t.Setenv(EnvCgroupMemoryMB, "8192")
	if err := CgroupGuardPolicyFromEnv().Validate(); !errors.Is(err, ErrInvalidCgroupPolicy) {
		t.Errorf("an oversubscribed env policy must be refused, got %v", err)
	}

	// The disk levers move the quota and its backend the same way.
	t.Setenv(EnvCgroupMemoryMB, "1024")
	t.Setenv(EnvDiskQuotaMB, "2048")
	t.Setenv(EnvCgroupPoolDiskMB, "8192")
	t.Setenv(EnvDiskQuotaBackend, "loopback")
	disk := CgroupGuardPolicyFromEnv()
	if disk.DiskMB != 2048 || disk.DiskQuotaBackend != DiskQuotaBackendLoopback {
		t.Errorf("disk levers did not resolve: %+v", disk)
	}
	if err := disk.Validate(); err != nil {
		t.Errorf("a lever-consistent disk policy must validate: %v", err)
	}
	if target := disk.DiskQuotaTarget("chat-a"); target != "/workspaces/chat-a" {
		t.Errorf("DiskQuotaTarget = %q, want the default root", target)
	}

	// A pool that reports a backend this code does not know fails closed.
	t.Setenv(EnvDiskQuotaBackend, "guess")
	if err := CgroupGuardPolicyFromEnv().Validate(); !errors.Is(err, ErrWorkspaceDiskQuotaUnavailable) {
		t.Errorf("an unknown disk backend must be refused, got %v", err)
	}
}

func TestDefaultCgroupGuardSeam(t *testing.T) {
	previous := DefaultCgroupGuard()
	t.Cleanup(func() { SetDefaultCgroupGuard(previous) })

	if DefaultCgroupGuard() != nil {
		t.Fatal("the seam must start empty in tests")
	}
	guard, err := NewCgroupGuard(DefaultCgroupGuardPolicy())
	if err != nil {
		t.Fatalf("NewCgroupGuard: %v", err)
	}
	SetDefaultCgroupGuard(guard)
	if DefaultCgroupGuard() != guard {
		t.Error("SetDefaultCgroupGuard must install the guard")
	}
	SetDefaultCgroupGuard(nil)
	if DefaultCgroupGuard() != nil {
		t.Error("SetDefaultCgroupGuard(nil) must clear the guard")
	}
}

func TestCgroupGuardReportNamesStates(t *testing.T) {
	p := DefaultCgroupGuardPolicy()
	p.MaxWorkspaces = 2
	p.PoolCPUQuotaMillicores = p.CPUQuotaMillicores * 2
	p.PoolMemoryMB = p.MemoryMB * 2
	guard, err := NewCgroupGuard(p)
	if err != nil {
		t.Fatalf("NewCgroupGuard: %v", err)
	}
	empty := strings.Join(guard.ReportLines(), "\n")
	if !strings.Contains(empty, "no slice in use") {
		t.Errorf("a fresh report must say so, got %q", empty)
	}
	if _, err := guard.Apply("chat-a"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	full := strings.Join(guard.ReportLines(), "\n")
	if !strings.Contains(full, "slices in use: 1") || !strings.Contains(full, "chat-a") {
		t.Errorf("the report must list the live slice, got %q", full)
	}
	if strings.Contains(full, "password") || strings.Contains(full, "token") {
		t.Errorf("the report must carry no credential material, got %q", full)
	}
}
