package msgmate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// This file is the **memory** half of the sandbox-pool sizing policy (ci#662,
// the sibling of the CPU knee of ci#632 and of the provider in-flight cap of
// ci#620).
//
// ci#529 showed the failure mode: a per-chat Kubernetes sandbox pod carries its
// own memory request, the node runs out of requests, and the scheduler rejects
// the *next* sandbox ("Insufficient memory") — while the DB row for the chat is
// already there. Raising concurrency (ci#652, MAX_CONCURRENT=4) multiplies that
// risk by the number of concurrent agents, so concurrency must be derived from
// the measured memory budget instead of being guessed.
//
// The policy implemented here is deliberately **measure first, bound second,
// fail open**:
//
//  1. every number comes from configuration or from a measurement that landed
//     in the CI record — the reaper/report workflow fills in the node
//     allocatable memory and the per-sandbox request/limit (see the ci#662
//     measurement step). Nothing is hard-coded and **no production limit is
//     changed** by this file: an unset number means the bound is not applied.
//  2. when the numbers *are* known, the number of agents packed onto one pool
//     is bounded by how many per-sandbox memory requests fit into the node's
//     usable memory (allocatable minus reserve) with the last sandbox left out
//     as headroom, and additionally by the configured agents-per-pool and the
//     ci#620 provider in-flight cap, so memory and concurrency scale together.
//  3. when a number is missing the bound is skipped and the resolved source
//     says so, because an unmeasured deployment must keep working (an operator
//     reads `Source` off the admin surface and then supplies the measurement).
//
// Keys: the per-agent shared-config keys `coder_sandbox_memory_request`,
// `coder_sandbox_memory_limit`, `coder_sandbox_node_allocatable_memory`,
// `coder_sandbox_memory_reserve` and `coder_sandbox_max_agents_per_pool`
// (ci#602), overridable deployment-wide through the `OCI_MSGMATE_CODER_*`
// environment levers below — env wins, for the same reason as in ci#620: the
// shared-config default merge materialises a literal into every agent config
// and would otherwise shadow the deployment lever forever.

const (
	// SandboxMemoryRequestEnvKey is the deployment-wide per-sandbox memory
	// request (a Kubernetes quantity: "2Gi", "1536Mi", bytes).
	SandboxMemoryRequestEnvKey = "OCI_MSGMATE_CODER_SANDBOX_MEMORY_REQUEST"
	// SandboxMemoryLimitEnvKey is the deployment-wide per-sandbox memory limit.
	SandboxMemoryLimitEnvKey = "OCI_MSGMATE_CODER_SANDBOX_MEMORY_LIMIT"
	// NodeAllocatableMemoryEnvKey is the measured allocatable memory of the node
	// (or of the pool's node group) the sandboxes land on.
	NodeAllocatableMemoryEnvKey = "OCI_MSGMATE_CODER_NODE_ALLOCATABLE_MEMORY"
	// SandboxMemoryReserveEnvKey is the memory reserved for non-sandbox
	// workloads and system daemons on that node.
	SandboxMemoryReserveEnvKey = "OCI_MSGMATE_CODER_SANDBOX_MEMORY_RESERVE"
)

const (
	sandboxMemoryRequestConfigKey      = "coder_sandbox_memory_request"
	sandboxMemoryLimitConfigKey        = "coder_sandbox_memory_limit"
	sandboxNodeAllocatableConfigKey    = "coder_sandbox_node_allocatable_memory"
	sandboxMemoryReserveConfigKey      = "coder_sandbox_memory_reserve"
	sandboxAgentsPerPoolConfigKey      = providerPoolBoundConfigKey
	sandboxDefaultMemoryReservePercent = 10
)

