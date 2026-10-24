package sandboxpool

import (
	"errors"
	"strings"
	"testing"
)

func cgroupTestHost(controllers ...string) CgroupHostSupport {
	return CgroupHostSupport{Root: "/sys/fs/cgroup", Unified: true, Controllers: controllers}
}

func cgroupFullHost() CgroupHostSupport {
	return cgroupTestHost("cpu", "io", "memory", "pids")
}

func cgroupTestNewWorkspace() string {
	return "01234567-89ab-cdef-0123-456789abcdef"
}

func cgroupEnabledPolicy() WorkspaceCgroupPolicy {
	policy := DefaultWorkspaceCgroupPolicy()
	policy.Enabled = true
	return policy
}

func TestDetectCgroupHostSupport(t *testing.T) {
	t.Run("unified with every controller", func(t *testing.T) {
		support := DetectCgroupHostSupport(CgroupHostProbe{
			Root: "/sys/fs/cgroup",
			ReadFile: func(string) ([]byte, error) {
				return []byte("cpuset cpu io memory pids\n"), nil
			},
		})
		if !support.Supported() {
			t.Fatalf("expected support, got %v", support.Blockers)
		}
		if len(support.Controllers) != 5 || support.Controllers[0] != "cpu" {
			t.Fatalf("controllers must be sorted: %v", support.Controllers)
		}
		if !strings.Contains(strings.Join(support.ReportLines(), " "), "host=supported") {
			t.Fatalf("report must say supported: %v", support.ReportLines())
		}
	})

	t.Run("v1 or unmounted hierarchy fails closed", func(t *testing.T) {
		support := DetectCgroupHostSupport(CgroupHostProbe{
			Root:     "/sys/fs/cgroup",
			ReadFile: func(string) ([]byte, error) { return nil, errors.New("no such file") },
		})
		if support.Supported() || support.Unified {
			t.Fatalf("must not report support: %+v", support)
		}
		if len(support.Blockers) != 1 || !strings.Contains(support.Blockers[0], "cgroup.controllers") {
			t.Fatalf("blocker must name the mount: %v", support.Blockers)
		}
	})

	t.Run("missing controller is a blocker, never a silently unenforced limit", func(t *testing.T) {
		support := DetectCgroupHostSupport(CgroupHostProbe{
			ReadFile: func(string) ([]byte, error) { return []byte("cpu memory"), nil },
		})
		if support.Supported() {
			t.Fatal("a missing io controller must not be reported as supported")
		}
		if len(support.Blockers) != 1 || !strings.Contains(support.Blockers[0], `"io"`) {
			t.Fatalf("blocker must name io: %v", support.Blockers)
		}
	})
}

func TestWorkspaceCgroupPolicyValidate(t *testing.T) {
	t.Run("defaults are valid and the guard is off", func(t *testing.T) {
		policy := DefaultWorkspaceCgroupPolicy()
		if policy.Enabled {
			t.Fatal("the guard must be opt-in")
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("defaults must validate: %v", err)
		}
	})

	t.Run("enabled without any limit fails closed", func(t *testing.T) {
		policy := WorkspaceCgroupPolicy{Enabled: true, Root: "/sys/fs/cgroup", SlicePrefix: "wsc-"}
		err := policy.Validate()
		if !errors.Is(err, ErrInvalidCgroupPolicy) || !strings.Contains(err.Error(), "would bound nothing") {
			t.Fatalf("expected a bound-nothing refusal, got %v", err)
		}
	})

	t.Run("bad root, prefix and limits are refused", func(t *testing.T) {
		for name, policy := range map[string]WorkspaceCgroupPolicy{
			"relative root": {Root: "sys/fs/cgroup", SlicePrefix: "wsc-"},
			"empty prefix":  {Root: "/sys/fs/cgroup"},
			"prefix slash":  {Root: "/sys/fs/cgroup", SlicePrefix: "a/b"},
			"prefix space":  {Root: "/sys/fs/cgroup", SlicePrefix: "a b"},
			"tiny memory":   {Root: "/sys/fs/cgroup", SlicePrefix: "wsc-", MemoryMaxBytes: 1024},
			"huge memory":   {Root: "/sys/fs/cgroup", SlicePrefix: "wsc-", MemoryMaxBytes: 1 << 42},
			"negative io":   {Root: "/sys/fs/cgroup", SlicePrefix: "wsc-", IOMaxBytesPerSec: -1},
		} {
			if err := policy.Validate(); !errors.Is(err, ErrInvalidCgroupPolicy) {
				t.Fatalf("%s: expected ErrInvalidCgroupPolicy, got %v", name, err)
			}
		}
	})
}

