#!/usr/bin/env python3
"""Regression guard for ci#745.

The `Update Progress Comment After Tests` job in
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` used to build its
GitHub API URLs straight from
`needs.run-pr-ci-tests-and-deployments.outputs.source_repository` /
`...outputs.pr_number`. Whenever the upstream orchestration job was skipped
those outputs are empty, so the step posted to
`https://api.github.com/repos//issues//comments` and the whole job died with
`curl: (22) The requested URL returned error: 404` on every PR run (ci#745).

The fix threads the comment target through explicit step env (falling back to
the event context) and adds a fail-safe that skips the comment with a clear
warning when the target is still empty. These tests pin both halves so the
empty-URL 404 cannot silently come back.

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

STEP_NAME = "Render test-complete progress comment"
COMMENT_MARKER = "<!-- open-chat-pr-ci-progress -->"

# Step entries inside a job start at six spaces of indentation.
NEXT_STEP_RE = re.compile(r"\n {6}- ")


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _progress_comment_step() -> str:
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


class ProgressCommentTargetTest(unittest.TestCase):
    def setUp(self) -> None:
        self.step = _progress_comment_step()

    def test_comment_target_is_passed_as_explicit_env(self) -> None:
        """Repository and PR number must be handed to the inline script."""
        self.assertRegex(self.step, r"\n\s+SOURCE_REPOSITORY:")
        self.assertRegex(self.step, r"\n\s+PR_NUMBER:")
        # Fall back to the event context when the upstream outputs are empty.
        self.assertIn("github.repository", self.step)
        self.assertIn("github.event.pull_request.number", self.step)

    def test_urls_use_the_threaded_env(self) -> None:
        """Every API URL must interpolate the checked env, not raw outputs."""
        urls = re.findall(r"https://api\.github\.com/[^\"']*", self.step)
        # lookup + PATCH + POST
        self.assertGreaterEqual(len(urls), 3, urls)
        for url in urls:
            self.assertIn("${SOURCE_REPOSITORY}", url)
            # The pre-ci#745 form interpolated `${{ ...outputs.* }}` directly,
            # which collapsed to `repos//issues//comments`.
            self.assertNotIn("outputs.", url)
        self.assertIn("/issues/${PR_NUMBER}/comments", self.step)
        self.assertNotIn("/issues/comments/${comment_id}", urls[0])

    def test_fail_safe_skips_instead_of_404_looping(self) -> None:
        """An empty target must log a warning and exit 0, never curl."""
        self.assertRegex(self.step, r'\[ -z "\$\{SOURCE_REPOSITORY:-\}" \]')
        self.assertRegex(self.step, r'\[ -z "\$\{PR_NUMBER:-\}" \]')
        self.assertIn("::warning::", self.step)
        guard = self.step.index("::warning::")
        self.assertIn("exit 0", self.step[guard:])

    def test_lookup_and_update_semantics_are_kept(self) -> None:
        """One marker, one comment: lookup by marker, then PATCH or POST."""
        self.assertIn(COMMENT_MARKER, self.step)
        self.assertIn("/issues/comments/${comment_id}", self.step)
        self.assertIn("-X PATCH", self.step)
        self.assertIn("-X POST", self.step)


if __name__ == "__main__":
    unittest.main()
