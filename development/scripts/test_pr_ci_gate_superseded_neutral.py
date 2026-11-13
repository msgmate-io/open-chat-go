#!/usr/bin/env python3
"""Regression guard for ci#1069 (supersession gate never reads red for a live tree).

On PR #45 (head 7711516c) four `pull_request` runs were created within a few
seconds - the PR opened and its `test` label arrived almost together - and
concurrency cancelled all but the newest. The replacement run 43313025364 was
green on every job it had finished, yet `PR Test Merge Requirement` reported
`cancelled` for the commit and the required check stayed red, so a fully tested
tree could not merge. Two defects combined:

1. **The gate step accepted any exit-0 reconciler run.** `continue-on-error`
   hides the reconciler's exit code, and the step mapped `status == 0` to
   `accepted=true` without ever reading the reconciler's own verdict line. A
   truncated log, an unparseable payload or a stub could therefore accept a
   supersession that had no replacement behind it.

2. **The cancelled run's own gate decided.** A run cancelled before its gate job
   ran has no job conclusion to report; only a *replacement* run for the same
   commit that carries the `PR Test Merge Requirement` job can testify. The
   workflow now requires exit 0 **and** the explicit `accepted=true` line, so
   every fail-closed arm (hand-cancel, re-run, shutdown, API error, unparseable
   payload, yield without a replacement, missing reconciler, missing
   `pr-labels.txt`) keeps the check red, while an accepted supersession emits a
   `::notice::` naming the adopted run.

Deliberately stdlib-only and hermetic: `Pin Freshness Gate` runs this file with
the system `python3`, before the build-tools venv is guaranteed, and it must not
need network, a token or a GitHub API. Both `run:` blocks are extracted from
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` verbatim - the
assertions are about the shipped shell, never a copy - with the reconciler
replaced by a stub that replays a canned exit code and log. Extraction is
anchored on the step names, so a missing anchor is a hard failure rather than a
silently skipped test; the plan's stale line numbers are exactly why every
anchor is re-resolved here at runtime.
"""

from __future__ import annotations

import os
import pathlib
import re
import subprocess
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "run-pr-ci-tests-and-deployments.yaml"

RECONCILE_STEP = "- name: Reconcile a superseded (cancelled) CI run"
GATE_STEP = "- name: Enforce test label and passing tests"

# The `needs.<job>.result` value that keeps every unrelated arm of the gate
# green, so a test isolates the superseded-run arms.
GREEN_NEEDS = "success"


def _step_run_block(anchor):
    """The `run: |` shell of the workflow step whose `- name:` is `anchor`.

    Anchored on the step name and terminated at the next step at the same
    indentation, so the extraction survives the line drift that invalidated the
    ticket's original numbers. Raises rather than returning an empty block: a
    renamed step must fail this module loudly instead of passing vacuously.
    """
    if not WORKFLOW.is_file():
        raise AssertionError("workflow not found at %s" % WORKFLOW)
    lines = WORKFLOW.read_text(encoding="utf-8").splitlines()
    starts = [i for i, line in enumerate(lines) if line.strip() == anchor]
    if not starts:
        raise AssertionError("step anchor %r not found in %s" % (anchor, WORKFLOW.name))
    if len(starts) > 1:
        raise AssertionError("step anchor %r is ambiguous (%d hits)" % (anchor, len(starts)))
    start = starts[0]

    run_at = None
    for i in range(start, len(lines)):
        if lines[i].strip() == "run: |":
            run_at = i
            break
        if i > start and re.match(r"^      - name: ", lines[i]):
            break
    if run_at is None:
        raise AssertionError("step %r has no `run: |` block" % anchor)

    # Steps sit at 6 spaces, their keys at 8, so the shell body is indented 10.
    body = []
    for i in range(run_at + 1, len(lines)):
        line = lines[i]
        if line.strip() and not line.startswith("          "):
            break
        body.append(line[10:] if len(line) > 10 else "")
    while body and not body[-1].strip():
        body.pop()
    if not body:
        raise AssertionError("step %r has an empty `run: |` block" % anchor)
    return "\n".join(body) + "\n"


def substitute(script, values):
    """Replace the workflow's `${{ ... }}` expressions with literal values."""
    def repl(match):
        expression = match.group(0)
        if expression not in values:
            raise AssertionError("test does not stub the expression %r" % expression)
        return str(values[expression])

    return re.sub(r"\$\{\{[^}]*\}\}", repl, script)


