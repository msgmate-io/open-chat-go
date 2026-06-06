package sandboxsession

// This file implements the ci#612 per-workspace network namespace for the
// *pooled* sandbox path, the follow-up of the ci#546 shared-sandbox networking
// design.
//
// Background. The bounded shared-netns policy separates *ports* (a per-workspace
// block plus env plus loopback alias) and, with ci#603, *files/HOME* (a
// per-agent OS identity), but two workspaces in one pooled pod still share the
// pod's network namespace: an agent can see and reach a sibling's listeners, and
// `localhost` remains pod-wide. That is accepted for the ephemeral dev mode
// only; a sensitive repository (secrets available, production credentials,
// `trust=allow`) must get the real boundary.
//
// The rule enforced here, and pinned by netns_test.go:
//
//  1. every pooled workspace gets its own network namespace
//     (`ip netns` + a slirp4netns sidecar) so it keeps outbound egress and its
//     own private 127.0.0.1 while a sibling's bound ports are unreachable;
//  2. the coder exec target *joins* that namespace (WrapShellForNetns) and keeps
//     the ci#603 per-agent UID/HOME, which is why the plan refuses an identity
//     without OS user and HOME;
//  3. DNS/egress and the ci#497 credential flow keep working inside the
//     namespace: the sidecar never disables egress, it only disables the host
//     loopback, and the plan carries a per-namespace resolv.conf path plus the
//     GitHub endpoints `gh` needs;
//  4. a sensitive repository is *refused* a pooled slot and always gets a
//     dedicated pod (DecidePlacement). When no dedicated pod is available the
//     call fails closed instead of silently downgrading to a shared pool.
//
// Nothing here logs or returns credential material: a plan carries names, paths
// and argv only.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Deployment levers. All of them are optional.
const (
	// EnvNetnsDisabled turns per-workspace namespaces off ("1"/"true"), which
	// drops pooled workspaces back to the shared pod netns for the ephemeral dev
	// mode. It never applies to a sensitive repository: that gate is not
	// configurable.
	EnvNetnsDisabled = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_NETNS_DISABLED"
	// EnvNetnsStrict refuses the shared pod netns for *every* pooled workspace,
	// sensitive or not (dedicated pod or nothing).
	EnvNetnsStrict = "OCI_MSGMATE_CODER_SANDBOX_WORKSPACE_NETNS_STRICT"
)

// Placement modes returned by DecidePlacement.
const (
	// PlacementPooledSharedNetns is the ephemeral dev mode: a pooled slot whose
	// workspace shares the pod network namespace (ports/HOME still separated).
	PlacementPooledSharedNetns = "pooled-pod-netns"
	// PlacementPooledWorkspaceNetns is the real boundary: a pooled slot where
	// the workspace runs inside its own network namespace.
	PlacementPooledWorkspaceNetns = "pooled-workspace-netns"
	// PlacementDedicated is a sandbox pod of its own (always used for a
	// sensitive repository).
	PlacementDedicated = "dedicated"
)

// Trust levels a repository scope can carry. TrustAllow is the sensitive one:
// the agent may run arbitrary code against a repo whose secrets are reachable.
const (
	TrustAllow = "allow"
)

// Defaults for a per-workspace network namespace plan.
const (
	// DefaultNetnsBinary creates/manages the namespaces.
	DefaultNetnsBinary = "ip"
	// DefaultSlirpBinary is the userspace network sidecar that gives the
	// namespace outbound egress (and DNS via slirp's built-in resolver).
	DefaultSlirpBinary = "slirp4netns"
	// NetnsNamePrefix prefixes every workspace namespace name.
	NetnsNamePrefix = "wsc-"
	// NetnsNameHashLen is how many hex characters of the workspace UUID hash
	// the namespace name keeps. `ip netns` names are file names, but keeping
	// them short keeps the derived interface names well inside IFNAMSIZ.
	NetnsNameHashLen = 12
	// DefaultNetnsMTU matches slirp4netns' default for a tap-backed namespace.
	DefaultNetnsMTU = 65520
	// SidecarReadyFD is the fd the sidecar reports readiness on.
	SidecarReadyFD = 3
	// SidecarPIDPlaceholder is substituted by the provider with the pid of the
	// anchor process that lives inside the namespace.
	SidecarPIDPlaceholder = "@TARGET_PID"
	// DefaultNetnsLoopbackAliasPrefix is the ci#546 loopback alias space,
	// preserved inside the private namespace so a harness that hardcodes the
	// shared-pod alias keeps working.
	DefaultNetnsLoopbackAliasPrefix = "127.0.1."
	// NetnsResolvConfDir is where per-namespace resolver config lives.
	NetnsResolvConfDir = "/etc/netns"
)

