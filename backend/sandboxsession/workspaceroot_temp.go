package sandboxsession

// Temp workspace root materialisation (ci#1192).
//
// ci#1126/ci#1130 taught the provision path to materialise a workspace ROOT, but
// the plan it built was repository-agnostic: an idempotent `mkdir -p` of the root
// plus a chown. A per-chat temp sandbox (`auto_sandbox`, no `git_repo`) therefore
// came up as an empty directory that was not a git checkout, and a
// repository-backed temp sandbox relied on a clone step that no core contract
// owned. Both gaps surfaced only in the first tool call, as
//
//	sh: cd: line 0: can't cd to /workspace/coder-<uuid>: No such file or directory
//
// or as `fatal: destination path ... already exists` on a second provisioning.
//
// The plan side of the fix lives where every other root is planned:
// WorkspaceRootSpec.Temp/Repo (see NewWorkspaceRootPlan) make materialisation
// UNCONDITIONAL for the temp-sandbox shape - the root is always created, and it
// is always made a usable checkout (`git init` when no repo is named, a guarded
// clone when one is). Both steps are idempotent, so double provisioning of the
// same root stays a no-op.
//
// This file adds the fail-closed boundary around that plan, because the failure
// mode above is not "the root is missing" but "nothing owned the root": a
// provisioning failure is reported as the typed ErrWorkspaceRootProvisioning
// (joined with ErrWorkspaceRootMissing and the underlying cause), and
// RequireMaterialisedWorkspaceRoot lets the turn-start path refuse to start a
// turn against a root that was not materialised.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrWorkspaceRootProvisioning marks a workspace root that could not be
// materialised. It is a *provisioning* failure: a caller must surface it and must
// not start a turn against the workspace. The underlying cause (including
// ErrWorkspaceRootMissing) stays matchable with errors.Is.
var ErrWorkspaceRootProvisioning = errors.New("sandboxsession: workspace root could not be provisioned")

// TempWorkspaceRootSpec builds the materialisation spec for a per-chat temp
// sandbox root: the root plus the git remote (empty for a repo-less workspace).
func TempWorkspaceRootSpec(workspaceUUID, root, repo string) WorkspaceRootSpec {
	return WorkspaceRootSpec{
		WorkspaceUUID: workspaceUUID,
		Root:          root,
		Temp:          true,
		Repo:          strings.TrimSpace(repo),
	}
}

// EnsureTempWorkspaceRoot materialises the root of a per-chat temp sandbox and
// FAILS CLOSED: any plan or runner failure is reported as
// ErrWorkspaceRootProvisioning, and the observation then reports Exists=false.
// run is the provider's exec path; core never touches a container path itself.
func EnsureTempWorkspaceRoot(ctx context.Context, spec WorkspaceRootSpec, run WorkspaceRootRunner) (WorkspaceRootObservation, error) {
	spec.Temp = true
	plan, err := NewWorkspaceRootPlan(spec)
	if err != nil {
		return WorkspaceRootObservation{Root: strings.TrimSpace(spec.Root)}, fmt.Errorf("%w: %w", ErrWorkspaceRootProvisioning, err)
	}
	observation, err := plan.Materialise(ctx, run)
	if err != nil {
		return observation, fmt.Errorf("%w: %w", ErrWorkspaceRootProvisioning, err)
	}
	return observation, nil
}

// RequireMaterialisedWorkspaceRoot refuses to start a turn against a workspace
// whose root was not materialised. The turn-start path calls it with the
// observation of the provisioning step, so the failure is a provisioning error
// at the boundary instead of `sh: cd: ... No such file or directory` inside the
// first tool call.
func RequireMaterialisedWorkspaceRoot(root string, observation WorkspaceRootObservation) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("%w: workspace root is empty", ErrInvalidRequest)
	}
	if !observation.Exists {
		return fmt.Errorf("%w: workspace root %q does not exist (ci#1192); refusing to start a turn",
			ErrWorkspaceRootProvisioning, root)
	}
	return nil
}

// wrapTempRootProvisionError adds the typed provisioning marker to a temp-root
// provisioning failure, so the turn-start boundary can report "the root could not
// be provisioned" without unwrapping the provider's cause. A non-temp root keeps
// the pre-existing error shape (ci#1126/ci#1166) unchanged.
func wrapTempRootProvisionError(temp bool, err error) error {
	if err == nil || !temp {
		return err
	}
	if errors.Is(err, ErrWorkspaceRootProvisioning) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrWorkspaceRootProvisioning, err)
}
