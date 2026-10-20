#!/usr/bin/env python3
"""Regression guard for the mocked-AI merge-gate case.

`PR Test Merge Requirement` in
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` decides per suite
whether a non-`success` result may still satisfy the gate. The `e2e` and
`sandbox` cases fail closed: when the suite was requested by a label but its
job reports `skipped`, the gate errors out instead of passing.

The mocked-AI case did not. It accepted `skipped) ;;` unconditionally, so a run
in which `run-pr-ci-ai-mock-tests` was skipped while the `ai-test-mock` label
was live - for example because `build-backend-image-once` failed or the
orchestration job was itself skipped - satisfied the merge requirement and the
label's guarantee was silently dropped.

The same block also carried a verbatim copy of the go-tests guard
(`should_test` / `Tests were not executed despite the test label.`) that lives
just above the ai-mock case. That copy was a no-op there: it re-read the *go*
job's output, so it could never fire for the ai-mock job.

These tests pin the fail-closed label guard and the absence of the dead copy so
neither can silently come back.

Deliberately stdlib-only: the `Pin Freshness Gate` CI job runs this file with
the system `python3`, before the build-tools venv (PyYAML) is guaranteed.
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

STEP_NAME = "Enforce test label and passing tests"
AI_MOCK_RESULT = "${{ needs.run-pr-ci-ai-mock-tests.result }}"

# Step entries inside a job start at six spaces of indentation.
NEXT_STEP_RE = re.compile(r"\n {6}- ")
# A `case` branch header inside the step's shell script. The branch body may
# start on the same line (`skipped) ;;`) or on the next one.
CASE_BRANCH_RE = re.compile(r"\n\s+([a-z*]+)\)")


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _gate_step() -> str:
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


def _ai_mock_case(step: str) -> str:
    try:
        start = step.index(f'case "{AI_MOCK_RESULT}"')
    except ValueError as exc:  # pragma: no cover - a rename is the failure
        raise AssertionError(
            f'no `case "{AI_MOCK_RESULT}"` in the {STEP_NAME!r} step'
        ) from exc

    rest = step[start:]
    end = rest.index("esac")
    return rest[: end + len("esac")]


def _skipped_branch(case: str) -> str:
    branches = list(CASE_BRANCH_RE.finditer(case))
    for index, match in enumerate(branches):
        if match.group(1) != "skipped":
            continue
        stop = branches[index + 1].start() if index + 1 < len(branches) else len(case)
        return case[match.end() : stop]
    raise AssertionError("the ai-mock case has no `skipped)` branch")


class AiMockGateTest(unittest.TestCase):
    def setUp(self) -> None:
        self.step = _gate_step()
        self.case = _ai_mock_case(self.step)

    def test_skipped_fails_closed_on_the_ai_test_mock_label(self) -> None:
        """`skipped` must be fatal while the requesting label is present."""
        branch = _skipped_branch(self.case)
        self.assertRegex(
            branch,
            r"grep -qx 'ai-test-mock'",
            "the skipped ai-mock branch must consult the live `ai-test-mock` label",
        )
        self.assertRegex(branch, r"exit 1", "the skipped branch must fail the gate")

    def test_skipped_still_passes_without_the_label(self) -> None:
        """The guard is label-driven; an unrequested suite stays a pass."""
        branch = _skipped_branch(self.case)
        self.assertNotIn("::error", branch.split("exit 1")[-1])

    def test_case_covers_every_gha_job_result(self) -> None:
        """failure/cancelled/unknown must keep their explicit handling."""
        for result in ("success", "failure", "cancelled", "*"):
            self.assertIn(
                f"{result})",
                self.case,
                f"the ai-mock case lost its `{result})` branch",
            )
        self.assertIn("accept_superseded", self.case)

    def test_no_dead_go_tests_guard_in_the_ai_mock_case(self) -> None:
        """The copied go-tests guard could never fire here; it must not return."""
        for marker in (
            "run-pr-ci-go-tests.outputs.should_test",
            "Tests were not executed despite the test label.",
        ):
            self.assertNotIn(
                marker,
                self.case,
                f"the ai-mock case re-reads the go-tests job ({marker})",
            )

    def test_go_tests_guard_is_still_present_once(self) -> None:
        """Removing the copy must not have removed the real guard."""
        marker = "Tests were not executed despite the test label."
        self.assertEqual(
            self.step.count(marker),
            1,
            "the go-tests guard must exist exactly once in the gate step",
        )


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