// DefaultNetnsEgressHosts are the endpoints the credential flow of ci#497 and
// `gh` need, probed from *inside* the namespace by the verification script so a
// namespace that lost its egress is never mistaken for a working boundary.
var DefaultNetnsEgressHosts = []string{
	"github.com",
	"api.github.com",
	"uploads.github.com",
}

// Sentinel errors callers can match with errors.Is.
var (
	// ErrSensitiveRequiresDedicated means a sensitive repository was offered a
	// pooled slot. It is never downgraded: the placement is refused.
	ErrSensitiveRequiresDedicated = errors.New("sandboxsession: sensitive repository requires a dedicated sandbox")
	// ErrWorkspaceNetnsUnavailable means no placement satisfies the requested
	// netns policy (eg strict mode without a dedicated pod).
	ErrWorkspaceNetnsUnavailable = errors.New("sandboxsession: per-workspace network namespace unavailable")
	// ErrInvalidNetnsPlan means a namespace plan could not be built or is not
	// usable as an isolation plan.
	ErrInvalidNetnsPlan = errors.New("sandboxsession: invalid workspace network namespace plan")
	// ErrNoPlacement means neither a pooled slot nor a dedicated pod is left.
	ErrNoPlacement = errors.New("sandboxsession: no sandbox placement available")
)

// IsNetnsPlacementFailure reports whether err came from this file's gates.
func IsNetnsPlacementFailure(err error) bool {
	return errors.Is(err, ErrSensitiveRequiresDedicated) ||
		errors.Is(err, ErrWorkspaceNetnsUnavailable) ||
		errors.Is(err, ErrInvalidNetnsPlan) ||
		errors.Is(err, ErrNoPlacement)
}

// RepoScope describes what a workspace is allowed to touch; it decides whether
// the workspace may share a pod network namespace at all.
type RepoScope struct {
	// Repo is the "owner/name" the workspace checks out (a label, no secrets).
	Repo string
	// Trust is the repository trust level, eg TrustAllow.
	Trust string
	// SecretsAvailable means the workspace can read deployment secrets.
	SecretsAvailable bool
	// ProductionCredentials means the workspace can reach production
	// credentials (registries, clusters, cloud APIs).
	ProductionCredentials bool
}

// SensitiveReasons lists why the scope needs a dedicated pod, sorted. An empty
// result means the scope may share a pod network namespace.
func (s RepoScope) SensitiveReasons() []string {
	reasons := make([]string, 0, 3)
	if strings.EqualFold(strings.TrimSpace(s.Trust), TrustAllow) {
		reasons = append(reasons, "trust=allow")
	}
	if s.SecretsAvailable {
		reasons = append(reasons, "secrets available")
	}
	if s.ProductionCredentials {
		reasons = append(reasons, "production credentials")
	}
	sort.Strings(reasons)
	return reasons
}

// Sensitive reports whether the scope must never land in a shared pool.
func (s RepoScope) Sensitive() bool { return len(s.SensitiveReasons()) > 0 }

// PlacementRequest is the input of the placement gate.
type PlacementRequest struct {
	// Scope is what the workspace may touch.
	Scope RepoScope
	// Identity is the ci#603 per-workspace identity the netns plan is built
	// for. It is required for PlacementPooledWorkspaceNetns.
	Identity WorkspaceIdentity
	// PooledSlotAvailable reports whether a pooled sandbox slot exists.
	PooledSlotAvailable bool
	// DedicatedAvailable reports whether a dedicated pod can be provisioned.
	DedicatedAvailable bool
	// WorkspaceNetnsSupported reports whether the runtime/pod can create a
	// per-workspace namespace (CAP_NET_ADMIN plus slirp4netns).
	WorkspaceNetnsSupported bool
	// Strict refuses the shared pod netns even for a non-sensitive repository.
	Strict bool
}

