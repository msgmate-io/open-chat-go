// Package sandboxnetwork is the core-side networking & isolation policy for
// coding-agent sandboxes (ci#546, the sibling of the shared/pooled sandbox mode
// of ci#602).
//
// The failure mode this package exists to prevent: several coding agents share
// ONE sandbox pod. A pod shares its **network namespace**, so two workspaces
// that bind the same fixed dev-server port (3000, 8080, ...) collide, and
// `localhost:<port>` stops meaning "my workspace's server". The conflicts are
// silent at bind time (the second server dies or, worse, serves the other
// agent's traffic) and only show up as a flaky test far away from the cause.
//
// The policy is deliberately **isolate first, bound second, fail-open into the
// dedicated-pod semantics**:
//
//  1. the default mode is `isolated`: a dedicated per-chat sandbox already has
//     its own network namespace, so nothing changes for every existing
//     deployment and no port is ever allocated;
//  2. a shared/pooled pod must opt in explicitly (`coder_sandbox_mode: shared`)
//     and then gets one of two *bounded* policies, never an unbounded shared
//     netns:
//     - `loopback_alias`: every slot owns a distinct loopback address
//     (`127.0.0.<slot+2>`, so `127.0.0.1` stays the pod's plain loopback), so
//     `localhost:<port>` becomes
//     `$OPENCHAT_SANDBOX_LOOPBACK:<port>` and two workspaces may keep their
//     conventional ports;
//     - `shared_ports`: one netns with a **deterministic per-slot port slice**
//     (`base + slot*stride .. +stride-1`) plus injected env, so an agent binds
//     an allocated port instead of guessing;
//  3. a shared pool that names no bounded policy is not left to chance: the
//     resolver materialises the `shared_ports` defaults (documented base and
//     stride) and reports a warning, so the worst case is a bounded, visible
//     policy instead of a runtime collision;
//  4. nothing here provisions anything: the sandbox integration owns the pod,
//     the netns/unshare call and the backend implementation. This package
//     resolves *what the workspace must do* and *which env it gets*, which is
//     the part the core can decide and the part that must be reproducible and
//     unit-testable.
//
// Secrets never enter this package: it only ever handles slot numbers, ports
// and public addresses.
package sandboxnetwork

