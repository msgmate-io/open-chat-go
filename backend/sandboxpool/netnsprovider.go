package sandboxpool

// ci#792 item 4 (parent ci#546) - actually provision per-workspace netns.
//
// netns mode used to be policy and validation only (see netisolation.go
// ErrWorkspaceIsolationUnavailable and the placement decision in
// backend/sandboxsession/netns.go): a pool could *ask* for a per-workspace network
// namespace but nothing checked whether the pod could create one, and nothing
// rendered the provisioning step. This file adds the two missing halves:
//
//   - NetnsHostSupport / NetnsHostProbe detect the host capability fail-closed: a
//     usable user-namespace ceiling, CAP_NET_ADMIN in the pool process and a slirp
//     sidecar binary for egress passthrough. A missing requirement is a blocker, so
//     the pool refuses netns mode instead of silently downgrading to shared;
//   - WorkspaceNetns / ResolveWorkspaceNetns render the per-workspace provisioning:
//     the namespace name, a private loopback (the aliases of shared mode are not
//     used), the egress passthrough argv and the read-only setup script.
//
// It carries no credential material and never mutates the host at render time: names,
// paths, argv and commands only. The caller applies the script; this file proves the
// host supports it first.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Netns provisioning contract.
const (
	// DefaultNetnsSidecarBinary is the egress passthrough an unprivileged pool
	// uses to give a workspace namespace a network without CAP_NET_ADMIN sockets.
	DefaultNetnsSidecarBinary = "slirp4netns"
	// EnvNetnsSidecarBinary overrides the sidecar binary name.
	EnvNetnsSidecarBinary = "OCI_MSGMATE_CODER_SANDBOX_NETNS_SIDECAR_BINARY"
	// EnvNetnsHostRoot overrides the host root the capability probe reads (a
	// deployment can mount the pod's /proc read-only elsewhere).
	EnvNetnsHostRoot = "OCI_MSGMATE_CODER_SANDBOX_NETNS_HOST_ROOT"
	// DefaultNetnsHostRoot is the pod's own filesystem view.
	DefaultNetnsHostRoot = "/"
	// NetnsPathPrefix is where the created namespaces live.
	NetnsPathPrefix = "/var/run/netns"
	// DefaultNetnsLoopback is the private loopback of a workspace namespace: in
	// netns mode every workspace has its own 127.0.0.1, so the shared-mode alias
	// dance is unnecessary.
	DefaultNetnsLoopback = "127.0.0.1"
	// NetnsEgressSlirp is the egress mode of a namespace without CAP_NET_ADMIN in
	// the workspace itself: egress passthrough by the sidecar.
	NetnsEgressSlirp = "slirp4netns"
	// EnvWorkspaceNetnsName is the session env name carrying the namespace name.
	EnvWorkspaceNetnsName = "CODER_WORKSPACE_NETNS"
	// EnvWorkspaceNetnsLoopback is the session env name carrying the private
	// loopback of the namespace.
	EnvWorkspaceNetnsLoopback = "CODER_WORKSPACE_NETNS_LOOPBACK"
	// EnvWorkspaceNetnsEgress is the session env name carrying the egress mode.
	EnvWorkspaceNetnsEgress = "CODER_WORKSPACE_NETNS_EGRESS"
)

// capNetAdminBit is CAP_NET_ADMIN in the CapEff bitmask of /proc/<pid>/status.
const capNetAdminBit = 12

// NetnsHostSupport is the resolved, fail-closed host capability check of netns
// mode. Supported is true only when every requirement holds: a caller that gets a
// false must refuse netns mode (ErrWorkspaceIsolationUnavailable), never downgrade
// a sensitive scope to shared.
type NetnsHostSupport struct {
	// Root is the host root the probe inspected.
	Root string `json:"root"`
	// UserNamespaces reports whether unprivileged user namespaces are allowed.
	UserNamespaces bool `json:"user_namespaces"`
	// MaxUserNamespaces is the resolved proc/sys/user/max_user_namespaces value.
	MaxUserNamespaces int `json:"max_user_namespaces"`
	// CapNetAdmin reports whether the pool process holds CAP_NET_ADMIN.
	CapNetAdmin bool `json:"cap_net_admin"`
	// CapEff is the effective capability bitmask, for the operator to compare.
	CapEff string `json:"cap_eff,omitempty"`
	// SidecarBinary is the resolved sidecar binary name.
	SidecarBinary string `json:"sidecar_binary,omitempty"`
	// SidecarPath is the resolved sidecar binary path (empty when not found).
	SidecarPath string `json:"sidecar_path,omitempty"`
	// Blockers are the unmet requirements, each naming the lever that fixes it.
	Blockers []string `json:"blockers,omitempty"`
}