// Placement is the decision of the gate.
type Placement struct {
	// Mode is one of the Placement* constants.
	Mode string
	// Sensitive mirrors Scope.Sensitive() for the caller's reporting.
	Sensitive bool
	// Reasons lists the sensitivity reasons (mode == dedicated) or the policy
	// reason that forced a dedicated pod.
	Reasons []string
	// RequireDedicated is set for a sensitive repository: the caller must not
	// retry the pooled path.
	RequireDedicated bool
	// SharedPodNetns marks the ephemeral dev mode, where sibling listeners stay
	// reachable. It is never set for a sensitive repository.
	SharedPodNetns bool
	// Netns is the per-workspace namespace plan of a pooled workspace.
	Netns *NetnsPlan
}

// PlacementEnvOverrides applies the deployment levers to a request. It keeps
// DecidePlacement free of environment access, so the gate stays testable.
func PlacementEnvOverrides(req *PlacementRequest) {
	if req == nil {
		return
	}
	if envFlag(EnvNetnsDisabled) {
		req.WorkspaceNetnsSupported = false
	}
	if envFlag(EnvNetnsStrict) {
		req.Strict = true
	}
}

func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// DecidePlacement is the hard gate: a sensitive repository is refused a pooled
// slot and always gets a dedicated pod, and a pooled workspace gets its own
// network namespace when the runtime supports one.
//
// It returns an error instead of a placement whenever the policy cannot be
// satisfied, so a caller can never silently downgrade a sensitive workspace
// into a shared pool.
func DecidePlacement(req PlacementRequest) (Placement, error) {
	reasons := req.Scope.SensitiveReasons()
	placement := Placement{Sensitive: len(reasons) > 0, Reasons: reasons}

	if placement.Sensitive {
		// Hard gate: no pooled slot, ever, with or without a per-workspace
		// netns. Ports and HOME separation are not a trust boundary.
		placement.RequireDedicated = true
		if !req.DedicatedAvailable {
			return Placement{}, fmt.Errorf("%w: repo %s (%s): pooled slot refused and no dedicated pod is available",
				ErrSensitiveRequiresDedicated, repoLabel(req.Scope.Repo), strings.Join(reasons, ", "))
		}
		placement.Mode = PlacementDedicated
		return placement, nil
	}

	if req.PooledSlotAvailable {
		if req.WorkspaceNetnsSupported {
			plan, err := NewNetnsPlan(req.Identity)
			if err != nil {
				return Placement{}, err
			}
			placement.Mode = PlacementPooledWorkspaceNetns
			placement.Netns = &plan
			return placement, nil
		}
		if req.Strict {
			if !req.DedicatedAvailable {
				return Placement{}, fmt.Errorf("%w: repo %s: shared pod netns refused in strict mode and no dedicated pod is available",
					ErrWorkspaceNetnsUnavailable, repoLabel(req.Scope.Repo))
			}
			placement.Mode = PlacementDedicated
			placement.Reasons = []string{"per-workspace netns unavailable (strict)"}
			return placement, nil
		}
		// Ephemeral dev mode: accepted for a non-sensitive repository only.
		placement.Mode = PlacementPooledSharedNetns
		placement.SharedPodNetns = true
		return placement, nil
	}

	if !req.DedicatedAvailable {
		return Placement{}, fmt.Errorf("%w: repo %s: no pooled slot and no dedicated pod",
			ErrNoPlacement, repoLabel(req.Scope.Repo))
	}
	placement.Mode = PlacementDedicated
	return placement, nil
}

func repoLabel(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "<unknown>"
	}
	return repo
}

