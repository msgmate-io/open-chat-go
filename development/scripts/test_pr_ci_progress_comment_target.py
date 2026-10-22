#!/usr/bin/env python3
"""Regression guard for ci#745 (residual scope).

The PR-CI progress-comment jobs in
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` used to build their
GitHub API URLs straight from
`needs.run-pr-ci-tests-and-deployments.outputs.source_repository` /
`...outputs.pr_number`. Whenever the upstream orchestration job was skipped those
outputs are empty, so the step posted to
`https://api.github.com/repos//issues//comments` and the job died with
`curl: (22) The requested URL returned error: 404` on every PR run.

`Render test-complete progress comment` (the only job that was reachable) is
fixed by threading the target through explicit step env with an event-context
fallback plus a fail-safe skip. This file pins that, pins the residual part of
ci#745 - the two sibling jobs that were only safe *by accident* now delegate to
`development/scripts/upsert_pr_ci_progress_comment.sh` - and pins the global
invariant that no comment URL is ever built from a raw job output again.

Deliberately stdlib-only: the `Pin Freshness Gate` CI job runs this file with the
system `python3`, before the build-tools venv (PyYAML) is guaranteed. The helper's
runtime behaviour is pinned separately by
`development/scripts/tests/test_upsert_pr_ci_progress_comment.sh`.
"""

from __future__ import annotations

import os
import pathlib
import re
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "run-pr-ci-tests-and-deployments.yaml"
HELPER = REPO_ROOT / "development" / "scripts" / "upsert_pr_ci_progress_comment.sh"
HELPER_TEST = (
    REPO_ROOT / "development" / "scripts" / "tests" / "test_upsert_pr_ci_progress_comment.sh"
)

COMMENT_MARKER = "<!-- open-chat-pr-ci-progress -->"
HELPER_REF = "development/scripts/upsert_pr_ci_progress_comment.sh"

# Step entries inside a job start at six spaces of indentation.
STEP_SPLIT_RE = re.compile(r"\n {6}- ")
# A new job starts with a two-space key at column zero.
JOB_BOUNDARY_RE = re.compile(r"\n {2}[a-zA-Z0-9_-]+:\n")

# Steps that delegate the whole upsert to the shared helper (ci#745 residual).
DELEGATING_STEPS = (
    "Update progress comment after approved dispatch",
    "Render final progress comment with AI real result",
)
# Step fixed by the original ci#755/ci#759 pass: explicit env + fail-safe inline.
ENV_TARGET_STEPS = ("Render test-complete progress comment",)

# ci#833 follow-up: the job/step that now renders the *terminal* `(4) test`
# bullet, and the suites whose results it must render (and therefore wait for).
# Before the fix this job only `needs` the orchestration job, so on a PR without
# the `deploy` / `ai-test-real` labels no renderer ever replaced the dispatch-era
# `go=running` literal - run 39866169619 on PR #1033 finished green while its
# progress comment still read `go=running`.
AFTER_TESTS_JOB = "update-progress-comment-after-tests"
AFTER_TESTS_STEP = "Render test-complete progress comment"
SUITE_JOBS = ("run-pr-ci-go-tests", "run-pr-ci-ai-mock-tests", "run-pr-ci-e2e-tests")

# The ci#745 root cause: a raw `needs.*.outputs.*` expression interpolated inside
# an api.github.com *comment* URL, which collapses to `repos//issues//comments`.
RAW_OUTPUT_COMMENT_URL_RE = re.compile(
    r"api\.github\.com/repos/\$\{\{[^}]*outputs\.[^}]*\}\}/issues"
)


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _job_block(job: str) -> str:
    """Return the YAML block of one top-level job, without the next job."""
    text = _workflow_text()
    start = text.index(f"\n  {job}:\n") + 1
    body = text[start:]
    boundary = JOB_BOUNDARY_RE.search(body[1:])
    end = start + 1 + boundary.start() if boundary else len(text)
    return text[start:end]


def _progress_comment_steps() -> dict[str, str]:
    """Map every progress-comment step name to its YAML block."""
    steps: dict[str, str] = {}
    for chunk in STEP_SPLIT_RE.split(_workflow_text())[1:]:
        name, _, _ = chunk.partition("\n")
        name = name.strip()
        if not name.startswith("name:") or "progress comment" not in name.lower():
            continue
        step_name = name[len("name:") :].strip()
        boundary = JOB_BOUNDARY_RE.search(chunk)
        steps[step_name] = chunk[: boundary.start()] if boundary else chunk
    return steps