// Supported reports whether the host can carry a per-workspace namespace.
func (s NetnsHostSupport) Supported() bool {
	return s.UserNamespaces && s.MaxUserNamespaces > 0 && s.CapNetAdmin && strings.TrimSpace(s.SidecarPath) != "" && len(s.Blockers) == 0
}

// ReportLines renders the host check for the admin surface and the CI log.
func (s NetnsHostSupport) ReportLines() []string {
	lines := []string{fmt.Sprintf(
		"sandbox_netns_host supported=%t root=%s user_namespaces=%t max_user_namespaces=%d cap_net_admin=%t cap_eff=%s sidecar=%s",
		s.Supported(), s.Root, s.UserNamespaces, s.MaxUserNamespaces, s.CapNetAdmin, s.CapEff, s.SidecarBinary)}
	for _, blocker := range s.Blockers {
		lines = append(lines, "sandbox_netns_host blocker="+blocker)
	}
	return lines
}

// NetnsHostProbe reads the host facts. Root and both functions are injectable so the
// detection is testable without a privileged host.
type NetnsHostProbe struct {
	// Root is the host root (a file root, not a chroot).
	Root string
	// ReadFile reads a file below Root; defaults to os.ReadFile.
	ReadFile func(path string) ([]byte, error)
	// LookPath resolves the sidecar binary; defaults to exec.LookPath.
	LookPath func(binary string) (string, error)
}

// DefaultNetnsHostProbe probes the configured (or default) host root.
func DefaultNetnsHostProbe(root string) NetnsHostProbe {
	if strings.TrimSpace(root) == "" {
		root = DefaultNetnsHostRoot
	}
	return NetnsHostProbe{Root: root, ReadFile: os.ReadFile, LookPath: exec.LookPath}
}

// ResolveNetnsHostSupport probes the deployment-configured host root and sidecar.
func ResolveNetnsHostSupport() NetnsHostSupport {
	return DetectNetnsHostSupport(DefaultNetnsHostProbe(os.Getenv(EnvNetnsHostRoot)))
}

// DetectNetnsHostSupport inspects the host and reports every unmet requirement as a
// blocker. It never fails silently: an unreadable capability file is a blocker, not
// an assumed capability.
func DetectNetnsHostSupport(probe NetnsHostProbe) NetnsHostSupport {
	root := strings.TrimSpace(probe.Root)
	if root == "" {
		root = DefaultNetnsHostRoot
	}
	read := probe.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	lookPath := probe.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	support := NetnsHostSupport{
		Root:          root,
		SidecarBinary: netnsSidecarBinary(),
	}

	raw, err := read(hostPath(root, "proc/self/status"))
	if err != nil {
		support.Blockers = append(support.Blockers, fmt.Sprintf(
			"cannot read /proc/self/status (%v), so CAP_NET_ADMIN cannot be verified and netns mode is refused", err))
	} else {
		capEff, ok := capabilityHex(string(raw))
		if !ok {
			support.Blockers = append(support.Blockers, fmt.Sprintf(
				"no CapEff line in /proc/self/status, so CAP_NET_ADMIN cannot be verified; grant the capability or use %s mode", NetworkModeShared))
		} else {
			support.CapEff = capEff
			value, parseErr := strconv.ParseUint(capEff, 16, 64)
			if parseErr != nil {
				support.Blockers = append(support.Blockers, fmt.Sprintf(
					"CapEff %q is not a capability bitmask (%v), so CAP_NET_ADMIN cannot be verified", capEff, parseErr))
			} else if value&(uint64(1)<<capNetAdminBit) == 0 {
				support.Blockers = append(support.Blockers, fmt.Sprintf(
					"CAP_NET_ADMIN is not held (CapEff=%s), so a workspace namespace cannot be wired up; unsupported, never downgraded", capEff))
			} else {
				support.CapNetAdmin = true
			}
		}
	}

	raw, err = read(hostPath(root, "proc/sys/user/max_user_namespaces"))
	if err != nil {
		support.Blockers = append(support.Blockers, fmt.Sprintf(
			"cannot read /proc/sys/user/max_user_namespaces (%v), so the namespace ceiling is unknown and netns mode is refused", err))
	} else {
		max, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		switch {
		case parseErr != nil:
			support.Blockers = append(support.Blockers, fmt.Sprintf(
				"max_user_namespaces %q is not a number (%v); netns mode is refused", strings.TrimSpace(string(raw)), parseErr))
		case max <= 0:
			support.Blockers = append(support.Blockers, fmt.Sprintf(
				"max_user_namespaces is %d, so no unprivileged user namespace can be created (set /proc/sys/user/max_user_namespaces or run with a privileged helper)", max))
		default:
			support.UserNamespaces = true
			support.MaxUserNamespaces = max
		}
	}

	if path, lookErr := lookPath(support.SidecarBinary); lookErr != nil {
		support.Blockers = append(support.Blockers, fmt.Sprintf(
			"the egress sidecar %q was not found (%v), so a workspace namespace would have no egress passthrough; install it or set %s",
			support.SidecarBinary, lookErr, EnvNetnsSidecarBinary))
	} else {
		support.SidecarPath = path
	}

	return support
}

