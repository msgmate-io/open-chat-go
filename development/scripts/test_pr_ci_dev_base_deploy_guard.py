#!/usr/bin/env python3
"""Regression guard for the `dev`-based PR deploy override (ci#833).

`Resolve context and label actions` in
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` derives
`should_deploy` from the PR's live labels. Because `dev` is the
**non-deploying** integration trunk - only the `dev` -> `main` promotion PR
(base `main`) dispatches the CI environment deploy - a `dev`-based PR that
carries a stray or hand-applied `deploy` label must not reach
`approve-and-dispatch-deploy`.

The production hop already had such an override (`base.ref == 'production'`
forces the label set and clears `SHOULD_DEPLOY`). The `dev` hop only had a
*merge-time* guard in `auto-approve-consolidation`, which refuses to merge a
`dev`-based consolidation PR carrying `deploy`/`sandbox-test` but is far too
late to prevent the pre-merge deploy dispatch: the deploy is dispatched from
this job, gated on `should_deploy != ''`, long before the merge.

These tests pin the `dev` arm: it exists, it clears `SHOULD_DEPLOY`, and it runs
*after* the label-derived assignment so it can actually neutralise it.

Deliberately stdlib-only: the `Pin Freshness Gate` CI job runs this file with the
system `python3`, before the build-tools venv (PyYAML) is guaranteed.
"""

from __future__ import annotations

import pathlib
import re
import unittest

WORKFLOW = (
    pathlib.Path(__file__).resolve().parents[2]
    / ".github"
    / "workflows"
    / "run-pr-ci-tests-and-deployments.yaml"
)

STEP_NAME = "Resolve context and label actions"
DEV_ARM = 'if [ "${{ github.event.pull_request.base.ref }}" = "dev" ]; then'
PRODUCTION_ARM = 'if [ "${{ github.event.pull_request.base.ref }}" = "production" ]; then'
# The label-derived `should_deploy` assignment the override has to follow.
LABEL_DERIVED_DEPLOY = """SHOULD_DEPLOY="$(cat "${RUNNER_TEMP}/pr-labels.txt" | grep -x 'deploy' || true)\""""

NEXT_STEP_RE = re.compile(r"\n {6}- ")


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _context_step() -> str:
    text = _workflow_text()
    try:
        start = text.index(STEP_NAME)
    except ValueError as exc:  # pragma: no cover - a rename is the failure
        raise AssertionError(
            f"step {STEP_NAME!r} not found in {WORKFLOW.name}: was it renamed "
            "without updating this guard?"
        ) from exc

    rest = text[start + len(STEP_NAME) :]
    match = NEXT_STEP_RE.search(rest)
    return rest[: match.start()] if match else rest


def _dev_arm(step: str) -> str:
    try:
        start = step.index(DEV_ARM)
    except ValueError as exc:  # pragma: no cover - the regression
        raise AssertionError(
            "the pull-request context step no longer neutralises should_deploy "
            "for `dev`-based PRs (ci#833): a `dev` PR carrying a `deploy` label "
            "would dispatch a staging deploy"
        ) from exc

    rest = step[start:]
    end = rest.index("\n            fi")
    return rest[: end + len("\n            fi")]


class DevBaseDeployGuardTest(unittest.TestCase):
    def setUp(self) -> None:
        self.step = _context_step()

    def test_dev_base_pr_clears_should_deploy(self) -> None:
        """A `dev`-based PR is the non-deploying hop, whatever its labels say."""
        arm = _dev_arm(self.step)
        self.assertIn(
            'SHOULD_DEPLOY=""',
            arm,
            "the `dev` override must clear SHOULD_DEPLOY, not merely log about it",
        )

    def test_dev_override_runs_after_the_label_derived_assignment(self) -> None:
        """Ordering is the point: a later assignment would overwrite the reset."""
        label_read_at = self.step.index(LABEL_DERIVED_DEPLOY)
        dev_arm_at = self.step.index(DEV_ARM)
        self.assertLess(
            label_read_at,
            dev_arm_at,
            "the `dev` override must run after the label-derived should_deploy "
            "assignment, otherwise the reset is a no-op",
        )

    def test_production_override_is_still_present(self) -> None:
        """The `dev` arm is modelled on the production arm; neither may vanish."""
        self.assertIn(
            PRODUCTION_ARM,
            self.step,
            "the production promotion override disappeared from the context step",
        )
        self.assertIn(
            'SHOULD_BUILD="build"',
            self.step,
            "the production override must keep forcing the full label set",
        )

    def test_dev_override_leaves_the_other_labels_alone(self) -> None:
        """Only the deploy flag is neutralised: `dev` PRs still run `test`/`e2e`."""
        arm = _dev_arm(self.step)
        for flag in (
            "SHOULD_TEST=",
            "SHOULD_E2E_TEST=",
            "SHOULD_SANDBOX_TEST=",
            "SHOULD_BUILD=",
            "SHOULD_AI_TEST_REAL=",
        ):
            self.assertNotIn(
                flag,
                arm,
                f"the `dev` override must not rewrite {flag}; labels are trusted "
                "on the dev hop except for the deploy flag",
            )


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