import (
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Network modes.
const (
	// ModeIsolated gives the workspace its own network namespace (a dedicated
	// per-chat sandbox, or `slirp4netns`/user-netns inside a shared pod).
	// `localhost` is the workspace's alone and no port is allocated.
	ModeIsolated = "isolated"
	// ModeLoopbackAlias shares the pod netns but gives the slot its own
	// loopback address, leaving conventional ports usable.
	ModeLoopbackAlias = "loopback_alias"
	// ModeSharedPorts shares the pod netns *and* `localhost`, so the slot gets a
	// bounded slice of ports it must bind instead of its conventional ones.
	ModeSharedPorts = "shared_ports"
)

// Defaults. The port window is high and rarely used, so it is unlikely to
// collide with something already bound in the sandbox image.
const (
	// DefaultMode keeps every existing deployment on dedicated-pod semantics.
	DefaultMode = ModeIsolated
	// DefaultSharedMode is what a shared pool without an explicit policy gets:
	// bounded port allocation, which is the only mode that cannot silently
	// break `localhost`.
	DefaultSharedMode = ModeSharedPorts
	// DefaultPortBase is the first port of the pool's allocated window.
	DefaultPortBase = 31000
	// DefaultPortStride is how many ports one slot owns.
	DefaultPortStride = 200
	// DefaultSlotCount bounds how many workspaces one pool pod may host when no
	// `coder_sandbox_max_shared_workspaces`/`..._max_agents_per_pool` bound is
	// configured. It matches the ci#602 default pool bound intent: a small pool.
	DefaultSlotCount = 4
	// MaxSlotCount is the hard cap: one dedicated loopback /24 plus one port
	// window cannot address more, and a bigger pool must be split into pools.
	MaxSlotCount = 253
	// MinPort is the first unprivileged port; a slice below it cannot be bound
	// by the non-root sandbox user.
	MinPort = 1024
	// MaxPort is the last usable TCP/UDP port.
	MaxPort = 65535
	// UnassignedSlot marks a workspace without a resolved slot.
	UnassignedSlot = -1
	// MaxConfigInt bounds every numeric config/env lever before it is read, so
	// an operator typo (or a hostile value) cannot overflow the 32 bit
	// arithmetic of the port/slot math. It is far above every legal lever
	// (port <= 65535, slot count <= 253) and a single digit wider than a port.
	MaxConfigInt = 1 << 24
)

// Environment variables injected into the workspace and read by the coding
// tools / dev servers, plus the deployment-wide levers. These are the
// documented, stable surface of the policy: a dev server in the sandbox reads
// OPENCHAT_SANDBOX_PORT_MIN/MAX instead of hard-coding 3000.
const (
	// EnvNetworkMode names the resolved mode.
	EnvNetworkMode = "OPENCHAT_SANDBOX_NETWORK_MODE"
	// EnvSlot is the workspace's slot index inside the pool.
	EnvSlot = "OPENCHAT_SANDBOX_NETWORK_SLOT"
	// EnvNetns is "private" (isolated) or "shared" (pool netns).
	EnvNetns = "OPENCHAT_SANDBOX_NETNS"
	// EnvLoopback is the address `localhost` means for this workspace.
	EnvLoopback = "OPENCHAT_SANDBOX_LOOPBACK"
	// EnvPortBase / EnvPortMin / EnvPortMax bound the ports the workspace owns.
	EnvPortBase = "OPENCHAT_SANDBOX_PORT_BASE"
	EnvPortMin  = "OPENCHAT_SANDBOX_PORT_MIN"
	EnvPortMax  = "OPENCHAT_SANDBOX_PORT_MAX"
	// EnvPortStride is how many ports the slice holds.
	EnvPortStride = "OPENCHAT_SANDBOX_PORT_STRIDE"
	// EnvTargetAddr is the SSH/target address of the *pool pod* the coder binds
	// to. Shared mode changes what this address means: it is the pool, not the
	// chat, and the workspace path differentiates the chat (see the design note).
	EnvTargetAddr = "OPENCHAT_SANDBOX_TARGET_ADDR"

	// EnvNetworkModeLever / EnvPortBaseLever / EnvPortStrideLever / EnvSlotCountLever
	// are the deployment-wide overrides; env wins over the per-agent shared
	// config for the same reason as ci#620/ci#662: the shared-config default
	// merge materialises a literal into every agent config and would otherwise
	// shadow the deployment lever forever.
	EnvNetworkModeLever = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_MODE"
	EnvPortBaseLever    = "OCI_MSGMATE_CODER_SANDBOX_PORT_BASE"
	EnvPortStrideLever  = "OCI_MSGMATE_CODER_SANDBOX_PORT_STRIDE"
	EnvSlotCountLever   = "OCI_MSGMATE_CODER_SANDBOX_NETWORK_SLOTS"
)

// Per-agent shared-config keys.
const (
	// ConfigNetworkMode is the explicit mode (`isolated`/`loopback_alias`/
	// `shared_ports`).
	ConfigNetworkMode = "coder_sandbox_network_mode"
	// ConfigPortBase / ConfigPortStride size the port window.
	ConfigPortBase   = "coder_sandbox_port_base"
	ConfigPortStride = "coder_sandbox_port_stride"
	// ConfigSharedMode is the pooled-sandbox switch owned by the sibling ticket
	// (ci#602); a shared pool is what makes a networking policy necessary.
	ConfigSharedMode = "coder_sandbox_mode"
	// ConfigMaxSharedWorkspaces / ConfigMaxAgentsPerPool bound how many
	// workspaces one pool pod hosts, ie how many slots must be allocated.
	ConfigMaxSharedWorkspaces = "coder_sandbox_max_shared_workspaces"
	ConfigMaxAgentsPerPool    = "coder_sandbox_max_agents_per_pool"
	// ConfigRequireIsolation forces the isolated mode for this deployment/agent
	// and makes a shared/pooled sandbox a configuration error instead of a
	// silently weakened isolation boundary (sensitive repositories).
	ConfigRequireIsolation = "coder_sandbox_require_network_isolation"
	// ConfigIsolatedRepos lists repository slugs that may never share a netns.
	ConfigIsolatedRepos = "coder_sandbox_isolated_repos"
)

// Policy is the resolved networking policy of one workspace. It is
// operator-readable (`String`, `ReportLines`) and carries no secret.
type Policy struct {
	Mode string `json:"mode"`
	// Slot is the workspace's slot inside the pool (UnassignedSlot when the
	// workspace is isolated or the pool size is unknown).
	Slot int `json:"slot"`
	// SlotCount is how many workspaces one pool pod hosts.
	SlotCount int `json:"slot_count"`
	// Loopback is the address `localhost` means for this workspace.
	Loopback string `json:"loopback"`
	// Netns is "private" or "shared".
	Netns string `json:"netns"`
	// PortBase is the pool window's first port (0 = no allocation).
	PortBase int `json:"port_base"`
	// PortStride is how many ports one slot owns.
	PortStride int `json:"port_stride"`
	// PortMin / PortMax are the first/last port this workspace may bind.
	PortMin int `json:"port_min"`
	PortMax int `json:"port_max"`
	// Env is the injected workspace environment (public values only).
	Env map[string]string `json:"env"`
	// Warnings names every degraded decision (missing bound, clamped slot, a
	// shared pool without an explicit mode), so a status line can say why.
	Warnings []string `json:"warnings,omitempty"`
	// Source names the values that won, e.g. "env:OCI_..._NETWORK_MODE+slot:3".
	Source string `json:"source"`
}

// Isolated reports whether the workspace keeps its own network namespace.
func (p Policy) Isolated() bool { return p.Mode == ModeIsolated }

// Shared reports whether the workspace shares the pool's network namespace.
func (p Policy) Shared() bool { return !p.Isolated() }

// String renders the policy as one operator-readable line.
func (p Policy) String() string {
	line := fmt.Sprintf(
		"mode=%s netns=%s slot=%d/%d loopback=%s ports=%d..%d (base=%d stride=%d) source=%s",
		p.Mode, p.Netns, p.Slot, p.SlotCount, p.Loopback,
		p.PortMin, p.PortMax, p.PortBase, p.PortStride, p.Source,
	)
	if len(p.Warnings) > 0 {
		line += " warnings=" + strings.Join(p.Warnings, ",")
	}
	return line
}

// ReportLines renders the CI/status block for the policy; it never contains a
// credential, only addresses and ports of the shared, ephemeral dev sandbox.
func (p Policy) ReportLines() []string {
	lines := []string{
		fmt.Sprintf("sandbox_network mode=%s netns=%s slot=%d slot_count=%d", p.Mode, p.Netns, p.Slot, p.SlotCount),
		fmt.Sprintf("sandbox_network loopback=%s port_base=%d port_stride=%d port_range=%d..%d", p.Loopback, p.PortBase, p.PortStride, p.PortMin, p.PortMax),
		fmt.Sprintf("sandbox_network isolation=%s source=%s", p.IsolationVerdict(), p.Source),
	}
	for _, warning := range p.Warnings {
		lines = append(lines, "sandbox_network warning="+warning)
	}
	return lines
}

// IsolationVerdict names the isolation boundary the workspace actually has, so
// an operator can tell the ephemeral dev pool apart from a dedicated pod.
func (p Policy) IsolationVerdict() string {
	switch {
	case p.Mode == ModeIsolated:
		return "own-netns"
	case p.Mode == ModeLoopbackAlias:
		return "shared-netns-own-loopback"
	default:
		return "shared-netns-shared-localhost"
	}
}

// PortFor maps a conventional fixed port (3000, 8080, ...) onto this
// workspace's slice, so an agent that *wants* port 3000 binds `PortFor(3000)`
// and stays inside its allocation. An isolated workspace always returns the
// conventional port unchanged: nothing is shared, so nothing needs moving.
//
// The mapping is stable for a given (policy, port) pair and injective inside one
// slice as long as the requested ports are distinct modulo the stride, which is
// the documented contract for a pool.
func (p Policy) PortFor(conventional int) int {
	if p.Isolated() || p.PortStride <= 0 || p.PortMin <= 0 {
		return conventional
	}
	offset := 0
	if conventional > 0 {
		offset = conventional % p.PortStride
	}
	port := p.PortMin + offset
	if port > p.PortMax {
		port = p.PortMax
	}
	return port
}

// EnvFor returns the environment to inject into the workspace, merging the
// policy's own values into base (base wins on conflict, so an explicit operator
// value is never overwritten); a nil base yields just the policy values.
func (p Policy) EnvFor(base map[string]string) map[string]string {
	merged := make(map[string]string, len(p.Env)+len(base)+1)
	for key, value := range p.Env {
		merged[key] = value
	}
	for key, value := range base {
		merged[key] = value
	}
	return merged
}

// Resolve resolves the networking policy of the workspace identified by
// workspaceKey (typically the workspace path, which is what differentiates two
// chats in one pool pod).
func Resolve(config map[string]interface{}, workspaceKey string) Policy {
	return ResolveWithLookup(config, workspaceKey, os.Getenv)
}

// ResolveWithLookup is Resolve with an injectable environment lookup, so the
// precedence (env → agent config → default) is unit-testable.
func ResolveWithLookup(config map[string]interface{}, workspaceKey string, lookup func(string) string) Policy {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	sources := make([]string, 0, 5)
	policy := Policy{Slot: UnassignedSlot, Netns: "private", Loopback: "127.0.0.1"}

	shared, sharedSource := pooledSandboxRequested(config, lookup)
	if sharedSource != "" {
		sources = append(sources, sharedSource)
	}

	mode, modeSource := resolveMode(config, lookup, shared)
	if modeSource != "" {
		sources = append(sources, modeSource)
	}
	policy.Mode = mode

	policy.SlotCount = resolveSlotCount(config, lookup)
	if policy.SlotCount > MaxSlotCount {
		policy.Warnings = append(policy.Warnings,
			fmt.Sprintf("slot_count %d clamped to %d: split the pool into several pools", policy.SlotCount, MaxSlotCount))
		policy.SlotCount = MaxSlotCount
	}

	if policy.Mode == ModeIsolated {
		policy.PortBase = 0
		policy.PortStride = 0
		policy.PortMin = 0
		policy.PortMax = 0
		policy.Env = isolatedEnv()
		policy.Source = joinSources(sources)
		return policy
	}

	policy.Netns = "shared"
	policy.Slot = SlotFor(workspaceKey, policy.SlotCount)
	if policy.Slot == UnassignedSlot {
		// A workspace without a stable key cannot be assigned an exclusive
		// slot; say so instead of handing it slot 0 and colliding with whoever
		// already holds it.
		policy.Warnings = append(policy.Warnings,
			"no workspace key: the workspace cannot be assigned a pool slot, so its ports and loopback alias are not exclusive")
	}

	policy.PortBase = resolvePortValue(config, lookup, EnvPortBaseLever, ConfigPortBase, DefaultPortBase, &sources, "port_base")
	policy.PortStride = resolvePortValue(config, lookup, EnvPortStrideLever, ConfigPortStride, DefaultPortStride, &sources, "port_stride")
	policy.Loopback = loopbackFor(policy.Slot)

	if policy.Mode == ModeLoopbackAlias {
		// The loopback alias alone makes the workspace exclusive, so its
		// conventional ports stay usable and no slice is consumed.
		policy.PortBase = 0
		policy.PortStride = 0
		policy.PortMin = 0
		policy.PortMax = 0
	} else {
		windowEnd := policy.PortBase + policy.SlotCount*policy.PortStride
		switch {
		case policy.PortBase < MinPort:
			policy.Warnings = append(policy.Warnings,
				fmt.Sprintf("port_base %d is below %d: the sandbox user cannot bind it, using %d", policy.PortBase, MinPort, MinPort))
			policy.PortBase = MinPort
			windowEnd = policy.PortBase + policy.SlotCount*policy.PortStride
		case windowEnd-1 > MaxPort:
			policy.Warnings = append(policy.Warnings,
				fmt.Sprintf("port window %d..%d exceeds %d: the pool does not fit, reduce the stride or the slot count",
					policy.PortBase, windowEnd-1, MaxPort))
		}
		slot := policy.Slot
		if slot < 0 {
			slot = 0
		}
		policy.PortMin = policy.PortBase + slot*policy.PortStride
		policy.PortMax = policy.PortMin + policy.PortStride - 1
		if policy.PortMax > MaxPort {
			policy.PortMax = MaxPort
		}
	}

	policy.Env = sharedEnv(policy)
	policy.Source = joinSources(sources)
	return policy
}

// pooledSandboxRequested reports whether the effective config asks for a shared
// (pooled) sandbox pod, which is what makes a networking policy necessary. A
// bool or a boolean-like string counts; anything else (including the tool-side
// `mode` values) does not.
func pooledSandboxRequested(config map[string]interface{}, lookup func(string) string) (bool, string) {
	if value := strings.TrimSpace(lookup(EnvNetworkModeLever)); falseLike(value) {
		return false, ""
	}
	raw, ok := config[ConfigSharedMode]
	if !ok {
		return false, ""
	}
	switch typed := raw.(type) {
	case bool:
		return typed, "agent:" + ConfigSharedMode
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		switch normalized {
		case "pooled", "shared", "shared_pod", "shared-pod":
			return true, "agent:" + ConfigSharedMode
		case "isolated", "dedicated", "per_chat", "per-chat":
			return false, "agent:" + ConfigSharedMode
		}
	}
	return false, ""
}

// resolveMode resolves the mode with precedence env → agent config → default.
// A pooled sandbox with no explicit mode gets DefaultSharedMode plus a warning,
// because a shared netns is never left unallocated by accident.
func resolveMode(config map[string]interface{}, lookup func(string) string, shared bool) (string, string) {
	if raw := strings.TrimSpace(lookup(EnvNetworkModeLever)); raw != "" {
		if mode, ok := NormalizeMode(raw); ok {
			return mode, "env:" + EnvNetworkModeLever
		}
	}
	if config != nil {
		if raw, ok := config[ConfigNetworkMode].(string); ok {
			if mode, ok := NormalizeMode(raw); ok {
				return mode, "agent:" + ConfigNetworkMode
			}
		}
	}
	if shared {
		return DefaultSharedMode, "default:shared_pool"
	}
	return DefaultMode, ""
}

// NormalizeMode maps a configured mode (allowing a few aliases) onto one of the
// three modes; ok=false for anything unknown, so a typo is never silently
// treated as isolation.
func NormalizeMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ModeIsolated, "netns", "namespace", "own_netns", "own-netns", "private":
		return ModeIsolated, true
	case ModeLoopbackAlias, "loopback", "alias", "loopback-address", "loopback_address":
		return ModeLoopbackAlias, true
	case ModeSharedPorts, "ports", "port_allocation", "port-allocation", "shared":
		return ModeSharedPorts, true
	}
	return "", false
}

