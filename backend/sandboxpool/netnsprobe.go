package sandboxpool

// ci#546 / ci#792 - per-workspace network namespace capability probe.
//
// The shared-mode design note (development/sandbox-network-isolation.md) records
// the true isolation option of a shared pod as a per-workspace netns with a
// slirp4netns / user-namespace provider. mode netns already fails closed while
// the pool reports no support (ErrWorkspaceIsolationUnavailable), but until now
// nothing could ever *report* that support: NetworkPolicy.NetnsAvailable was a
// field a caller had to set by hand, so a deployment that really had slirp4netns
// still refused every netns lease, and the failure mode ("mode netns requested
// but this pool reports no per-workspace namespace support") named no cause.
//
// This file is the missing half: a read-only, injectable probe that answers
// "can this pod create a per-workspace network namespace, and with which
// mechanism", plus the deployment lever
// (OCI_MSGMATE_CODER_SANDBOX_NETWORK_NETNS_AVAILABLE) that lets a rollout pin a
// probed answer instead of re-probing at start. Both sources resolve into the
// same NetworkPolicy.NetnsAvailable / .NetnsProvider fields, so the enforcement
// path (`NetworkPolicy.Validate`, the lease path) and the operator surface
// (`ReportLines`, `NetworkPlanStatus`) keep reading one value.
//
// Two properties are deliberate and must not be relaxed:
//
//   - Fail closed. An unreadable or unknown host fact never yields Available,
//     and a probe result is only consulted when the explicit env lever is unset.
//     A pool that cannot prove the mechanism keeps refusing netns leases instead
//     of handing out a namespace it cannot create.
//   - No secrets, no writes. The probe reads /proc and looks for two binaries;
//     it never creates a namespace, never mutates the host, and what it reports
//     (provider name, booleans, missing fact names) is exactly what an operator
//     may see in a report.
//
// The probe answers the *capability*, not the wiring: actually spawning the
// provider around a workspace command belongs to the shared-sandbox provisioning
// path (ci#602), which consumes NetnsCapability.Env(), the same "policy +
// validation first, provisioning later" shape as netisolation.go and
// cgroupguard.go.

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Netns provider names. They are stable strings: a rollout pins one, a report
// prints one, and an operator greps for one.
const (
	// NetnsProviderSlirp4netns is the preferred mechanism: a userspace network
	// stack per workspace, no privileged helper in the pod.
	NetnsProviderSlirp4netns = "slirp4netns"
	// NetnsProviderUserNamespace is the fallback: the kernel's own network
	// namespace via an unprivileged user namespace.
	NetnsProviderUserNamespace = "userns"
	// NetnsProviderUnavailable is the "no mechanism" spelling, so an empty
	// provider is never mistaken for a provider named "".
	NetnsProviderUnavailable = "unavailable"
	// NetnsProviderDeclared is what a policy reports when the deployment lever
	// asserted netns availability but no mechanism was probed or pinned. It is
	// deliberately distinct from NetnsProviderUnavailable: "someone promised"
	// is not "the host proved".
	NetnsProviderDeclared = "declared"
)

// Host paths the probe reads. They are package constants so a test can inject a
// fake state (or a temp dir) instead of asserting on the CI host's kernel.
const (
	// NetnsUsernsClonePath gates unprivileged user namespaces on the Debian/
	// Ubuntu family. A missing file means "the knob does not exist here", which
	// is not evidence of absence (the probe then relies on the other facts).
	NetnsUsernsClonePath = "/proc/sys/kernel/unprivileged_userns_clone"
	// NetnsUIDMapPath exists whenever the kernel gives the process a uid map,
	// i.e. it is running inside a user namespace.
	NetnsUIDMapPath = "/proc/self/uid_map"
	// NetnsTunPath is what a userspace network stack needs from the pod.
	NetnsTunPath = "/dev/net/tun"
)

// NetnsHostState is the read-only, injectable view of the host facts the probe
// reasons about. Booleans are plain values (not tri-states) and DefaultNetnsHostState
// is explicit about what an unreadable fact becomes; a *bool would only move the
// "unknown vs false" decision into every caller.
type NetnsHostState struct {
	// UnprivilegedUsernsClone is the value of NetnsUsernsClonePath, -1 when the
	// knob does not exist (kernel without the patch).
	UnprivilegedUsernsClone int
	// UsernsKnobPresent reports whether NetnsUsernsClonePath was readable.
	UsernsKnobPresent bool
	// InUserNamespace reports whether the process already runs in a user
	// namespace (NetnsUIDMapPath readable and non-identity).
	InUserNamespace bool
	// TunDevice reports whether NetnsTunPath exists.
	TunDevice bool
	// Slirp4netns reports whether the slirp4netns binary is on PATH.
	Slirp4netns bool
	// UnshareTool reports whether an unshare(1) binary is on PATH (the userspace
	// entry point to a user namespace).
	UnshareTool bool
	// MissingFact lists the facts that could not be read at all. They are named
	// in the refusal reason, never guessed at.
	MissingFact []string
}

// DefaultNetnsHostState reads the real host. Every failure is recorded as a
// missing fact rather than panicking: the probe must be safe in a pod that
// exposes none of the paths.
func DefaultNetnsHostState() NetnsHostState {
	state := NetnsHostState{UnprivilegedUsernsClone: -1}
	if raw, err := os.ReadFile(NetnsUsernsClonePath); err == nil {
		if value, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
			state.UnprivilegedUsernsClone = value
			state.UsernsKnobPresent = true
		} else {
			state.MissingFact = append(state.MissingFact, NetnsUsernsClonePath)
		}
	}
	if raw, err := os.ReadFile(NetnsUIDMapPath); err == nil {
		state.InUserNamespace = isNonIdentityUIDMap(string(raw))
	} else {
		state.MissingFact = append(state.MissingFact, NetnsUIDMapPath)
	}
	state.TunDevice = pathExists(NetnsTunPath)
	state.Slirp4netns = binaryOnPath(NetnsProviderSlirp4netns)
	state.UnshareTool = binaryOnPath("unshare")
	return state
}