// SlirpSidecar is the userspace network stack that gives one workspace
// namespace outbound egress. It is a plain argv: the provider substitutes
// SidecarPIDPlaceholder with the pid of the namespace's anchor process.
type SlirpSidecar struct {
	// Binary is the sidecar executable.
	Binary string
	// Args are the sidecar arguments, containing SidecarPIDPlaceholder.
	Args []string
	// ReadyFD is the fd the sidecar signals readiness on.
	ReadyFD int
}

// DefaultSlirpSidecar returns the sidecar of a workspace namespace: configure
// the tap inside the namespace, keep a large MTU (the ci#497 credential flow and
// `gh` push a fair amount of data), and disable the *host* loopback so the
// namespace cannot reach a sibling's pod-local listener through it. Egress and
// DNS stay enabled.
func DefaultSlirpSidecar() SlirpSidecar {
	return SlirpSidecar{
		Binary: DefaultSlirpBinary,
		Args: []string{
			"--configure",
			"--mtu=" + strconv.Itoa(DefaultNetnsMTU),
			"--disable-host-loopback",
			"--enable-sandbox",
			"--ready-fd=" + strconv.Itoa(SidecarReadyFD),
			SidecarPIDPlaceholder,
		},
		ReadyFD: SidecarReadyFD,
	}
}

// Validate rejects a sidecar that would not isolate the namespace or would lose
// egress.
func (s SlirpSidecar) Validate() error {
	if strings.TrimSpace(s.Binary) == "" {
		return fmt.Errorf("%w: sidecar binary is empty", ErrInvalidNetnsPlan)
	}
	if s.ReadyFD <= 0 {
		return fmt.Errorf("%w: sidecar ready fd must be positive", ErrInvalidNetnsPlan)
	}
	var hasPID, hasHostLoopbackOff, hasConfigure bool
	for _, arg := range s.Args {
		switch {
		case arg == SidecarPIDPlaceholder:
			hasPID = true
		case arg == "--disable-host-loopback":
			hasHostLoopbackOff = true
		case arg == "--configure":
			hasConfigure = true
		case strings.HasPrefix(strings.ToLower(arg), "--disable-host-loopback="):
			// Any explicit form of the flag is accepted; the value is irrelevant.
			hasHostLoopbackOff = true
		}
	}
	if !hasPID {
		return fmt.Errorf("%w: sidecar argv is missing the %s placeholder", ErrInvalidNetnsPlan, SidecarPIDPlaceholder)
	}
	if !hasHostLoopbackOff {
		return fmt.Errorf("%w: sidecar argv must disable the host loopback so siblings stay unreachable", ErrInvalidNetnsPlan)
	}
	if !hasConfigure {
		return fmt.Errorf("%w: sidecar argv must configure the namespace tap", ErrInvalidNetnsPlan)
	}
	return nil
}

// Argv renders the sidecar argv for one anchor pid.
func (s SlirpSidecar) Argv(pid int) []string {
	out := make([]string, 0, len(s.Args)+1)
	out = append(out, s.Binary)
	for _, arg := range s.Args {
		out = append(out, strings.ReplaceAll(arg, SidecarPIDPlaceholder, strconv.Itoa(pid)))
	}
	return out
}

// NetnsPlan is the per-workspace network namespace of one pooled workspace.
type NetnsPlan struct {
	// WorkspaceUUID identifies the workspace (and its lease).
	WorkspaceUUID string
	// OSUser is the ci#603 per-agent OS user the exec target runs as.
	OSUser string
	// Home is the ci#603 per-agent HOME.
	Home string
	// Namespace is the `ip netns` name of the workspace.
	Namespace string
	// LoopbackAlias is the private loopback of the namespace. Every workspace
	// has the same one; that is the point: it is no longer pod-wide.
	LoopbackAlias string
	// CompatLoopbackAlias is the ci#546 alias inside the private namespace, so
	// harnesses that bind it keep working.
	CompatLoopbackAlias string
	// MTU is the namespace tap MTU.
	MTU int
	// Sidecar is the egress sidecar of the namespace.
	Sidecar SlirpSidecar
	// Command is the exec prefix that joins the namespace. It is prepended to
	// the coder exec target.
	Command []string
	// ResolvConfPath is the per-namespace resolver config path that carries the
	// cluster DNS, so the credential flow and `gh` resolve inside the namespace.
	ResolvConfPath string
	// EgressHosts are the endpoints verified from inside the namespace.
	EgressHosts []string
}