// SandboxMemoryBudget is the resolved, operator-readable memory budget of the
// sandbox pool. All byte fields are bytes; a zero byte field means "unknown /
// not measured", never "zero memory".
type SandboxMemoryBudget struct {
	PerSandboxRequest int64 `json:"per_sandbox_request_bytes"`
	PerSandboxLimit   int64 `json:"per_sandbox_limit_bytes"`
	NodeAllocatable   int64 `json:"node_allocatable_bytes"`
	Reserve           int64 `json:"reserve_bytes"`
	// UsableBytes is allocatable minus reserve, the memory the pool may request.
	UsableBytes int64 `json:"usable_bytes"`
	// MaxAgentsByMemory is how many per-sandbox requests fit into UsableBytes
	// minus one sandbox of headroom (0 when the numbers are unknown).
	MaxAgentsByMemory int `json:"max_agents_by_memory"`
	// ConfiguredAgentsPerPool is the ci#602 bound (0 when unset).
	ConfiguredAgentsPerPool int `json:"configured_agents_per_pool"`
	// ProviderMaxConcurrency is the ci#620 provider in-flight cap.
	ProviderMaxConcurrency int `json:"provider_max_concurrency"`
	// EffectiveAgentsPerPool is the number of agents one pool pod may run:
	// the minimum of the configured, the memory-derived and the provider bounds.
	EffectiveAgentsPerPool int `json:"effective_agents_per_pool"`
	// HeadroomBytes is what is left of UsableBytes after
	// EffectiveAgentsPerPool * PerSandboxRequest.
	HeadroomBytes int64 `json:"headroom_bytes"`
	// Source names the values that actually won, so a status line can say why
	// the effective bound is what it is.
	Source string `json:"source"`
}

// String renders the budget as one operator-readable line.
func (b SandboxMemoryBudget) String() string {
	return fmt.Sprintf(
		"per_sandbox_request=%s per_sandbox_limit=%s node_allocatable=%s reserve=%s usable=%s max_agents_by_memory=%d configured_agents_per_pool=%d provider_max_concurrency=%d effective_agents_per_pool=%d headroom=%s source=%s",
		FormatMemoryBytes(b.PerSandboxRequest), FormatMemoryBytes(b.PerSandboxLimit),
		FormatMemoryBytes(b.NodeAllocatable), FormatMemoryBytes(b.Reserve),
		FormatMemoryBytes(b.UsableBytes), b.MaxAgentsByMemory,
		b.ConfiguredAgentsPerPool, b.ProviderMaxConcurrency, b.EffectiveAgentsPerPool,
		FormatMemoryBytes(b.HeadroomBytes), b.Source,
	)
}

// ReportLines renders the measurement/inventory block ci#662 asks for: the
// per-sandbox request/limit, the node's allocatable memory, how many sandboxes
// fit and what headroom is left. It is the core-side half of the CI report and
// contains no secret.
func (b SandboxMemoryBudget) ReportLines() []string {
	return []string{
		fmt.Sprintf("sandbox_memory per_sandbox_request=%s per_sandbox_limit=%s", FormatMemoryBytes(b.PerSandboxRequest), FormatMemoryBytes(b.PerSandboxLimit)),
		fmt.Sprintf("sandbox_memory node_allocatable=%s reserve=%s usable=%s", FormatMemoryBytes(b.NodeAllocatable), FormatMemoryBytes(b.Reserve), FormatMemoryBytes(b.UsableBytes)),
		fmt.Sprintf("sandbox_memory max_agents_by_memory=%d configured_agents_per_pool=%d provider_max_concurrency=%d effective_agents_per_pool=%d headroom=%s",
			b.MaxAgentsByMemory, b.ConfiguredAgentsPerPool, b.ProviderMaxConcurrency, b.EffectiveAgentsPerPool, FormatMemoryBytes(b.HeadroomBytes)),
		fmt.Sprintf("sandbox_memory source=%s", b.Source),
	}
}