class ProgressCommentTargetTest(unittest.TestCase):
    def setUp(self) -> None:
        self.steps = _progress_comment_steps()

    def test_expected_progress_comment_steps_exist(self) -> None:
        """A rename must be a failure, not a silently skipped guard."""
        for step_name in DELEGATING_STEPS + ENV_TARGET_STEPS:
            with self.subTest(step=step_name):
                self.assertIn(step_name, self.steps, sorted(self.steps))

    def test_no_raw_outputs_are_interpolated_into_comment_urls(self) -> None:
        """The workflow must never build a comment URL from raw job outputs."""
        text = _workflow_text()
        self.assertIsNone(
            RAW_OUTPUT_COMMENT_URL_RE.search(text),
            RAW_OUTPUT_COMMENT_URL_RE.findall(text),
        )

    def test_delegating_steps_pass_an_explicit_comment_target(self) -> None:
        """Repository and PR number reach the helper as checked shell variables."""
        for step_name in DELEGATING_STEPS:
            step = self.steps[step_name]
            with self.subTest(step=step_name):
                # Explicit env, falling back to the event context: the ci#745
                # failure mode was an empty target from skipped upstream jobs.
                self.assertRegex(step, r"\n\s+SOURCE_REPOSITORY:")
                self.assertRegex(step, r"\n\s+PR_NUMBER:")
                self.assertIn("github.repository", step)
                self.assertIn("github.event.pull_request.number", step)
                self.assertRegex(step, r'--repo "\$\{SOURCE_REPOSITORY\}"')
                self.assertRegex(step, r'--pr "\$\{PR_NUMBER\}"')
                self.assertRegex(step, r'--token "\$\{COMMENT_TOKEN\}"')

    def test_delegating_steps_have_no_inline_api_calls(self) -> None:
        """One helper owns the comment: no fourth copy of the curl block."""
        for step_name in DELEGATING_STEPS:
            step = self.steps[step_name]
            with self.subTest(step=step_name):
                self.assertIn(HELPER_REF, step)
                self.assertNotIn("api.github.com", step)
                # No inline HTTP call: `curl` may only appear in prose/comments.
                self.assertIsNone(re.search(r"^\s*curl\s", step, re.M), step[:200])

    def test_env_target_step_keeps_its_fail_safe(self) -> None:
        """The originally fixed job must keep env target, warning and exit 0."""
        for step_name in ENV_TARGET_STEPS:
            step = self.steps[step_name]
            with self.subTest(step=step_name):
                self.assertRegex(step, r"\n\s+SOURCE_REPOSITORY:")
                self.assertRegex(step, r"\n\s+PR_NUMBER:")
                self.assertIn("github.repository", step)
                self.assertIn("github.event.pull_request.number", step)
                self.assertIn("${SOURCE_REPOSITORY}", step)
                self.assertIn("${PR_NUMBER}", step)
                self.assertIn("::warning::", step)
                guard = step.index("::warning::")
                self.assertIn("exit 0", step[guard:])
                # The warning must precede any HTTP call.
                self.assertLess(guard, step.index("curl -fsSL"))

    def test_helper_owns_the_marker_fail_safe_and_upsert_contract(self) -> None:
        self.assertTrue(HELPER.is_file(), HELPER)
        self.assertTrue(os.access(HELPER, os.X_OK), f"{HELPER} must be executable")
        helper = HELPER.read_text(encoding="utf-8")
        # A shell helper: a leftover Actions expression would not expand.
        self.assertNotIn("${{", helper)
        self.assertIn(COMMENT_MARKER, helper)
        self.assertIn("::warning::", helper)
        self.assertIn("-X PATCH", helper)
        self.assertIn("-X POST", helper)
        self.assertIn("/issues/comments/${comment_id}", helper)
        self.assertIn("/issues/${PR_NUMBER}/comments", helper)
        # The fail-safe must warn, exit 0 and do so before any HTTP call.
        guard = helper.index("::warning::")
        self.assertIn("exit 0", helper[guard:])
        self.assertLess(guard, helper.index("curl -fsSL"))

    def test_after_tests_job_waits_for_the_suites_it_renders(self) -> None:
        """ci#833 follow-up: the terminal renderer must depend on its inputs.

        A `needs` list that omits the suites makes the `needs.*.result` reads
        below evaluate against an unscoped context, and lets the job run before
        the suites it reports on.
        """
        block = _job_block(AFTER_TESTS_JOB)
        needs = re.search(r"^\s+needs: \[(.*)\]$", block, re.M)
        self.assertIsNotNone(needs, block[:400])
        entries = {entry.strip() for entry in needs.group(1).split(",")}
        self.assertIn("run-pr-ci-tests-and-deployments", entries)
        for job in SUITE_JOBS:
            with self.subTest(job=job):
                self.assertIn(job, entries, sorted(entries))

    def test_after_tests_step_renders_terminal_test_status(self) -> None:
        """The `(4) test` bullet must end terminal, not re-echo the dispatch.

        Asserting on the *absence* of the dispatch-era label gates is what keeps
        a future copy/paste of the dispatch template from silently restoring the
        never-terminal `go=running` line.
        """
        step = self.steps[AFTER_TESTS_STEP]
        self.assertNotIn("should_test != ''", step)
        self.assertNotIn("should_ai_test_mock != ''", step)
        # `ai-real` genuinely only runs after an approved deploy, so it may stay
        # `running`; go and ai-mock must map their suite result.
        self.assertRegex(step, r"needs\.run-pr-ci-go-tests\.result == 'success'")
        self.assertRegex(step, r"needs\.run-pr-ci-ai-mock-tests\.result == 'success'")
        # A terminal render marks the bullet done instead of leaving `- [ ]`.
        self.assertIn("'- [x] `(4) test` `go={0}`", step)

    def test_helper_behaviour_test_is_wired_into_the_gate(self) -> None:
        """The shell behaviour test must run in CI, not only locally."""
        self.assertTrue(HELPER_TEST.is_file(), HELPER_TEST)
        self.assertTrue(os.access(HELPER_TEST, os.X_OK), f"{HELPER_TEST} must be executable")
        self.assertIn(
            "bash development/scripts/tests/test_upsert_pr_ci_progress_comment.sh",
            _workflow_text(),
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
