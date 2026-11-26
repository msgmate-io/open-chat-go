package sandboxsession

// Workspace root materialisation contract (ci#1126).
//
// The red live runtime canary failed like this:
//
//	Canary step "coder_list" ... coder tool api failed (400): failed to list .:
//	sh: cd: line 0: can't cd to /workspace/coder-<uuid>: No such file or directory
//
// Nothing in the provisioning path owned "the workspace root directory exists".
// The ci#614 replay (`reprovision.go`) re-provisions *credentials* only: it
// replays them into the workspace's per-agent HOME and never re-materialises the
// workspace ROOT, and `WorkspaceIdentity.Validate` only checks that HOME is
// absolute (empty until provisioned, absolute afterwards), never that the root
// exists. A workspace provisioned for a repository-less intent - the canary's
// `auto_sandbox` per-chat workspace clones nothing, so no git step ever created
// the directory - therefore came up bound to a root nobody had materialised, and
// the gap only surfaced in the *first tool call* instead of failing the
// provision.
//
// The contract lives here, in core, but the materialisation itself is executed
// by the provider: core cannot see or mkdir a path inside a container, so it
// expresses *what* must exist and hands the provider an argv-level plan - an
// idempotent `mkdir -p` of the root (and of the per-agent HOME when it differs)
// plus a chown to the ci#603 per-workspace OS user - then asserts the provider's
// observation. Core never touches a container path itself.
//
// The private msgmate_coder integration hook opts in by implementing
// WorkspaceRootProvisioner and calling EnsureWorkspaceRoot; every existing
// provider keeps compiling and simply reports the capability as unsupported.
//
// The rule, pinned by workspaceroot_test.go:
//
//  1. a provision either materialises the workspace root or FAILS CLOSED with
//     ErrWorkspaceRootMissing - a root-less workspace is never handed to a tool
//     call;
//  2. materialisation is idempotent (`mkdir -p`, `chown -R`), so replaying it on
//     adopt, on a re-provision or after a pool restart is a no-op for a healthy
//     workspace;
//  3. this package reports paths and booleans only, never file contents.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrWorkspaceRootMissing marks a provision whose workspace root was not
// materialised. It is a *provisioning* failure: the caller must report it as
// such and must not hand the session to a tool call.
var ErrWorkspaceRootMissing = errors.New("sandboxsession: workspace root missing after provisioning")

// WorkspaceRootSpec describes the workspace root a sandbox must have. Root is
// required; WorkspaceUUID, OSUser and Home are optional and only feed the plan
// (chown / HOME materialisation) and the CI report.
type WorkspaceRootSpec struct {
	// WorkspaceUUID identifies the workspace, for the report only.
	WorkspaceUUID string
	// Root is the absolute workspace root, eg "/workspace/coder-<uuid>".
	Root string
	// OSUser is the ci#603 per-workspace OS user the root is chowned to. When
	// empty no chown is planned.
	OSUser string
	// Home is the workspace's per-agent HOME. When empty or equal to Root it
	// needs no separate materialisation.
	Home string
	// Temp marks a per-chat temp sandbox workspace root (`auto_sandbox`,
	// ci#1192). Materialisation for a temp root is UNCONDITIONAL: the root is
	// always created and always made a usable git checkout, so a repo-less temp
	// workspace no longer comes up as an empty directory that the first
	// `cd /workspace/coder-<uuid>` cannot enter. Non-temp roots keep the plain
	// mkdir/chown plan.
	Temp bool
	// Repo is the git remote a Temp workspace root is cloned from (ci#1192).
	// Empty means the root is `git init`ed instead. It is ignored for a
	// non-temp root.
	Repo string
}

// WorkspaceRootPlan is the argv-level materialisation plan for one workspace
// root. It carries paths and commands only, so it is always safe to log.
type WorkspaceRootPlan struct {
	// Spec is the workspace root the plan was built from.
	Spec WorkspaceRootSpec
	// Root is the validated absolute workspace root.
	Root string
	// OSUser is the per-workspace OS user the plan chowns to ("" when unknown).
	OSUser string
	// Home is the per-agent HOME the plan materialises ("" when not needed).
	Home string
	// Commands materialise the root. They are idempotent, so running them on an
	// already provisioned workspace changes nothing.
	Commands [][]string
	// VerifyCommands assert the root exists. A non-zero exit means the root is
	// absent, which fails the provision closed.
	VerifyCommands [][]string
}

