package msgmate

import (
	"strings"
	"testing"
)

// ci#662: the sandbox pool's memory budget. The rules these tests pin are
// "measure first, bound second, fail open": a missing measurement must never
// invent a number, and a measured one must bound agents-per-pool by how many
// per-sandbox memory requests actually fit on the node.

// sandboxMemoryConfig builds a shared config from key/value pairs so the tests
// spell out exactly the keys the policy reads.
func sandboxMemoryConfig(pairs ...interface{}) map[string]interface{} {
	config := map[string]interface{}{}
	for i := 0; i+1 < len(pairs); i += 2 {
		config[pairs[i].(string)] = pairs[i+1]
	}
	return config
}

func clearSandboxMemoryEnv(t *testing.T) {
	t.Helper()
	withEnv(t, SandboxMemoryRequestEnvKey, "")
	withEnv(t, SandboxMemoryLimitEnvKey, "")
	withEnv(t, NodeAllocatableMemoryEnvKey, "")
	withEnv(t, SandboxMemoryReserveEnvKey, "")
	withEnv(t, ProviderMaxConcurrencyEnvKey, "")
}

func TestParseMemoryQuantityKubernetesForms(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{"2Gi", 2 << 30, true},
		{"2G", 2 << 30, true},
		{"2GiB", 2 << 30, true},
		{"1536Mi", 1536 << 20, true},
		{"512Ki", 512 << 10, true},
		{"1.5Gi", 3 << 29, true},
		{"3221225472", 3221225472, true},
		{" 4Gi ", 4 << 30, true},
		{"4g", 4 << 30, true},
		{"", 0, false},
		{"2Xi", 0, false},
		{"lots", 0, false},
		{"Gi", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseMemoryQuantity(tc.raw)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("ParseMemoryQuantity(%q) = (%d, %v), want (%d, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFormatMemoryBytes(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "unset"},
		{2 << 30, "2Gi"},
		{1536 << 20, "1536Mi"},
		{512 << 10, "512Ki"},
		{3*1024 + 512, "3584"},
	}
	for _, tc := range cases {
		if got := FormatMemoryBytes(tc.bytes); got != tc.want {
			t.Fatalf("FormatMemoryBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// TestResolveSandboxMemoryBudgetUnmeasuredAppliesNoBound is the "do not change
// production limits without numbers" guard: with nothing measured, no
// memory-derived bound may appear.
func TestResolveSandboxMemoryBudgetUnmeasuredAppliesNoBound(t *testing.T) {
	clearSandboxMemoryEnv(t)
	budget := ResolveSandboxMemoryBudget(nil)
	if budget.MaxAgentsByMemory != 0 {
		t.Fatalf("MaxAgentsByMemory = %d, want 0 without a measured request/node pair", budget.MaxAgentsByMemory)
	}
	if budget.EffectiveAgentsPerPool != DefaultProviderMaxConcurrency {
		t.Fatalf("EffectiveAgentsPerPool = %d, want the provider default %d", budget.EffectiveAgentsPerPool, DefaultProviderMaxConcurrency)
	}
	if budget.Source != "unmeasured" {
		t.Fatalf("Source = %q, want \"unmeasured\"", budget.Source)
	}
}

// TestResolveSandboxMemoryBudgetEnvBeatsMaterialisedDefault mirrors the ci#620
// regression: the shared-config default merge materialises a literal, so the
// deployment lever must still win.
func TestResolveSandboxMemoryBudgetEnvBeatsMaterialisedDefault(t *testing.T) {
	clearSandboxMemoryEnv(t)
	t.Setenv(SandboxMemoryRequestEnvKey, "2Gi")
	t.Setenv(NodeAllocatableMemoryEnvKey, "16Gi")

	config := sandboxMemoryConfig(
		"coder_sandbox_memory_request", "512Mi",
		"coder_sandbox_node_allocatable_memory", "4Gi",
	)
	budget := ResolveSandboxMemoryBudget(config)
	if budget.PerSandboxRequest != 2<<30 {
		t.Fatalf("PerSandboxRequest = %d, want the env value 2Gi", budget.PerSandboxRequest)
	}
	if budget.NodeAllocatable != 16<<30 {
		t.Fatalf("NodeAllocatable = %d, want the env value 16Gi", budget.NodeAllocatable)
	}
	if !strings.Contains(budget.Source, "env:"+SandboxMemoryRequestEnvKey) {
		t.Fatalf("Source = %q, want it to name the env lever", budget.Source)
	}
}

// TestResolveSandboxMemoryBudgetAgentConfigWhenEnvUnset keeps the per-agent
// shared config working for a deployment that does not set the levers.
func TestResolveSandboxMemoryBudgetAgentConfigWhenEnvUnset(t *testing.T) {
	clearSandboxMemoryEnv(t)
	config := sandboxMemoryConfig(
		"coder_sandbox_memory_request", "1Gi",
		"coder_sandbox_memory_limit", "3Gi",
		"coder_sandbox_node_allocatable_memory", "8Gi",
		"coder_sandbox_memory_reserve", "1Gi",
	)
	budget := ResolveSandboxMemoryBudget(config)
	if budget.PerSandboxRequest != 1<<30 || budget.PerSandboxLimit != 3<<30 {
		t.Fatalf("request/limit = %d/%d, want 1Gi/3Gi", budget.PerSandboxRequest, budget.PerSandboxLimit)
	}
	// usable = 8Gi - 1Gi = 7Gi; 7 sandboxes fit, one is left as headroom.
	if budget.MaxAgentsByMemory != 6 {
		t.Fatalf("MaxAgentsByMemory = %d, want 6 (7 that fit minus one headroom sandbox)", budget.MaxAgentsByMemory)
	}
	if !strings.Contains(budget.Source, "memory_bound") {
		t.Fatalf("Source = %q, want it to record the memory bound", budget.Source)
	}
}

// TestResolveSandboxMemoryBudgetBoundsEffectiveAgentsPerPool is the actual
// "cap the pool so memory and concurrency scale together" assertion: the
// memory-derived count wins over a greedier configured/provider cap.
func TestResolveSandboxMemoryBudgetBoundsEffectiveAgentsPerPool(t *testing.T) {
	clearSandboxMemoryEnv(t)
	t.Setenv(ProviderMaxConcurrencyEnvKey, "12")
	config := sandboxMemoryConfig(
		"coder_sandbox_memory_request", "3Gi",
		"coder_sandbox_node_allocatable_memory", "16Gi",
		"coder_sandbox_memory_reserve", "1Gi",
		"coder_sandbox_max_agents_per_pool", 8,
	)
	budget := ResolveSandboxMemoryBudget(config)
	// usable = 15Gi; 5 requests fit, minus one headroom sandbox = 4.
	if budget.MaxAgentsByMemory != 4 {
		t.Fatalf("MaxAgentsByMemory = %d, want 4", budget.MaxAgentsByMemory)
	}
	if budget.EffectiveAgentsPerPool != 4 {
		t.Fatalf("EffectiveAgentsPerPool = %d, want the memory bound 4 (configured 8/provider 12 are greedier)", budget.EffectiveAgentsPerPool)
	}
	if budget.HeadroomBytes != 15<<30-4*(3<<30) {
		t.Fatalf("HeadroomBytes = %d, want usable - effective*request", budget.HeadroomBytes)
	}
	if budget.ConfiguredAgentsPerPool != 8 || budget.ProviderMaxConcurrency != 12 {
		t.Fatalf("configured/provider = %d/%d, want 8/12 reported alongside the effective bound", budget.ConfiguredAgentsPerPool, budget.ProviderMaxConcurrency)
	}
}

// TestResolveSandboxMemoryBudgetKeepsTighterConfiguredBound proves the memory
// bound only ever lowers the effective cap, never raises it past ci#602/ci#620.
func TestResolveSandboxMemoryBudgetKeepsTighterConfiguredBound(t *testing.T) {
	clearSandboxMemoryEnv(t)
	config := sandboxMemoryConfig(
		"coder_sandbox_memory_request", "1Gi",
		"coder_sandbox_node_allocatable_memory", "64Gi",
		"coder_sandbox_max_agents_per_pool", 2,
		"coder_provider_max_concurrency", 3,
	)
	budget := ResolveSandboxMemoryBudget(config)
	if budget.MaxAgentsByMemory < 2 {
		t.Fatalf("MaxAgentsByMemory = %d, want the node to fit several sandboxes", budget.MaxAgentsByMemory)
	}
	if budget.EffectiveAgentsPerPool != 2 {
		t.Fatalf("EffectiveAgentsPerPool = %d, want the tighter configured pool bound 2", budget.EffectiveAgentsPerPool)
	}
}

// TestResolveSandboxMemoryBudgetDefaultReserve pins the documented fallback: a
// measured node allocatable without an explicit reserve keeps 10% off the top.
func TestResolveSandboxMemoryBudgetDefaultReserve(t *testing.T) {
	clearSandboxMemoryEnv(t)
	config := sandboxMemoryConfig(
		"coder_sandbox_memory_request", "1Gi",
		"coder_sandbox_node_allocatable_memory", "10Gi",
	)
	budget := ResolveSandboxMemoryBudget(config)
	if budget.Reserve != 1<<30 {
		t.Fatalf("Reserve = %d, want 10%% of 10Gi", budget.Reserve)
	}
	if budget.UsableBytes != 9<<30 {
		t.Fatalf("UsableBytes = %d, want 9Gi", budget.UsableBytes)
	}
	// 9 fit, minus one headroom sandbox = 8.
	if budget.MaxAgentsByMemory != 8 {
		t.Fatalf("MaxAgentsByMemory = %d, want 8", budget.MaxAgentsByMemory)
	}
	if !strings.Contains(budget.Source, "reserve=default:10%") {
		t.Fatalf("Source = %q, want the default reserve to be recorded", budget.Source)
	}
}

// TestSandboxMemoryBudgetReportLines is the CI-record half: the measurement
// must be printable without any secret and must state "unset" when a number was
// never measured.
func TestSandboxMemoryBudgetReportLines(t *testing.T) {
	clearSandboxMemoryEnv(t)
	unmeasured := ResolveSandboxMemoryBudget(nil)
	lines := unmeasured.ReportLines()
	if len(lines) != 4 {
		t.Fatalf("ReportLines() = %d lines, want 4", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "per_sandbox_request=unset") || !strings.Contains(joined, "node_allocatable=unset") {
		t.Fatalf("unmeasured report must say unset:\n%s", joined)
	}
	if !strings.Contains(joined, "source=unmeasured") {
		t.Fatalf("unmeasured report must carry the source:\n%s", joined)
	}

	t.Setenv(SandboxMemoryRequestEnvKey, "2Gi")
	t.Setenv(NodeAllocatableMemoryEnvKey, "16Gi")
	measured := strings.Join(ResolveSandboxMemoryBudget(nil).ReportLines(), "\n")
	if !strings.Contains(measured, "per_sandbox_request=2Gi") || !strings.Contains(measured, "node_allocatable=16Gi") {
		t.Fatalf("measured report must carry the quantities:\n%s", measured)
	}
	if !measuredIsSecretFree(measured) {
		t.Fatalf("report contains something key-like:\n%s", measured)
	}
}

func measuredIsSecretFree(report string) bool {
	lowered := strings.ToLower(report)
	for _, marker := range []string{"key", "token", "secret", "password"} {
		if strings.Contains(lowered, marker) {
			return false
		}
	}
	return true
}