// FormatMemoryBytes renders a byte count as a short human/CI-readable quantity
// ("2Gi", "1536Mi", "512B"); zero renders as "unset".
func FormatMemoryBytes(bytes int64) string {
	if bytes <= 0 {
		return "unset"
	}
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case bytes%gib == 0:
		return strconv.FormatInt(bytes/gib, 10) + "Gi"
	case bytes%mib == 0:
		return strconv.FormatInt(bytes/mib, 10) + "Mi"
	case bytes%kib == 0:
		return strconv.FormatInt(bytes/kib, 10) + "Ki"
	}
	return strconv.FormatInt(bytes, 10)
}

// ParseMemoryQuantity parses a Kubernetes-style memory quantity into bytes. It
// accepts the binary suffixes (Ki/Mi/Gi/Ti/Pi/Ei), the decimal suffix forms
// (k/M/G/T, plus the plain "K"), an optional "B"/"iB" tail and a bare byte
// count. It is case-insensitive on the suffix, which is what a hand-written
// shared config or a kubectl-derived value looks like.
func ParseMemoryQuantity(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	value = strings.TrimSuffix(value, "B")
	value = strings.TrimSuffix(value, "i") // "GiB" -> "G", "Gi" -> "G"
	if value == "" {
		return 0, false
	}

	multiplier := int64(1)
	last := value[len(value)-1]
	switch {
	case last >= '0' && last <= '9':
		// bare bytes
	default:
		switch strings.ToUpper(string(last)) {
		case "K":
			multiplier = 1024
		case "M":
			multiplier = 1024 * 1024
		case "G":
			multiplier = 1024 * 1024 * 1024
		case "T":
			multiplier = 1024 * 1024 * 1024 * 1024
		case "P":
			multiplier = 1024 * 1024 * 1024 * 1024 * 1024
		case "E":
			multiplier = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
		default:
			return 0, false
		}
		value = value[:len(value)-1]
	}

	value = strings.TrimSuffix(strings.TrimSuffix(value, "B"), "i")
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number < 0 {
		return 0, false
	}
	bytes := int64(number * float64(multiplier))
	if bytes <= 0 {
		return 0, false
	}
	return bytes, true
}

// sandboxMemoryQuantity resolves one memory quantity from the deployment env
// (which wins) and then the per-agent shared config.
func sandboxMemoryQuantity(config map[string]interface{}, envKey, configKey string) (int64, string) {
	if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
		if bytes, ok := ParseMemoryQuantity(raw); ok {
			return bytes, "env:" + envKey
		}
	}
	if config != nil {
		if raw, ok := config[configKey].(string); ok {
			if bytes, ok := ParseMemoryQuantity(raw); ok {
				return bytes, "agent:" + configKey
			}
		}
		if number := providerAdmissionConfigInt(config, configKey); number > 0 {
			return int64(number), "agent:" + configKey
		}
	}
	return 0, ""
}

// resolveProviderMaxConcurrencyWithoutPoolBound reads the ci#620 provider cap
// *without* folding in the ci#602 pool bound, so the memory budget can report
// the three bounds it takes the minimum of (configured pool, provider,
// memory) separately instead of re-using one that already contains another.
func resolveProviderMaxConcurrencyWithoutPoolBound(config map[string]interface{}) int {
	if envMax, ok := providerAdmissionEnvInt(ProviderMaxConcurrencyEnvKey); ok {
		return envMax
	}
	if agentMax := providerAdmissionConfigInt(config, providerMaxConcurrencyConfigKey); agentMax > 0 {
		return agentMax
	}
	return DefaultProviderMaxConcurrency
}