// resolveSlotCount resolves how many workspaces one pool pod hosts: the explicit
// ci#602 pool bounds when configured, DefaultSlotCount otherwise. It is the
// number of slots the port window and the loopback aliases must cover.
func resolveSlotCount(config map[string]interface{}, lookup func(string) string) int {
	if number, ok := lookupInt(lookup(EnvSlotCountLever)); ok && number > 0 {
		return number
	}
	for _, key := range []string{ConfigMaxSharedWorkspaces, ConfigMaxAgentsPerPool} {
		if config == nil {
			break
		}
		if number, ok := configInt(config[key]); ok && number > 0 {
			return number
		}
	}
	return DefaultSlotCount
}

// resolvePortValue resolves one positive port number with precedence env →
// agent config → default, recording the winning source.
func resolvePortValue(config map[string]interface{}, lookup func(string) string, envKey, configKey string, fallback int, sources *[]string, label string) int {
	if number, ok := lookupInt(lookup(envKey)); ok && number > 0 {
		*sources = append(*sources, "env:"+envKey)
		return number
	}
	if config != nil {
		if number, ok := configInt(config[configKey]); ok && number > 0 {
			*sources = append(*sources, "agent:"+configKey)
			return number
		}
	}
	*sources = append(*sources, "default:"+label)
	return fallback
}