func TestResolveWorkspaceCgroupFailsClosed(t *testing.T) {
	uuid := cgroupTestNewWorkspace()

	t.Run("guard off", func(t *testing.T) {
		_, err := ResolveWorkspaceCgroup(uuid, DefaultWorkspaceCgroupPolicy(), cgroupFullHost())
		if !errors.Is(err, ErrWorkspaceCgroupUnavailable) || !strings.Contains(err.Error(), EnvWorkspaceCgroupGuard) {
			t.Fatalf("expected an unavailable refusal naming the lever, got %v", err)
		}
	})

	t.Run("host without v2", func(t *testing.T) {
		host := CgroupHostSupport{Root: "/sys/fs/cgroup", Blockers: []string{"no cgroup v2"}}
		if _, err := ResolveWorkspaceCgroup(uuid, cgroupEnabledPolicy(), host); !errors.Is(err, ErrWorkspaceCgroupUnavailable) {
			t.Fatalf("expected a fail-closed refusal, got %v", err)
		}
	})

	t.Run("invalid workspace", func(t *testing.T) {
		if _, err := ResolveWorkspaceCgroup("not-a-uuid", cgroupEnabledPolicy(), cgroupFullHost()); !errors.Is(err, ErrInvalidWorkspaceUUID) {
			t.Fatalf("expected ErrInvalidWorkspaceUUID, got %v", err)
		}
	})
}

func TestResolveWorkspaceCgroupBoundsOneWorkspace(t *testing.T) {
	uuid := cgroupTestNewWorkspace()
	plan, err := ResolveWorkspaceCgroup(uuid, cgroupEnabledPolicy(), cgroupFullHost())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !plan.Valid() {
		t.Fatalf("plan must be valid: %+v", plan)
	}
	if plan.WorkspaceUUID != uuid {
		t.Fatalf("uuid must be preserved: %s", plan.WorkspaceUUID)
	}
	if !strings.HasPrefix(plan.Slice, DefaultWorkspaceCgroupRoot+"/"+DefaultWorkspaceCgroupSlicePrefix) {
		t.Fatalf("slice must live under the root with the prefix: %s", plan.Slice)
	}
	if strings.Contains(plan.Slice, uuid) {
		t.Fatalf("the uuid must never become a path: %s", plan.Slice)
	}
	if plan.ProcsFile() != plan.Slice+"/cgroup.procs" {
		t.Fatalf("procs file: %s", plan.ProcsFile())
	}

	// cpu.max: one core is one full period.
	wantQuota := DefaultWorkspaceCgroupCPUQuotaMilli * (CgroupCPUPeriodMicros / 1000)
	if plan.Files["cpu.max"] != "100000 100000" {
		t.Fatalf("cpu.max must be %d us over one period, got %q", wantQuota, plan.Files["cpu.max"])
	}
	if plan.CPUWeight != 1000 || plan.Files["cpu.weight"] != "1000" {
		t.Fatalf("cpu.weight must follow the quota: %q", plan.Files["cpu.weight"])
	}
	// memory: hard ceiling plus the soft one below it, so a workspace is
	// throttled before it is killed.
	if plan.Files["memory.max"] != "2147483648" {
		t.Fatalf("memory.max: %q", plan.Files["memory.max"])
	}
	if plan.MemoryHighBytes != DefaultWorkspaceCgroupMemoryMaxBytes*9/10 {
		t.Fatalf("memory.high must be derived from memory.max: %d", plan.MemoryHighBytes)
	}
	// disk is published, never guessed.
	if plan.Env[EnvWorkspaceCgroupIOMax] != "67108864" {
		t.Fatalf("io budget must be published: %v", plan.Env)
	}
	if _, bound := plan.Files["io.max"]; bound {
		t.Fatal("io.max needs a device and must not be guessed into the plan")
	}

	// the env contract is names and numbers only.
	if plan.Env[EnvWorkspaceCgroupPath] != plan.Slice || plan.Env[EnvWorkspaceCgroupGuardState] != "enabled" {
		t.Fatalf("env contract: %v", plan.Env)
	}
	for _, value := range plan.Env {
		if strings.Contains(value, uuid) && value != plan.Slice {
			t.Fatalf("no env value may leak the uuid: %v", plan.Env)
		}
	}
	if plan.Env[EnvWorkspaceCgroupCPUQuota] != "1000" || plan.Env[EnvWorkspaceCgroupMemoryMax] != "2147483648" {
		t.Fatalf("limits must be readable by the session: %v", plan.Env)
	}

	lines := strings.Join(plan.ReportLines(), "\n")
	for _, want := range []string{"sandbox_cgroup workspace=" + uuid, "slice=" + plan.Slice, "cpu_quota_milli=1000"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("report misses %q:\n%s", want, lines)
		}
	}
}

