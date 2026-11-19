package sandboxsession

// Workspace root materialisation driver (ci#1130).
//
// ci#1126 put the *contract* in core: PlanWorkspaceRoot builds an argv-level
// plan (idempotent `mkdir -p` of the root plus a chown to the ci#603
// per-workspace OS user and per-agent HOME) and EnsureWorkspaceRoot asserts the
// provider's observation. What was still missing was the driver: every provider
// had to re-implement "run the plan, then run the verify argv, then report the
// observation", and the private msgmate_coder hook - the only provider that can
// exec inside the sandbox - had no shared implementation to call.
//
// This file adds that driver. It stays argv-level: core never touches a
// container path itself, it hands the provider's runner one argv at a time and
// interprets the exit status. The rule is unchanged and still fails closed: a
// plan whose commands or verify argv do not succeed yields
// ErrWorkspaceRootMissing, so a root-less workspace is never handed to a tool
// call.
//
// Materialise is idempotent by construction (`mkdir -p`, `chown -R`), so the
// same call serves the provision path for *every* workspace intent (including
// the repo-less / empty-intent shape the live runtime canary uses), the ci#614
// replay/adopt path and a re-provision after a pool restart.

import (
	"context"
	"fmt"
)

// WorkspaceRootRunner executes one argv inside the sandbox and returns a
// non-nil error when the command exits non-zero. A provider supplies it from
// its own exec path (SSH, exec API, ...); core never runs it itself.
type WorkspaceRootRunner func(ctx context.Context, argv []string) error

// Materialise runs the plan's idempotent materialisation commands and then its
// verify commands, and reports the observation. It fails closed: a failing
// command or a failing verify yields ErrWorkspaceRootMissing (joined with the
// runner's error) and the observation then reports Exists=false.
func (p WorkspaceRootPlan) Materialise(ctx context.Context, run WorkspaceRootRunner) (WorkspaceRootObservation, error) {
	if run == nil {
		return WorkspaceRootObservation{Root: p.Root}, fmt.Errorf("%w: workspace %s: no workspace root runner", ErrInvalidRequest, p.Spec.WorkspaceUUID)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	observation := WorkspaceRootObservation{Root: p.Root}
	for _, argv := range p.Argv() {
		if err := run(ctx, argv); err != nil {
			return observation, fmt.Errorf("%w: workspace %s: materialise root %q: %w",
				ErrWorkspaceRootMissing, p.Spec.WorkspaceUUID, p.Root, err)
		}
	}
	for _, argv := range p.VerifyArgv() {
		if err := run(ctx, argv); err != nil {
			return observation, fmt.Errorf("%w: workspace %s: root %q does not exist after materialisation: %w",
				ErrWorkspaceRootMissing, p.Spec.WorkspaceUUID, p.Root, err)
		}
	}

	observation.Exists = true
	observation.Materialised = true
	if err := p.Assert(observation); err != nil {
		return observation, err
	}
	return observation, nil
}

// WorkspaceRootProvisionerFunc adapts a function to the
// WorkspaceRootProvisioner capability, so a provider opts into the ci#1126
// contract with one method instead of a new type.
type WorkspaceRootProvisionerFunc func(ctx context.Context, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error)

// ProvisionWorkspaceRoot implements WorkspaceRootProvisioner.
func (f WorkspaceRootProvisionerFunc) ProvisionWorkspaceRoot(ctx context.Context, ref SandboxRef, spec WorkspaceRootSpec) (WorkspaceRootObservation, error) {
	if f == nil {
		return WorkspaceRootObservation{}, fmt.Errorf("%w: nil workspace root provisioner", ErrInvalidRequest)
	}
	return f(ctx, ref, spec)
}