// WorkspaceNetns is the resolved per-workspace namespace provisioning: the name, the
// private loopback, the egress passthrough and the read-only setup script. Values are
// names, paths, ports and commands only.
type WorkspaceNetns struct {
	// WorkspaceUUID is the workspace the namespace belongs to.
	WorkspaceUUID string `json:"workspace_uuid"`
	// Name is the resolved namespace name (derived, never caller-supplied).
	Name string `json:"name"`
	// Path is the namespace path below NetnsPathPrefix.
	Path string `json:"path"`
	// Loopback is the private loopback of the namespace.
	Loopback string `json:"loopback"`
	// Egress is the egress mode (NetnsEgressSlirp).
	Egress string `json:"egress"`
	// EgressArgv is the resolved sidecar argv of the egress passthrough.
	EgressArgv []string `json:"egress_argv,omitempty"`
	// PortStart is the first port of the workspace block (informational: inside the
	// namespace the ports are private, so the block only documents the mapping).
	PortStart int `json:"port_start"`
	// PortEnd is the last (inclusive) port of the workspace block.
	PortEnd int `json:"port_end"`
	// Env is the session env contract of the namespace.
	Env map[string]string `json:"env"`
	// Commands are the ordered setup commands the provider runs.
	Commands []string `json:"commands,omitempty"`
	// HostBlockers are the unmet host requirements (empty when supported).
	HostBlockers []string `json:"host_blockers,omitempty"`
	// Notes are the caveats; never a secret.
	Notes []string `json:"notes,omitempty"`
	// GeneratedAt is the render time (RFC3339, UTC).
	GeneratedAt string `json:"generated_at"`
}

// Valid reports whether the namespace plan is usable.
func (n WorkspaceNetns) Valid() bool {
	return n.WorkspaceUUID != "" && strings.TrimSpace(n.Name) != "" && strings.HasPrefix(n.Path, "/") && len(n.Commands) > 0
}

// ApplyScript renders the ordered setup script (one command per line). Every value
// is a validated name, path or number, so the script carries nothing injectable.
func (n WorkspaceNetns) ApplyScript() string { return strings.Join(n.Commands, "\n") }

// ReportLines renders the plan as the CI/admin report block of ci#792 item 4.
func (n WorkspaceNetns) ReportLines() []string {
	lines := []string{fmt.Sprintf(
		"sandbox_netns workspace=%s name=%s path=%s loopback=%s egress=%s ports=%d-%d",
		n.WorkspaceUUID, n.Name, n.Path, n.Loopback, n.Egress, n.PortStart, n.PortEnd)}
	if len(n.EgressArgv) > 0 {
		lines = append(lines, fmt.Sprintf("sandbox_netns workspace=%s egress_argv=%s", n.WorkspaceUUID, strings.Join(n.EgressArgv, " ")))
	}
	for _, note := range n.Notes {
		lines = append(lines, fmt.Sprintf("sandbox_netns workspace=%s note=%s", n.WorkspaceUUID, note))
	}
	return lines
}