func TestResolveWorkspaceCgroupIsDeterministicAndDisjoint(t *testing.T) {
	policy := cgroupEnabledPolicy()
	host := cgroupFullHost()
	first, err := ResolveWorkspaceCgroup(cgroupTestNewWorkspace(), policy, host)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	again, err := ResolveWorkspaceCgroup(cgroupTestNewWorkspace(), policy, host)
	if err != nil || again.Slice != first.Slice {
		t.Fatalf("the slice must be idempotent across a pool restart: %v %s vs %s", err, again.Slice, first.Slice)
	}
	for _, other := range []string{
		"01234567-89ab-cdef-0123-456789abcdee",
		"fedcba98-7654-3210-fedc-ba9876543210",
		"00112233-4455-6677-8899-aabbccddeeff",
	} {
		plan, err := ResolveWorkspaceCgroup(other, policy, host)
		if err != nil {
			t.Fatalf("resolve %s: %v", other, err)
		}
		if plan.Slice == first.Slice {
			t.Fatalf("%s shares the slice of %s (%s)", other, first.WorkspaceUUID, plan.Slice)
		}
	}
}

func TestWorkspaceCgroupApplyScript(t *testing.T) {
	plan, err := ResolveWorkspaceCgroup(cgroupTestNewWorkspace(), cgroupEnabledPolicy(), cgroupFullHost())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	script := plan.ApplyScript()
	for _, want := range []string{"set -eu", "mkdir -p \"$D\"", "cpu.max", "memory.high", "cgroup.procs"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script misses %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "$"+"{") || strings.Contains(script, cgroupTestNewWorkspace()) {
		t.Fatalf("the script must carry no interpolation or uuid:\n%s", script)
	}
	if empty := (WorkspaceCgroup{}).ApplyScript(); empty != "" {
		t.Fatalf("an unresolved slice must render no script: %q", empty)
	}
}

func TestDisableIOLimitKeepsCPUAndMemoryBound(t *testing.T) {
	policy := cgroupEnabledPolicy()
	policy.IOMaxBytesPerSec = 0
	plan, err := ResolveWorkspaceCgroup(cgroupTestNewWorkspace(), policy, cgroupFullHost())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := plan.Env[EnvWorkspaceCgroupIOMax]; ok {
		t.Fatal("no io lever must be published when no budget is configured")
	}
	if plan.Files["cpu.max"] == "" || plan.Files["memory.max"] == "" {
		t.Fatalf("cpu and memory must stay bounded: %v", plan.Files)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), EnvWorkspaceCgroupIOMaxBytesPerSec) {
		t.Fatalf("the caveat must name the io lever: %v", plan.Notes)
	}
}

func TestCgroupIOMaxLine(t *testing.T) {
	line, err := CgroupIOMaxLine("8:0", DefaultWorkspaceCgroupIOMaxBytesPerSec)
	if err != nil || line != "8:0 rbps=67108864 wbps=67108864" {
		t.Fatalf("io.max line: %q %v", line, err)
	}
	for _, device := range []string{"", "8", "8:", "a:0", "8:-1"} {
		if _, err := CgroupIOMaxLine(device, 1024); !errors.Is(err, ErrInvalidCgroupPolicy) {
			t.Fatalf("%q: expected ErrInvalidCgroupPolicy, got %v", device, err)
		}
	}
	if _, err := CgroupIOMaxLine("8:0", 0); !errors.Is(err, ErrInvalidCgroupPolicy) {
		t.Fatalf("a zero budget must be refused, got %v", err)
	}
}