// NewWorkspaceRootPlan builds the idempotent materialisation plan for a
// workspace root spec.
func NewWorkspaceRootPlan(spec WorkspaceRootSpec) (WorkspaceRootPlan, error) {
	root, err := cleanAbsolutePath(spec.Root, "workspace root")
	if err != nil {
		return WorkspaceRootPlan{}, err
	}

	plan := WorkspaceRootPlan{
		Spec:           spec,
		Root:           root,
		OSUser:         strings.TrimSpace(spec.OSUser),
		Commands:       [][]string{{"mkdir", "-p", "--", root}},
		VerifyCommands: [][]string{{"test", "-d", root}},
	}
	if plan.OSUser != "" {
		plan.Commands = append(plan.Commands, []string{"chown", "-R", plan.OSUser, root})
	}

	home := strings.TrimSpace(spec.Home)
	if home != "" && home != root {
		home, err = cleanAbsolutePath(home, "workspace home")
		if err != nil {
			return WorkspaceRootPlan{}, err
		}
		plan.Home = home
		plan.Commands = append(plan.Commands, []string{"mkdir", "-p", "--", home})
		if plan.OSUser != "" {
			plan.Commands = append(plan.Commands, []string{"chown", "-R", plan.OSUser, home})
		}
	}
	// ci#1192: a per-chat temp sandbox root is made a usable checkout
	// unconditionally. `git init` is a no-op on an existing repository and the
	// clone step is guarded on the presence of `.git`, so double provisioning of
	// the same root stays a no-op for a healthy workspace.
	if spec.Temp {
		plan.Commands = append(plan.Commands, tempRootGitCommand(root, spec.Repo))
	}
	return plan, nil
}

// tempRootGitCommand is the git step that turns a materialised temp workspace
// root into a usable checkout (ci#1192): clone the repo when the request names
// one and the root does not hold a checkout yet, otherwise initialise an empty
// repository.
func tempRootGitCommand(root, repo string) []string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return []string{"git", "-C", root, "init", "--quiet"}
	}
	// Cloning into a non-empty directory fails with
	// `destination path ... already exists`, which is not a provisioning error
	// for a root that is already correct, so the step is guarded on `.git`.
	return []string{
		"sh", "-c", `test -e "$1/.git" || git clone --quiet -- "$2" "$1"`, "sh", root, repo,
	}
}

// PlanWorkspaceRoot builds the materialisation plan for a provisioned pool
// workspace: the root is the container path the sandbox advertises (for the
// coder sandbox, the shell workdir `/workspace/coder-<uuid>`), and the ci#603
// WorkspaceIdentity supplies the per-workspace OS user and per-agent HOME. The
// ci#614 replay uses it too, so a credential replay can never hand back a
// workspace whose root was not materialised - replaying it on an adopted root is
// an idempotent no-op.
func PlanWorkspaceRoot(identity WorkspaceIdentity, root string) (WorkspaceRootPlan, error) {
	if err := identity.Validate(); err != nil {
		return WorkspaceRootPlan{}, err
	}
	return NewWorkspaceRootPlan(WorkspaceRootSpec{
		WorkspaceUUID: identity.WorkspaceUUID,
		Root:          root,
		OSUser:        identity.OSUser,
		Home:          identity.Home,
	})
}

func cleanAbsolutePath(value, what string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrInvalidRequest, what)
	}
	if !path.IsAbs(value) {
		return "", fmt.Errorf("%w: %s %q must be absolute", ErrInvalidRequest, what, value)
	}
	if cleaned := path.Clean(value); cleaned != value || cleaned == "/" {
		return "", fmt.Errorf("%w: %s %q must be a clean, non-root path (got %q)", ErrInvalidRequest, what, value, cleaned)
	}
	return value, nil
}

// Argv returns a deep copy of the materialisation commands, so a provider cannot
// mutate the plan it was handed.
func (p WorkspaceRootPlan) Argv() [][]string { return copyArgv(p.Commands) }

// VerifyArgv returns a deep copy of the post-provision assertion commands.
func (p WorkspaceRootPlan) VerifyArgv() [][]string { return copyArgv(p.VerifyCommands) }

func copyArgv(in [][]string) [][]string {
	out := make([][]string, 0, len(in))
	for _, command := range in {
		out = append(out, append([]string(nil), command...))
	}
	return out
}

// WorkspaceRootObservation is what the provider reports back after materialising
// (or adopting) the root. It is an observation, not a claim core can verify
// itself: core cannot see inside the container.
type WorkspaceRootObservation struct {
	// Root is the path the provider observed.
	Root string
	// Exists reports whether the root directory exists after provisioning.
	Exists bool
	// Materialised reports whether the provider actually ran the plan (false when
	// an adopted root already existed), for the CI report only.
	Materialised bool
}

// ReportLine renders one `sandbox_workspace_root key=value` line for the CI
// report. Paths and booleans only.
func (o WorkspaceRootObservation) ReportLine() string {
	return fmt.Sprintf("sandbox_workspace_root root=%s exists=%t materialised=%t", o.Root, o.Exists, o.Materialised)
}

// Assert fails the provision closed when the root the plan names is absent. A
// caller must not hand the session to a tool call after an error here.
func (p WorkspaceRootPlan) Assert(observation WorkspaceRootObservation) error {
	if observed := strings.TrimSpace(observation.Root); observed != "" {
		cleaned, err := cleanAbsolutePath(observed, "workspace root")
		if err != nil {
			return err
		}
		if cleaned != p.Root {
			return fmt.Errorf("%w: workspace %s: observed root %q is not the planned root %q",
				ErrInvalidRequest, p.Spec.WorkspaceUUID, cleaned, p.Root)
		}
	}
	if !observation.Exists {
		return fmt.Errorf("%w: workspace %s: root %q does not exist after provisioning (ci#1126); refusing to hand the session to a tool call",
			ErrWorkspaceRootMissing, p.Spec.WorkspaceUUID, p.Root)
	}
	return nil
}