// NetnsName derives the namespace name of a workspace. It is deterministic, so
// a provider can re-attach to the same namespace after a restart, and it is
// short, so derived interface names stay inside IFNAMSIZ.
func NetnsName(workspaceUUID string) (string, error) {
	trimmed := strings.TrimSpace(workspaceUUID)
	if trimmed == "" {
		return "", fmt.Errorf("%w: workspace uuid is empty", ErrInvalidNetnsPlan)
	}
	sum := sha256.Sum256([]byte(trimmed))
	return NetnsNamePrefix + hex.EncodeToString(sum[:])[:NetnsNameHashLen], nil
}

// NewNetnsPlan builds the namespace plan for one per-workspace identity. It
// requires the ci#603 identity fields: joining a namespace must never replace
// the per-agent UID/HOME separation.
func NewNetnsPlan(identity WorkspaceIdentity) (NetnsPlan, error) {
	if err := identity.Validate(); err != nil {
		return NetnsPlan{}, fmt.Errorf("%w: %v", ErrInvalidNetnsPlan, err)
	}
	if strings.TrimSpace(identity.OSUser) == "" {
		return NetnsPlan{}, fmt.Errorf("%w: workspace %s: os user is empty (ci#603 identity is required)",
			ErrInvalidNetnsPlan, identity.WorkspaceUUID)
	}
	namespace, err := NetnsName(identity.WorkspaceUUID)
	if err != nil {
		return NetnsPlan{}, err
	}
	sum := sha256.Sum256([]byte(identity.WorkspaceUUID))
	slot := int(sum[0])%253 + 2
	plan := NetnsPlan{
		WorkspaceUUID:       identity.WorkspaceUUID,
		OSUser:              identity.OSUser,
		Home:                identity.Home,
		Namespace:           namespace,
		LoopbackAlias:       "127.0.0.1",
		CompatLoopbackAlias: DefaultNetnsLoopbackAliasPrefix + strconv.Itoa(slot),
		MTU:                 DefaultNetnsMTU,
		Sidecar:             DefaultSlirpSidecar(),
		Command:             []string{DefaultNetnsBinary, "netns", "exec", namespace, "--"},
		ResolvConfPath:      NetnsResolvConfDir + "/" + namespace + "/resolv.conf",
		EgressHosts:         append([]string(nil), DefaultNetnsEgressHosts...),
	}
	if err := plan.Validate(); err != nil {
		return NetnsPlan{}, err
	}
	return plan, nil
}