// SlotFor assigns a stable slot in [0, slotCount) to workspaceKey. The mapping
// is a hash, so two workspaces of one pool land on different slots with high
// probability and the same workspace always lands on the same slot across
// restarts (the coder target binding and the injected env must agree).
// It returns UnassignedSlot for an empty key or a non-positive slot count.
func SlotFor(workspaceKey string, slotCount int) int {
	trimmed := strings.TrimSpace(workspaceKey)
	if trimmed == "" || slotCount <= 0 {
		return UnassignedSlot
	}
	// A 64 bit digest (not the 32 bit sum) so the modulo needs no narrowing
	// conversion of an int to uint32: the digest width always covers slotCount.
	return int(fnvHash64(trimmed) % uint64(slotCount))
}

// fnvHash64 is the stable 64 bit FNV-1a digest of a workspace key.
func fnvHash64(workspaceKey string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(workspaceKey))
	return hasher.Sum64()
}

// loopbackFor renders the slot's loopback address. The whole 127.0.0.0/8 is
// local, so a distinct address per slot gives every workspace an exclusive
// `localhost` without a namespace. The aliases start at 127.0.0.2:
// 127.0.0.1 stays the pod's plain loopback and is therefore never an exclusive
// alias an agent could mistake for its own.
func loopbackFor(slot int) string {
	if slot < 0 {
		return "127.0.0.1"
	}
	return fmt.Sprintf("127.0.0.%d", slot%253+2)
}