RECONCILE_VALUES = {
    "${{ github.repository }}": "msgmate-io/open-chat-go",
    "${{ github.event.pull_request.number }}": "45",
    "${{ github.event.pull_request.head.sha }}": "7711516cdeadbeef",
    "${{ github.run_id }}": "43313025364",
    "${{ vars.PR_CI_SUPERSEDED_WAIT_SECONDS || '3600' }}": "5",
    "${{ vars.PR_CI_SUPERSEDED_YIELD_SECONDS || '60' }}": "0",
    "${{ vars.PR_CI_SUPERSEDED_EMPTY_GRACE_SECONDS || '30' }}": "0",
    "${{ secrets.BOT_TOKEN || secrets.GITHUB_TOKEN }}": "stub-token",
}

GATE_VALUES = {
    "${{ steps.superseded-reconcile.outputs.accepted }}": "",
    "${{ steps.superseded-reconcile.outputs.summary }}": "",
    "${{ github.event.pull_request.base.ref }}": "dev",
    "${{ needs.run-pr-ci-go-tests.result }}": GREEN_NEEDS,
    "${{ needs.run-pr-ci-go-tests.outputs.should_test }}": "true",
    "${{ needs.run-pr-ci-ai-mock-tests.result }}": GREEN_NEEDS,
    "${{ needs.run-pr-ci-e2e-tests.result }}": GREEN_NEEDS,
    "${{ needs.run-pr-ci-sandbox-tests-after-deploy.result }}": GREEN_NEEDS,
}

# Exit code the harness uses when bash itself blew up, so a syntax or `set -u`
# failure can never be mistaken for a legitimate gate verdict.
BASH_ERROR = 99


class Sandbox:
    """A throwaway checkout-shaped directory a workflow step can run in."""

    def __init__(self, tmp):
        self.root = pathlib.Path(tmp)
        self.runner_temp = self.root / "runner-temp"
        self.runner_temp.mkdir(parents=True, exist_ok=True)
        self.github_output = self.root / "github-output"
        self.github_output.write_text("", encoding="utf-8")
        self.scripts = self.root / "development" / "scripts"
        self.scripts.mkdir(parents=True, exist_ok=True)

    def install_reconciler(self, print_lines, exit_code):
        """Write a stub reconciler that replays `print_lines` and `exit_code`."""
        body = ["import sys"]
        for line in print_lines:
            body.append("print(%r)" % line)
        body.append("sys.exit(%d)" % exit_code)
        (self.scripts / "pr_ci_superseded_reconcile.py").write_text(
            "\n".join(body) + "\n", encoding="utf-8"
        )

    def write_labels(self, *labels):
        (self.runner_temp / "pr-labels.txt").write_text(
            "\n".join(labels) + "\n", encoding="utf-8"
        )

    def env(self):
        env = dict(os.environ)
        env.update(
            {
                "RUNNER_TEMP": str(self.runner_temp),
                "GITHUB_OUTPUT": str(self.github_output),
                "GITHUB_REPOSITORY": "msgmate-io/open-chat-go",
                "GITHUB_TOKEN": "stub-token",
                "HOME": str(self.root),
            }
        )
        # The stub reconciler is plain python3; keep the harness's own PATH so a
        # venv-less `Pin Freshness Gate` run behaves like the workflow runner.
        return env

    def run(self, script):
        completed = subprocess.run(
            ["bash", "-c", script],
            cwd=str(self.root),
            env=self.env(),
            capture_output=True,
            text=True,
        )
        return completed

    def outputs(self):
        """The step's `GITHUB_OUTPUT` key/value pairs."""
        pairs = {}
        for line in self.github_output.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            if line.startswith("summary="):
                pairs["summary"] = line[len("summary="):]
                continue
            if "=" not in line:
                raise AssertionError("malformed GITHUB_OUTPUT line %r" % line)
            key, value = line.split("=", 1)
            pairs[key] = value
        return pairs