// Validate rejects a plan that is not a usable isolation plan.
func (p NetnsPlan) Validate() error {
	if strings.TrimSpace(p.WorkspaceUUID) == "" {
		return fmt.Errorf("%w: workspace uuid is empty", ErrInvalidNetnsPlan)
	}
	if strings.TrimSpace(p.OSUser) == "" || strings.TrimSpace(p.Home) == "" {
		return fmt.Errorf("%w: workspace %s: os user and home are required", ErrInvalidNetnsPlan, p.WorkspaceUUID)
	}
	if !strings.HasPrefix(p.Home, "/") {
		return fmt.Errorf("%w: workspace %s: home %q must be absolute", ErrInvalidNetnsPlan, p.WorkspaceUUID, p.Home)
	}
	expected, err := NetnsName(p.WorkspaceUUID)
	if err != nil {
		return err
	}
	if p.Namespace != expected {
		return fmt.Errorf("%w: workspace %s: namespace %q is not the workspace's own (%q)",
			ErrInvalidNetnsPlan, p.WorkspaceUUID, p.Namespace, expected)
	}
	for _, r := range p.Namespace {
		if r == '/' || r == '.' || r == ' ' || r == 0 {
			return fmt.Errorf("%w: workspace %s: namespace %q must not contain %q",
				ErrInvalidNetnsPlan, p.WorkspaceUUID, p.Namespace, r)
		}
	}
	if p.LoopbackAlias != "127.0.0.1" {
		return fmt.Errorf("%w: workspace %s: loopback alias must be 127.0.0.1 inside the private namespace",
			ErrInvalidNetnsPlan, p.WorkspaceUUID)
	}
	if p.MTU <= 0 {
		return fmt.Errorf("%w: workspace %s: mtu must be positive", ErrInvalidNetnsPlan, p.WorkspaceUUID)
	}
	if err := p.Sidecar.Validate(); err != nil {
		return err
	}
	if len(p.Command) == 0 || p.Command[len(p.Command)-1] != "--" {
		return fmt.Errorf("%w: workspace %s: exec prefix must end with \"--\"", ErrInvalidNetnsPlan, p.WorkspaceUUID)
	}
	if !strings.Contains(strings.Join(p.Command, " "), "netns exec "+p.Namespace) {
		return fmt.Errorf("%w: workspace %s: exec prefix must join %q", ErrInvalidNetnsPlan, p.WorkspaceUUID, p.Namespace)
	}
	if !strings.HasPrefix(p.ResolvConfPath, "/") {
		return fmt.Errorf("%w: workspace %s: resolv.conf path %q must be absolute", ErrInvalidNetnsPlan, p.WorkspaceUUID, p.ResolvConfPath)
	}
	if len(p.EgressHosts) == 0 {
		return fmt.Errorf("%w: workspace %s: no egress host to verify", ErrInvalidNetnsPlan, p.WorkspaceUUID)
	}
	return nil
}

// JoinsNetns reports whether an argv already joins the namespace, which makes
// WrapShellForNetns idempotent.
func JoinsNetns(argv []string, namespace string) bool {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return false
	}
	for i := 0; i+3 < len(argv); i++ {
		if argv[i] != "netns" || argv[i+1] != "exec" {
			continue
		}
		if argv[i+2] == namespace {
			return true
		}
	}
	return false
}

// WrapShellForNetns prepends the namespace exec prefix to a shell spec, so the
// coder exec target joins the workspace's own network namespace while keeping
// its ci#603 per-agent UID/HOME (which this function never rewrites).
func WrapShellForNetns(spec *ShellSpec, plan NetnsPlan) error {
	if spec == nil {
		return fmt.Errorf("%w: nil shell spec", ErrInvalidNetnsPlan)
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if len(spec.Command) == 0 {
		return fmt.Errorf("%w: workspace %s: shell command is empty", ErrInvalidNetnsPlan, plan.WorkspaceUUID)
	}
	if JoinsNetns(spec.Command, plan.Namespace) {
		return nil
	}
	wrapped := make([]string, 0, len(plan.Command)+len(spec.Command))
	wrapped = append(wrapped, plan.Command...)
	wrapped = append(wrapped, spec.Command...)
	spec.Command = wrapped
	if spec.Env == nil {
		spec.Env = make(map[string]string, 3)
	}
	spec.Env[EnvSessionNetns] = plan.Namespace
	spec.Env[EnvSessionNetnsLoopback] = plan.LoopbackAlias
	return nil
}

// Session environment names a wrapped shell carries. They are names and paths,
// never credential material.
const (
	// EnvSessionNetns is the namespace the session runs inside.
	EnvSessionNetns = "SANDBOX_WORKSPACE_NETNS"
	// EnvSessionNetnsLoopback is the session's private loopback.
	EnvSessionNetnsLoopback = "SANDBOX_WORKSPACE_NETNS_LOOPBACK"
)

// String renders a plan for logs: names and paths only.
func (p NetnsPlan) String() string {
	return fmt.Sprintf("workspace=%s user=%s home=%s namespace=%s mtu=%d sidecar=%s resolv=%s egress=%s",
		p.WorkspaceUUID, p.OSUser, p.Home, p.Namespace, p.MTU, p.Sidecar.Binary, p.ResolvConfPath,
		strings.Join(p.EgressHosts, ","))
}