// isolatedEnv is the injected environment of an isolated workspace: the
// documented, unchanged localhost semantics, so a dev server needs no change.
func isolatedEnv() map[string]string {
	return map[string]string{
		EnvNetworkMode: ModeIsolated,
		EnvNetns:       "private",
		EnvLoopback:    "127.0.0.1",
	}
}

// sharedEnv is the injected environment of a shared workspace: the mode, the
// slot, the address `localhost` means for it and (for shared_ports) the port
// slice it must bind.
func sharedEnv(policy Policy) map[string]string {
	env := map[string]string{
		EnvNetworkMode: policy.Mode,
		EnvNetns:       policy.Netns,
		EnvLoopback:    policy.Loopback,
		EnvSlot:        strconv.Itoa(policy.Slot),
	}
	if policy.Mode == ModeSharedPorts && policy.PortStride > 0 {
		env[EnvPortBase] = strconv.Itoa(policy.PortBase)
		env[EnvPortStride] = strconv.Itoa(policy.PortStride)
		env[EnvPortMin] = strconv.Itoa(policy.PortMin)
		env[EnvPortMax] = strconv.Itoa(policy.PortMax)
	}
	return env
}

// Windows renders the port windows of every slot of a policy, so an operator (or
// a CI report) can see that the pool's allocations do not overlap. An isolated
// policy has no windows.
func Windows(policy Policy) []string {
	if policy.Isolated() || policy.PortStride <= 0 {
		return nil
	}
	windows := make([]string, 0, policy.SlotCount)
	for slot := 0; slot < policy.SlotCount; slot++ {
		low := policy.PortBase + slot*policy.PortStride
		high := low + policy.PortStride - 1
		if high > MaxPort {
			high = MaxPort
		}
		windows = append(windows, fmt.Sprintf("%d:%d-%d", slot, low, high))
	}
	return windows
}