func TestAllocatorWorkspaceCgroupsFollowsTheLeases(t *testing.T) {
	alloc := newTestAllocator(t, DefaultNetworkPolicy())
	if _, err := alloc.Allocate(cgroupTestNewWorkspace(), "msgmate-io/open-chat-go", "dev"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	guards, notes := alloc.WorkspaceCgroups(cgroupEnabledPolicy(), cgroupFullHost())
	if len(guards) != 1 || len(notes) != 0 {
		t.Fatalf("one live workspace must resolve one slice: %d guards, notes %v", len(guards), notes)
	}
	if guards[0].WorkspaceUUID != cgroupTestNewWorkspace() {
		t.Fatalf("the slice must belong to the lease: %+v", guards[0])
	}
	alloc.Release(cgroupTestNewWorkspace())
	if guards, notes := alloc.WorkspaceCgroups(cgroupEnabledPolicy(), cgroupFullHost()); len(guards) != 0 || len(notes) != 0 {
		t.Fatalf("a released workspace must leave no slice: %d guards, notes %v", len(guards), notes)
	}

	// A host that cannot carry the guard must not silently drop the workspace:
	// the reason is surfaced and no slice is handed out.
	blocked := CgroupHostSupport{Root: "/sys/fs/cgroup", Blockers: []string{"no cgroup v2"}}
	if _, err := alloc.Allocate("00112233-4455-6677-8899-aabbccddeeff", "msgmate-io/open-chat-go", "dev"); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	guards, notes = alloc.WorkspaceCgroups(cgroupEnabledPolicy(), blocked)
	if len(guards) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "no cgroup v2") {
		t.Fatalf("a blocked host must be reported, got %d guards and notes %v", len(guards), notes)
	}
}

func TestResolveWorkspaceCgroupStatus(t *testing.T) {
	t.Run("off by default, and the count bound is what remains", func(t *testing.T) {
		status := ResolveWorkspaceCgroupStatus(testNow())
		if status.Enabled {
			t.Fatal("the guard must be opt-in")
		}
		if status.PolicyError != "" {
			t.Fatalf("the default policy must resolve: %s", status.PolicyError)
		}
		joined := strings.Join(status.Notes, "\n")
		if !strings.Contains(joined, "only the MaxWorkspaces count bound applies") {
			t.Fatalf("the report must state the remaining bound: %v", status.Notes)
		}
		if !strings.Contains(joined, "additive to the ci#546 workspace bound") {
			t.Fatalf("the report must reference the ci#546 bound: %v", status.Notes)
		}
	})

	t.Run("enabled on a v1 host refuses the workspace instead of unbounded slices", func(t *testing.T) {
		t.Setenv(EnvWorkspaceCgroupGuard, "1")
		t.Setenv(EnvWorkspaceCgroupRoot, "/nonexistent/cgroup")
		status := ResolveWorkspaceCgroupStatus(testNow())
		if !status.Enabled || status.PolicyError == "" {
			t.Fatalf("an unusable host must set a policy error: %+v", status)
		}
		if !strings.Contains(status.PolicyError, "refuses a guarded workspace") &&
			!strings.Contains(strings.Join(status.Notes, "\n"), "refuses a guarded workspace") {
			t.Fatalf("the refusal must be explicit: %v / %v", status.PolicyError, status.Notes)
		}
	})

	t.Run("a mis-typed lever fails closed", func(t *testing.T) {
		t.Setenv(EnvWorkspaceCgroupGuard, "1")
		t.Setenv(EnvWorkspaceCgroupSlicePrefix, "bad/prefix")
		status := ResolveWorkspaceCgroupStatus(testNow())
		if status.PolicyError == "" {
			t.Fatal("an invalid prefix must not resolve to a usable policy")
		}
	})

	t.Run("a boolean typo never enables an unconfigured bound", func(t *testing.T) {
		t.Setenv(EnvWorkspaceCgroupGuard, "maybe")
		if ResolveWorkspaceCgroupStatus(testNow()).Enabled {
			t.Fatal("a non-boolean lever value must leave the guard off")
		}
	})

	t.Run("an unparseable limit falls back to the bounded default", func(t *testing.T) {
		t.Setenv(EnvWorkspaceCgroupMemoryMaxBytes, "not-a-number")
		status := ResolveWorkspaceCgroupStatus(testNow())
		if status.MemoryMaxBytes != DefaultWorkspaceCgroupMemoryMaxBytes {
			t.Fatalf("a typo must not widen the memory bound: %d", status.MemoryMaxBytes)
		}
	})
}

func testNow() time.Time { return time.Unix(1700000000, 0).UTC() }