// WorkspaceRootProvisioner is the provisioning contract a sandbox provider
// implements to materialise (or adopt) the workspace root of the sandbox it just
// ensured, inside the sandbox. It is an optional capability, like the lifecycle
// capabilities, so every existing provider keeps compiling and the private
// msgmate_coder hook opts in by implementing it.
//
// A conforming implementation builds its plan with PlanWorkspaceRoot (so the
// ci#603 identity's OS user and per-agent HOME are chowned), runs plan.Argv()
// idempotently - including on adopt, on a credential replay and after a pool
// restart - then plan.VerifyArgv(), and reports the observation. It must never
// report Exists=true unless the verify command succeeded. spec.Root is the root
// core observed from the request (empty when the request names none); the
// provider fills in the root its intent advertises and reports it back.
type WorkspaceRootProvisioner interface {
	ProvisionWorkspaceRoot(ctx context.Context, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error)
}

// SupportsWorkspaceRootProvisioning reports whether the provider satisfies the
// workspace root materialisation contract.
func SupportsWorkspaceRootProvisioning(p Provider) bool {
	_, ok := p.(WorkspaceRootProvisioner)
	return ok
}

// EnsureWorkspaceRoot drives the materialisation contract for a provisioned
// sandbox and FAILS CLOSED: a provider that cannot materialise the root yields a
// provisioning error (ErrUnsupportedOperation), and a root that is still absent
// afterwards yields ErrWorkspaceRootMissing. A caller must treat either as a
// failed *provision* and must not hand the session to a tool call.
func EnsureWorkspaceRoot(ctx context.Context, provider Provider, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error) {
	if provider == nil {
		return WorkspaceRootObservation{}, fmt.Errorf("%w: nil sandbox provider", ErrInvalidRequest)
	}
	name := provider.Name()

	provisioner, ok := provider.(WorkspaceRootProvisioner)
	if !ok {
		return WorkspaceRootObservation{}, fmt.Errorf("sandboxsession: %s: %w: the provider cannot materialise a workspace root (ci#1126)",
			name, ErrUnsupportedOperation)
	}

	observation, err := provisioner.ProvisionWorkspaceRoot(ctx, ref, spec)
	if err != nil {
		return observation, fmt.Errorf("sandboxsession: %s: provision workspace root for sandbox %s: %w", name, ref.SandboxUUID, err)
	}
	if strings.TrimSpace(observation.Root) == "" {
		observation.Root = spec.Root
	}
	if err := assertWorkspaceRoot(name, spec, observation); err != nil {
		return observation, err
	}
	return observation, nil
}

// coderWorkspaceRootPrefix is the container path prefix the coder sandbox
// advertises as its shell workdir (`/workspace/coder-<chat_uuid>`).
const coderWorkspaceRootPrefix = "/workspace/"

// IsCoderWorkspaceRoot reports whether root is a coder sandbox workspace root,
// ie a path under the `/workspace/` volume the coder image mounts.
func IsCoderWorkspaceRoot(root string) bool {
	root = strings.TrimSpace(root)
	return root != coderWorkspaceRootPrefix && strings.HasPrefix(root, coderWorkspaceRootPrefix)
}

// AssertWorkspaceRootCapability fails closed (ci#1166) when a session would be
// handed to a tool call with a coder workspace root that nothing materialised.
// The live canary kept failing after ci#1126 because the materialisation gate in
// Open/AttachSandbox was *fail-open*: a provider that advertises
// `/workspace/coder-<uuid>` but does not implement WorkspaceRootProvisioner was
// silently skipped, so the broken root reached the first tool call as
// `can't cd to /workspace/coder-<uuid>`. A coder root now requires the
// capability; every other root keeps the pre-existing fail-open behaviour so no
// existing provider changes shape.
func AssertWorkspaceRootCapability(provider Provider, root string) error {
	if provider == nil || !IsCoderWorkspaceRoot(root) || SupportsWorkspaceRootProvisioning(provider) {
		return nil
	}
	return fmt.Errorf("%w: provider %s advertises workspace root %q but cannot materialise it (ci#1166); refusing to hand the session to a tool call",
		ErrUnsupportedOperation, provider.Name(), strings.TrimSpace(root))
}

// assertWorkspaceRoot is the post-provision assertion: it fails closed when the
// observed root is not a clean absolute path or does not exist.
func assertWorkspaceRoot(providerName string, spec WorkspaceRootSpec, observation WorkspaceRootObservation) error {
	cleaned, err := cleanAbsolutePath(observation.Root, "workspace root")
	if err != nil {
		return fmt.Errorf("sandboxsession: %s: %w", providerName, err)
	}
	if err := (WorkspaceRootPlan{Spec: spec, Root: cleaned}).Assert(observation); err != nil {
		return fmt.Errorf("sandboxsession: %s: %w", providerName, err)
	}
	return nil
}