// Overlaps reports the pairs of slot windows that intersect. It must always be
// empty for a resolved policy; it exists so a hand-written policy (or a future
// allocator) can be checked instead of trusted.
func Overlaps(slotPorts map[int][2]int) []string {
	slots := make([]int, 0, len(slotPorts))
	for slot := range slotPorts {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	conflicts := make([]string, 0)
	for i := 0; i < len(slots); i++ {
		for j := i + 1; j < len(slots); j++ {
			first, second := slotPorts[slots[i]], slotPorts[slots[j]]
			if first[0] <= second[1] && second[0] <= first[1] {
				conflicts = append(conflicts, fmt.Sprintf("slot %d (%d..%d) overlaps slot %d (%d..%d)",
					slots[i], first[0], first[1], slots[j], second[0], second[1]))
			}
		}
	}
	return conflicts
}

// SharedNetnsPermitted is the repo/scope gate: a shared pod's netns lets one
// agent see another agent's processes and traffic, which is acceptable only for
// the ephemeral dev sandbox and never for a sensitive repository. It denies
// when the deployment requires isolation, when the repository is on the
// isolated-repo list, or when the repository slug is not a plain
// `owner/name` (an unknown remote must not be treated as harmless).
// The returned reason is safe to log and to show in the UI.
func SharedNetnsPermitted(config map[string]interface{}, repoSlug string) (bool, string) {
	if config != nil {
		if truthy(config, ConfigRequireIsolation) {
			return false, "shared network namespace denied: " + ConfigRequireIsolation + " is on"
		}
		for _, repo := range stringList(config[ConfigIsolatedRepos]) {
			if strings.EqualFold(strings.TrimSpace(repo), strings.TrimSpace(repoSlug)) {
				return false, "shared network namespace denied: repository " + repoSlug + " is listed in " + ConfigIsolatedRepos
			}
		}
	}
	slug := strings.TrimSpace(repoSlug)
	if !isRepoSlug(slug) {
		return false, "shared network namespace denied: no known repository scope (an unknown remote must not run in a shared pod)"
	}
	return true, ""
}

// isRepoSlug reports whether value looks like a plain `owner/name` slug.
func isRepoSlug(value string) bool {
	if value == "" || strings.ContainsAny(value, " \t:@") || strings.Contains(value, "://") {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	return parts[0] != "" && parts[1] != ""
}

// truthy reads a bool or boolean-like string config value.
func truthy(config map[string]interface{}, key string) bool {
	if config == nil {
		return false
	}
	switch typed := config[key].(type) {
	case bool:
		return typed
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "true" || normalized == "1" || normalized == "yes" || normalized == "on"
	}
	return false
}

// falseLike reports whether a raw env value switches a feature off.
func falseLike(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "off", "no", "disabled":
		return true
	}
	return false
}

// stringList reads a list of strings that may be decoded as []interface{},
// []string, or a single comma-separated string.
func stringList(raw interface{}) []string {
	switch typed := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		return out
	case []string:
		return typed
	case string:
		out := make([]string, 0)
		for _, part := range strings.Split(typed, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

// configInt reads a positive integer that may be decoded as int, float64 or a
// numeric string. Values outside [0, MaxConfigInt] are rejected instead of being
// narrowed, so the port/slot math cannot be driven out of range by config.
func configInt(raw interface{}) (int, bool) {
	switch typed := raw.(type) {
	case int:
		return clampedConfigInt(int64(typed))
	case int64:
		return clampedConfigInt(typed)
	case float64:
		if typed > MaxConfigInt || typed < 0 {
			return 0, false
		}
		return clampedConfigInt(int64(typed))
	case string:
		return lookupInt(typed)
	}
	return 0, false
}

// clampedConfigInt accepts a fully decoded integer only inside [0, MaxConfigInt].
func clampedConfigInt(value int64) (int, bool) {
	if value < 0 || value > MaxConfigInt {
		return 0, false
	}
	return int(value), true
}

// lookupInt parses a numeric string; ok=false for an empty, malformed or
// out-of-range value. The range check keeps every derived port/slot calculation
// inside its 32 bit bounds (see MaxConfigInt).
func lookupInt(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	number, err := strconv.Atoi(trimmed)
	if err != nil || number < 0 || number > MaxConfigInt {
		return 0, false
	}
	return number, true
}

// joinSources renders the resolution provenance; an empty list means the
// defaults decided everything.
func joinSources(sources []string) string {
	if len(sources) == 0 {
		return "default"
	}
	return strings.Join(sources, "+")
}