// ResolveSandboxMemoryBudget resolves the pool's memory budget (ci#662). It is
// fail-open: a missing per-sandbox request or node measurement leaves the
// configured agents-per-pool (and the ci#620 provider cap) in force, because
// an unmeasured deployment must not be bricked by a guess. The numbers the
// reaper/report workflow measures are what turn the bound on.
func ResolveSandboxMemoryBudget(config map[string]interface{}) SandboxMemoryBudget {
	budget := SandboxMemoryBudget{}
	sources := make([]string, 0, 4)

	if bytes, source := sandboxMemoryQuantity(config, SandboxMemoryRequestEnvKey, sandboxMemoryRequestConfigKey); bytes > 0 {
		budget.PerSandboxRequest = bytes
		sources = append(sources, source)
	}
	if bytes, source := sandboxMemoryQuantity(config, SandboxMemoryLimitEnvKey, sandboxMemoryLimitConfigKey); bytes > 0 {
		budget.PerSandboxLimit = bytes
		sources = append(sources, source)
	}
	if bytes, source := sandboxMemoryQuantity(config, NodeAllocatableMemoryEnvKey, sandboxNodeAllocatableConfigKey); bytes > 0 {
		budget.NodeAllocatable = bytes
		sources = append(sources, source)
	}
	if bytes, source := sandboxMemoryQuantity(config, SandboxMemoryReserveEnvKey, sandboxMemoryReserveConfigKey); bytes > 0 {
		budget.Reserve = bytes
		sources = append(sources, source)
	}

	budget.ConfiguredAgentsPerPool = providerAdmissionConfigInt(config, sandboxAgentsPerPoolConfigKey)
	budget.ProviderMaxConcurrency = resolveProviderMaxConcurrencyWithoutPoolBound(config)

	budget.UsableBytes = budget.NodeAllocatable - budget.Reserve
	if budget.NodeAllocatable > 0 && budget.Reserve <= 0 {
		// No explicit reserve: keep a documented percentage off the top instead
		// of packing the node to its last byte.
		budget.Reserve = budget.NodeAllocatable * sandboxDefaultMemoryReservePercent / 100
		budget.UsableBytes = budget.NodeAllocatable - budget.Reserve
		sources = append(sources, fmt.Sprintf("reserve=default:%d%%", sandboxDefaultMemoryReservePercent))
	}
	if budget.UsableBytes < 0 {
		budget.UsableBytes = 0
	}

	if budget.PerSandboxRequest > 0 && budget.UsableBytes > 0 {
		// One sandbox of headroom is left out of the packed count, so the pool
		// can still admit a *new* chat while every agent of the current pool is
		// running its sandbox.
		budget.MaxAgentsByMemory = int(budget.UsableBytes / budget.PerSandboxRequest)
		if budget.MaxAgentsByMemory > 1 {
			budget.MaxAgentsByMemory--
		}
		sources = append(sources, "memory_bound")
	}

	budget.EffectiveAgentsPerPool = budget.ConfiguredAgentsPerPool
	if budget.EffectiveAgentsPerPool <= 0 {
		// No configured pool bound: the provider cap is the only known limit, so
		// the memory bound alone decides how far concurrency may go.
		budget.EffectiveAgentsPerPool = budget.ProviderMaxConcurrency
	}
	if budget.MaxAgentsByMemory > 0 && budget.MaxAgentsByMemory < budget.EffectiveAgentsPerPool {
		budget.EffectiveAgentsPerPool = budget.MaxAgentsByMemory
	}
	if budget.ProviderMaxConcurrency > 0 && budget.ProviderMaxConcurrency < budget.EffectiveAgentsPerPool {
		budget.EffectiveAgentsPerPool = budget.ProviderMaxConcurrency
	}
	if budget.EffectiveAgentsPerPool < 0 {
		budget.EffectiveAgentsPerPool = 0
	}

	if budget.UsableBytes > 0 {
		budget.HeadroomBytes = budget.UsableBytes - int64(budget.EffectiveAgentsPerPool)*budget.PerSandboxRequest
		if budget.HeadroomBytes < 0 {
			budget.HeadroomBytes = 0
		}
	}

	if len(sources) == 0 {
		budget.Source = "unmeasured"
	} else {
		budget.Source = strings.Join(sources, "+")
	}
	return budget
}