class SupersededGateTests(unittest.TestCase):
    """ci#1069: the gate accepts only a reconciler exit 0 *and* `accepted=true`."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.sandbox = Sandbox(self.tmp.name)
        self.script = substitute(_step_run_block(RECONCILE_STEP), RECONCILE_VALUES)

    def test_extracted_blocks_are_the_shipped_shell(self):
        # Guard the extractor itself: if the anchors ever move to a step that no
        # longer mentions the reconciler, the assertions below would be vacuous.
        self.assertIn("pr_ci_superseded_reconcile.py", self.script)
        self.assertIn("accepted=true", self.script)
        gate = _step_run_block(GATE_STEP)
        self.assertIn("accept_superseded", gate)
        self.assertIn("pr-labels.txt", gate)

    def test_exit_zero_with_the_verdict_line_is_accepted(self):
        # The one arm ci#1069 needs: a replacement run for this commit testifies,
        # so the gate turns green and names the adopted verdict.
        self.sandbox.install_reconciler(
            ["accepted: replacement run 43313025364 gate job concluded success", "accepted=true"],
            0,
        )
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        outputs = self.sandbox.outputs()
        self.assertEqual(outputs.get("accepted"), "true", completed.stdout)
        self.assertIn("43313025364", outputs.get("summary", ""))

    def test_exit_zero_without_the_verdict_line_is_not_accepted(self):
        # A truncated log or a stub that exits 0 without deciding must never be
        # read as an acceptance: that was the ci#1069 defect.
        self.sandbox.install_reconciler(["accepted: replacement run 1 gate green"], 0)
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        self.assertEqual(self.sandbox.outputs().get("accepted"), "false", completed.stdout)

    def test_yield_without_a_replacement_is_not_accepted(self):
        # Exit 5 (yield_group) hands the concurrency group over; the replacement
        # run's gate owns the verdict, so this run must not claim acceptance.
        summary = "yielded: handed the concurrency group to a replacement run that has not started"
        self.sandbox.install_reconciler([summary, "accepted=false"], 5)
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        outputs = self.sandbox.outputs()
        self.assertEqual(outputs.get("accepted"), "false", completed.stdout)
        self.assertIn("yielded", outputs.get("summary", ""))

    def test_api_error_is_not_accepted_and_still_reports_a_summary(self):
        self.sandbox.install_reconciler(
            ["error: GitHub API returned HTTP 502 for /actions/runs/1/jobs", "accepted=false"], 4
        )
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        outputs = self.sandbox.outputs()
        self.assertEqual(outputs.get("accepted"), "false", completed.stdout)
        self.assertIn("HTTP 502", outputs.get("summary", ""))

    def test_unparseable_payload_is_not_accepted(self):
        # A traceback (unparseable run payload) exits non-zero with no verdict
        # line; the step must stay red and keep a greppable summary.
        self.sandbox.install_reconciler(
            ["Traceback (most recent call last):", "ValueError: unparseable run payload"], 1
        )
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        outputs = self.sandbox.outputs()
        self.assertEqual(outputs.get("accepted"), "false", completed.stdout)
        self.assertIn("unparseable run payload", outputs.get("summary", ""))

    def test_missing_reconciler_fails_closed_with_a_legible_summary(self):
        # No stub installed: the original "can't open file" was invisible behind
        # `continue-on-error`, which is how a missing checkout went unnoticed.
        completed = self.sandbox.run(self.script)
        self.assertNotEqual(completed.returncode, BASH_ERROR, completed.stderr)
        outputs = self.sandbox.outputs()
        self.assertEqual(outputs.get("accepted"), "false", completed.stdout)
        self.assertIn("missing", outputs.get("summary", ""))
        self.assertIn("::error::", completed.stdout)


class EnforceGateTests(unittest.TestCase):
    """ci#1069: every fail-closed arm keeps the check red; success is announced."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.sandbox = Sandbox(self.tmp.name)
        self.block = _step_run_block(GATE_STEP)
        self.sandbox.write_labels("test")

    def _run(self, accepted="", summary="", **overrides):
        values = dict(GATE_VALUES)
        values["${{ steps.superseded-reconcile.outputs.accepted }}"] = accepted
        values["${{ steps.superseded-reconcile.outputs.summary }}"] = summary
        for expression, value in overrides.items():
            values[expression] = value
        return self.sandbox.run(substitute(self.block, values))

    @staticmethod
    def _result(job):
        return "${{ needs.%s.result }}" % job

    def test_accepted_supersession_turns_the_gate_green_and_names_the_run(self):
        summary = "accepted: replacement run 43313025364 is queued and carries the 'PR Test Merge Requirement' job"
        completed = self._run(
            accepted="true",
            summary=summary,
            **{self._result("run-pr-ci-go-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
        self.assertIn("::notice::", completed.stdout)
        self.assertIn("43313025364", completed.stdout)
        self.assertNotIn("::error::", completed.stdout)

    def test_a_yielded_run_with_a_gating_replacement_turns_the_gate_green(self):
        # The exact 3-4-run burst: this attempt handed the concurrency group over
        # to a queued same-SHA attempt that carries the gate job, so the
        # reconciler accepts on exit 0 with the yield summary rather than exiting
        # 5. The gate must follow that verdict and name the replacement run.
        summary = (
            "accepted: replacement run 43313025364 for 7711516c is queued and carries "
            "the 'PR Test Merge Requirement' job, so its own check is newer and "
            "authoritative for this commit; superseded run 43313025301 defers to it."
        )
        completed = self._run(
            accepted="true",
            summary=summary,
            **{self._result("run-pr-ci-go-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
        self.assertIn("::notice::", completed.stdout)
        self.assertIn("43313025364", completed.stdout)
        self.assertNotIn("::error::", completed.stdout)

    def test_a_replacement_gate_that_went_red_keeps_the_gate_red(self):
        # Reconciler exit 1: a newer same-SHA run did reach the gate and concluded
        # something other than success, so this commit is genuinely red and the
        # acceptance path must not launder it.
        completed = self._run(
            accepted="false",
            summary="rejected: replacement run 43313025364 gate job concluded failure.",
            **{self._result("run-pr-ci-go-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("::error::", completed.stdout)
        self.assertNotIn("::notice::", completed.stdout)

    def test_hand_cancel_keeps_the_gate_red(self):
        # accepted=false is what a hand-cancel, a re-run or a shutdown leaves
        # behind: no replacement run testifies, so the check must stay red.
        completed = self._run(
            accepted="false",
            summary="missing: no replacement run reached the gate job",
            **{self._result("run-pr-ci-go-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("::error::", completed.stdout)
        self.assertIn("not accepted as superseded", completed.stdout)
        self.assertNotIn("::notice::", completed.stdout)

    def test_missing_accepted_output_keeps_the_gate_red(self):
        # The reconcile step is skipped when nothing was cancelled, so the output
        # is empty; an unrelated cancellation must never be read as accepted.
        completed = self._run(**{self._result("run-pr-ci-go-tests"): "cancelled"})
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("not accepted as superseded", completed.stdout)

    def test_accepted_supersession_without_the_label_snapshot_fails_closed(self):
        (self.sandbox.runner_temp / "pr-labels.txt").unlink()
        completed = self._run(
            accepted="true",
            summary="accepted: replacement run 43313025364 carries the gate job",
            **{self._result("run-pr-ci-go-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("pr-labels.txt is missing", completed.stdout)
        self.assertNotIn("::notice::", completed.stdout)

    def test_a_genuine_suite_failure_is_never_superseded(self):
        # A real failure is a verdict about this commit: acceptance must not
        # launder it, only a cancellation is reconcilable.
        completed = self._run(
            accepted="true",
            summary="accepted: replacement run 43313025364 carries the gate job",
            **{self._result("run-pr-ci-go-tests"): "failure"},
        )
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("PR CI tests failed.", completed.stdout)
        self.assertNotIn("::notice::", completed.stdout)

    def test_accepted_supersession_of_a_downstream_suite_is_announced(self):
        # The same acceptance covers the AI-mock/e2e/sandbox suites; the notice
        # names the suite that was superseded.
        completed = self._run(
            accepted="true",
            summary="accepted: replacement run 43313025364 carries the gate job",
            **{self._result("run-pr-ci-e2e-tests"): "cancelled"},
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
        self.assertIn("E2E", completed.stdout)

    def test_skipped_suite_with_the_test_label_stays_red(self):
        completed = self._run(**{self._result("run-pr-ci-go-tests"): "skipped"})
        self.assertEqual(completed.returncode, 1, completed.stdout + completed.stderr)
        self.assertIn("did not run for this commit", completed.stdout)


if __name__ == "__main__":
    unittest.main()