// ResolveWorkspaceNetns renders the provisioning of one workspace namespace. It fails
// closed: a host that cannot carry a namespace yields
// ErrWorkspaceIsolationUnavailable with every blocker, so a sensitive scope is
// refused rather than silently downgraded to shared mode.
func ResolveWorkspaceNetns(plan WorkspaceNetworkPlan, host NetnsHostSupport, now time.Time) (WorkspaceNetns, error) {
	if !host.Supported() {
		blockers := host.Blockers
		if len(blockers) == 0 {
			blockers = []string{"the host cannot create a per-workspace network namespace"}
		}
		return WorkspaceNetns{}, fmt.Errorf("%w: %s", ErrWorkspaceIsolationUnavailable, strings.Join(blockers, "; "))
	}
	uuid := strings.TrimSpace(plan.WorkspaceUUID)
	if uuid == "" || strings.ContainsAny(uuid, " \t\r\n/") {
		return WorkspaceNetns{}, fmt.Errorf("%w: %q", ErrInvalidWorkspaceUUID, plan.WorkspaceUUID)
	}

	name := netnsName(uuid)
	path := NetnsPathPrefix + "/" + name
	sidecar := strings.TrimSpace(host.SidecarPath)

	netns := WorkspaceNetns{
		WorkspaceUUID: uuid,
		Name:          name,
		Path:          path,
		Loopback:      DefaultNetnsLoopback,
		Egress:        NetnsEgressSlirp,
		EgressArgv:    []string{sidecar, "--configure", "--mtu=65520", "--enable-ipv6=false", path, "tap0"},
		PortStart:     plan.PortStart,
		PortEnd:       plan.PortEnd,
		Env: map[string]string{
			EnvWorkspaceNetnsName:     name,
			EnvWorkspaceNetnsLoopback: DefaultNetnsLoopback,
			EnvWorkspaceNetnsEgress:   NetnsEgressSlirp,
		},
		Commands: []string{
			fmt.Sprintf("ip netns add %s", name),
			fmt.Sprintf("ip netns exec %s ip link set lo up", name),
			fmt.Sprintf("%s --configure --mtu=65520 --enable-ipv6=false %s tap0", sidecar, path),
			fmt.Sprintf("ip netns exec %s ip route add default dev tap0", name),
		},
		Notes: []string{
			"the loopback is private to the namespace, so CODER_LOCALHOST is 127.0.0.1 for every workspace and no shared-mode alias is needed",
			fmt.Sprintf("egress is the %s passthrough: the workspace itself needs no CAP_NET_ADMIN", NetnsEgressSlirp),
			fmt.Sprintf("the pool keeps CAP_NET_ADMIN (CapEff=%s); if the capability or the sidecar disappears the pool refuses netns mode instead of downgrading", host.CapEff),
			"the workspace block remains the documented mapping, but inside the namespace the ports are private: a consumer dials the pool transport, not the loopback",
		},
		GeneratedAt: now.UTC().Format(time.RFC3339),
	}
	return netns, nil
}

// netnsName derives the namespace name from the workspace identity: deterministic,
// lowercase, 12 hex characters, so two workspaces can never share a namespace and a
// caller can never inject one.
func netnsName(workspaceUUID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(workspaceUUID)))
	return "cwn-" + hex.EncodeToString(sum[:])[:12]
}

// netnsSidecarBinary resolves the configured sidecar binary name, never an empty one.
func netnsSidecarBinary() string {
	if raw := strings.TrimSpace(os.Getenv(EnvNetnsSidecarBinary)); raw != "" {
		return raw
	}
	return DefaultNetnsSidecarBinary
}

// capabilityHex extracts the CapEff value of a /proc/self/status document.
func capabilityHex(status string) (string, bool) {
	for _, line := range strings.Split(status, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "CapEff:") {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "CapEff:")))
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

// hostPath joins a file below a host root without escaping it.
func hostPath(root, name string) string {
	if root == "/" {
		return "/" + name
	}
	return filepath.Join(root, name)
}