// NetnsCapability is the resolved verdict of the probe: whether the pool can give
// a workspace its own network namespace, with which mechanism, and (when it
// cannot) which host facts are missing. It carries no credential material.
type NetnsCapability struct {
	// Available is the verdict. False is the fail-closed default.
	Available bool
	// Provider is NetnsProviderSlirp4netns, NetnsProviderUserNamespace, or
	// NetnsProviderUnavailable.
	Provider string
	// Reasons explain the verdict, operator-readable: which fact satisfied the
	// chosen provider, or which facts are missing. Never a secret.
	Reasons []string
	// MissingFacts are the host facts the probe could not read at all, sorted and
	// de-duplicated so two probes of the same pod render the same report.
	MissingFacts []string
}

// ProbeNetnsCapability turns host facts into a capability verdict. It is pure, so
// the decision table is testable without a kernel that can actually do it.
//
// Order of preference follows the design note: slirp4netns first (no privileged
// helper, the mechanism the note names), then a user namespace with unshare(1).
// A pod that is *already* in a user namespace counts as supporting them, which is
// how a nested pool pod (the sandbox itself) reports honestly.
func ProbeNetnsCapability(state NetnsHostState) NetnsCapability {
	capability := NetnsCapability{
		Provider:     NetnsProviderUnavailable,
		MissingFacts: sortedUnique(state.MissingFact),
	}
	missing := func(fact string) { capability.MissingFacts = sortedUnique(append(capability.MissingFacts, fact)) }

	if state.Slirp4netns {
		if state.TunDevice {
			capability.Available = true
			capability.Provider = NetnsProviderSlirp4netns
			capability.Reasons = append(capability.Reasons,
				fmt.Sprintf("%s is on PATH and %s is present", NetnsProviderSlirp4netns, NetnsTunPath))
			return capability
		}
		capability.Reasons = append(capability.Reasons,
			fmt.Sprintf("%s is on PATH but %s is missing, so a workspace interface cannot be created", NetnsProviderSlirp4netns, NetnsTunPath))
		missing(NetnsTunPath)
	}

	usernsAllowed := state.InUserNamespace || !state.UsernsKnobPresent || state.UnprivilegedUsernsClone > 0
	switch {
	case !usernsAllowed:
		capability.Reasons = append(capability.Reasons,
			fmt.Sprintf("unprivileged user namespaces are disabled (%s=0), so %s cannot fall back to %s", NetnsUsernsClonePath, NetnsProviderSlirp4netns, NetnsProviderUserNamespace))
	case !state.UnshareTool:
		capability.Reasons = append(capability.Reasons,
			fmt.Sprintf("user namespaces look usable but no unshare(1) entry point is on PATH, so %s cannot create one", NetnsProviderUserNamespace))
		missing("unshare")
	default:
		capability.Available = true
		capability.Provider = NetnsProviderUserNamespace
		if state.InUserNamespace {
			capability.Reasons = append(capability.Reasons,
				fmt.Sprintf("already inside a user namespace (%s) and unshare(1) is on PATH", NetnsUIDMapPath))
		} else {
			capability.Reasons = append(capability.Reasons,
				fmt.Sprintf("user namespaces are allowed (%s) and unshare(1) is on PATH", NetnsUsernsClonePath))
		}
		return capability
	}

	if !capability.Available {
		capability.Reasons = append(capability.Reasons,
			"no per-workspace netns mechanism is available on this pool: netns mode keeps failing closed with ErrWorkspaceIsolationUnavailable, so a sensitive repo/scope needs a dedicated sandbox")
	}
	return capability
}

// HostNetnsCapability is the probe verdict of the real host: the convenience the
// lease path and a status surface call, so neither has to spell out the host
// state. It is named apart from the NetnsCapability verdict type on purpose.
func HostNetnsCapability() NetnsCapability { return ProbeNetnsCapability(DefaultNetnsHostState()) }

// Env is the workspace environment a provisioning path injects to tell the agent
// which mechanism its namespace uses. It mirrors WorkspaceNetworkPlan.Env: names
// and values only, so it is safe to log.
func (c NetnsCapability) Env() map[string]string {
	env := map[string]string{
		"CODER_NETNS_AVAILABLE": strconv.FormatBool(c.Available),
		"CODER_NETNS_PROVIDER":  c.Provider,
	}
	return env
}

// ReportLines renders the capability for the pool report. It never says more than
// the probe learned: provider, verdict and the missing facts.
func (c NetnsCapability) ReportLines() []string {
	lines := []string{fmt.Sprintf("sandbox_network netns_available=%t netns_provider=%s", c.Available, c.Provider)}
	if len(c.MissingFacts) > 0 {
		lines = append(lines, fmt.Sprintf("sandbox_network netns_missing_facts=%s", strings.Join(c.MissingFacts, ",")))
	}
	for _, reason := range c.Reasons {
		lines = append(lines, fmt.Sprintf("sandbox_network netns_reason %s", reason))
	}
	return lines
}

// isNonIdentityUIDMap reports whether a uid_map maps anything other than 0 to 0,
// which is what "inside a user namespace" looks like in /proc.
func isNonIdentityUIDMap(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return false
	}
	inside, err := strconv.Atoi(fields[0])
	if err != nil {
		return false
	}
	outside, err := strconv.Atoi(fields[1])
	if err != nil {
		return false
	}
	return inside != outside
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func binaryOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
