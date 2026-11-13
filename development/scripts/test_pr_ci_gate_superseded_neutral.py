#!/usr/bin/env python3
"""ci#1069: pin the shipped `PR Test Merge Requirement` gate to "not red == exit 0".

Why this module exists
----------------------
The 3-4-run burst of 2026-05 (#1069) cancelled several CI runs for one commit
while a starved replacement sat behind this run's own `pr-ci-<PR>-<SHA>`
concurrency group. The reconciler *yielded* the group (exit 5) and the gate step
went red, so the PR looked broken although a newer run for the same SHA was
about to produce the real verdict.

The fix is behavioural, not textual: an accepted supersede must make the gate
step **exit 0** and emit a `::notice::` naming the replacement run. A `neutral`
conclusion would not do - a neutral check run does not satisfy a required status
context - so these tests execute the *shipped* shell out of the workflow file and
assert on real exit codes and real stdout.

Hermetic
--------
The module extracts the two step shells from
`.github/workflows/run-pr-ci-tests-and-deployments.yaml` by anchor string
(hard-failing when an anchor disappears, so a refactor cannot silently un-pin
this), renders `${{ ... }}` from an explicit fixture map (hard-failing on an
unknown expression), stubs `python3`/`gh` on `PATH`, and runs everything in a
throw-away directory. Standard library only; no network, no Docker, no GitHub.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(os.path.dirname(HERE))
WORKFLOW_RELPATH = ".github/workflows/run-pr-ci-tests-and-deployments.yaml"
WORKFLOW_PATH = os.path.join(REPO_ROOT, WORKFLOW_RELPATH)

RECONCILE_STEP = "Reconcile a superseded (cancelled) CI run"
GATE_STEP = "Enforce test label and passing tests"
RECONCILER_RELPATH = "development/scripts/pr_ci_superseded_reconcile.py"

GATE_FAILURE_STRING = "were cancelled and this run was not accepted as superseded"
GATE_ACCEPT_STRING = "adopting the verdict of replacement run"


def workflow_text():
    with open(WORKFLOW_PATH, encoding="utf-8") as handle:
        return handle.read()


def extract_step(text, step_name):
    """Return the dedented `run:` shell of the step named `step_name`.

    Anchored on the step name and bounded by the run block's own indentation, so
    it keeps working across unrelated workflow edits - but it hard-fails (rather
    than silently returning nothing) when the anchor itself moves or disappears.
    """
    marker = "      - name: %s" % step_name
    if marker not in text:
        raise AssertionError(
            "anchor `- name: %s` is gone from %s; ci#1069's pin cannot be verified"
            % (step_name, WORKFLOW_RELPATH)
        )
    lines = text[text.index(marker):].splitlines()
    run_index = None
    for index, line in enumerate(lines):
        if line == "        run: |":
            run_index = index
            break
        if index and line.strip() and not line.startswith("        "):
            break
    if run_index is None:
        raise AssertionError("step %r has no `run: |` block" % step_name)
    body = []
    for line in lines[run_index + 1:]:
        if line.startswith("          "):
            body.append(line[10:])
        elif not line.strip():
            body.append("")
        else:
            break
    shell = "\n".join(body).rstrip() + "\n"
    if step_name == GATE_STEP and "accept_superseded" not in shell:
        raise AssertionError("the gate step no longer defines accept_superseded")
    return shell


def render(shell, expressions, label):
    """Substitute every `${{ ... }}` with a fixture value; fail on the unknown.

    Unknown expressions are a hard failure on purpose: a new expression in these
    steps (say another `needs.*.result`) must be added to the fixture map, not
    silently rendered as the empty string.
    """
    out = []
    index = 0
    while True:
        start = shell.find("${{", index)
        if start == -1:
            out.append(shell[index:])
            break
        end = shell.find("}}", start)
        if end == -1:
            raise AssertionError("%s: unterminated ${{ in shipped shell" % label)
        expression = " ".join(shell[start + 3:end].split())
        if expression not in expressions:
            raise AssertionError(
                "%s: unhandled workflow expression `${{ %s }}`; add it to the "
                "fixture map so the shipped shell stays executable here" % (label, expression)
            )
        out.append(shell[index:start])
        out.append(expressions[expression])
        index = end + 2
    return "".join(out)


def gate_expressions(accepted, replacement_run, go_tests="cancelled", should_test="",
                     base_ref="dev", ai_mock="success", e2e="success", sandbox="success"):
    return {
        "steps.superseded-reconcile.outputs.accepted": accepted,
        "steps.superseded-reconcile.outputs.replacement_run": replacement_run,
        "github.event.pull_request.base.ref": base_ref,
        "needs.run-pr-ci-go-tests.result": go_tests,
        "needs.run-pr-ci-go-tests.outputs.should_test": should_test,
        "needs.run-pr-ci-ai-mock-tests.result": ai_mock,
        "needs.run-pr-ci-e2e-tests.result": e2e,
        "needs.run-pr-ci-sandbox-tests-after-deploy.result": sandbox,
    }


RECONCILE_EXPRESSIONS = {
    "secrets.BOT_TOKEN || secrets.GITHUB_TOKEN": "stub-token",
    "vars.PR_CI_SUPERSEDED_WAIT_SECONDS || '3600'": "0",
    "vars.PR_CI_SUPERSEDED_YIELD_SECONDS || '60'": "60",
    "vars.PR_CI_SUPERSEDED_EMPTY_GRACE_SECONDS || '30'": "0",
    "github.repository": "msgmate-io/open-chat-go",
    "github.event.pull_request.number": "1069",
    "github.event.pull_request.head.sha": "a" * 40,
    "github.run_id": "90000000001",
}

# A stub `python3` that fails loudly if the shell ever stops invoking the
# reconciler with the flags the production step uses, then replays a canned
# reconciler transcript.
STUB_PYTHON = """#!/usr/bin/env bash
set -uo pipefail
printf '%s\\n' "$*" >> "${STUB_PYTHON_CALLS:?}"
if [ -n "${STUB_STDOUT:-}" ]; then
  printf '%s\\n' "${STUB_STDOUT}"
fi
exit "${STUB_EXIT:-0}"
"""

# The gate step shells out to `gh` only in the after-deploy arms these fixtures
# never reach; keep a stub anyway so a future arm cannot reach the network.
STUB_GH = """#!/usr/bin/env bash
echo "gh stub: query refused in hermetic tests (args: $*)" >&2
exit 1
"""


class ShippedShellTestCase(unittest.TestCase):
    """Base class: extract, render and run the two shipped step shells."""

    @classmethod
    def setUpClass(cls):
        text = workflow_text()
        cls.reconcile_shell = extract_step(text, RECONCILE_STEP)
        cls.gate_shell = extract_step(text, GATE_STEP)

    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="ci1069-")
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.bin = os.path.join(self.tmp, "bin")
        os.makedirs(self.bin)
        self._write_executable("python3", STUB_PYTHON)
        self._write_executable("gh", STUB_GH)
        self.runner_temp = os.path.join(self.tmp, "runner-temp")
        os.makedirs(self.runner_temp)
        self.github_output = os.path.join(self.tmp, "github-output")
        open(self.github_output, "w").close()
        self.calls = os.path.join(self.tmp, "python3-calls")
        open(self.calls, "w").close()
        # The reconciler script the production step requires in the checkout.
        reconciler_dir = os.path.join(self.tmp, "development", "scripts")
        os.makedirs(reconciler_dir)
        self.reconciler_dir = reconciler_dir

    def _write_executable(self, name, body):
        path = os.path.join(self.bin, name)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(body)
        os.chmod(path, 0o755)

    def write_labels(self, labels=("test",)):
        with open(os.path.join(self.runner_temp, "pr-labels.txt"), "w", encoding="utf-8") as handle:
            handle.write("\n".join(labels) + "\n")

    def add_reconciler(self):
        path = os.path.join(self.reconciler_dir, "pr_ci_superseded_reconcile.py")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write("# stub placeholder; `python3` is stubbed on PATH\n")

    def run_shell(self, shell, env_extra=None):
        env = {
            "PATH": self.bin + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": self.tmp,
            "RUNNER_TEMP": self.runner_temp,
            "GITHUB_OUTPUT": self.github_output,
            "GITHUB_WORKSPACE": self.tmp,
            "STUB_PYTHON_CALLS": self.calls,
        }
        env.update(env_extra or {})
        return subprocess.run(
            ["bash", "--noprofile", "--norc", "-c", shell],
            cwd=self.tmp,
            env=env,
            capture_output=True,
            text=True,
            timeout=120,
        )

    def outputs(self):
        values = {}
        with open(self.github_output, encoding="utf-8") as handle:
            for line in handle:
                line = line.rstrip("\n")
                if "=" in line:
                    key, _, value = line.partition("=")
                    values[key] = value
        return values

    def run_gate(self, accepted="false", replacement_run="", **needs):
        shell = render(self.gate_shell,
                       gate_expressions(accepted, replacement_run, **needs),
                       GATE_STEP)
        return self.run_shell(shell)

    def run_reconcile(self, stub_stdout="", stub_exit=0):
        shell = render(self.reconcile_shell, RECONCILE_EXPRESSIONS, RECONCILE_STEP)
        return self.run_shell(shell, {
            "STUB_STDOUT": stub_stdout,
            "STUB_EXIT": str(stub_exit),
        })


class ReconcileStepTests(ShippedShellTestCase):
    """The reconcile step must publish `accepted` *and* `replacement_run`."""

    def test_accepted_supersede_publishes_the_replacement_run(self):
        self.add_reconciler()
        self.write_labels()
        result = self.run_reconcile(
            stub_stdout="accepted: superseded.\nreplacement_run=424242\naccepted=true",
            stub_exit=0,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"accepted": "true", "replacement_run": "424242"})
        self.assertIn("replacement_run=424242", result.stdout,
                      "the reconciler transcript is still echoed to the log")
        with open(self.calls, encoding="utf-8") as handle:
            invocation = handle.read()
        self.assertIn("--repo msgmate-io/open-chat-go", invocation)
        self.assertIn("--pr 1069", invocation)
        self.assertIn("--head-sha " + "a" * 40, invocation)
        self.assertIn("--run-id 90000000001", invocation)

    def test_yielded_without_a_replacement_publishes_accepted_false(self):
        self.add_reconciler()
        self.write_labels()
        result = self.run_reconcile(
            stub_stdout="yielded: handed the concurrency group over.\nreplacement_run=\naccepted=false",
            stub_exit=5,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"accepted": "false", "replacement_run": ""})

    def test_non_zero_reconciler_exit_is_never_accepted(self):
        self.add_reconciler()
        self.write_labels()
        for exit_code in (1, 2, 3, 4):
            with self.subTest(exit_code=exit_code):
                open(self.github_output, "w").close()
                result = self.run_reconcile(
                    stub_stdout="error: something went wrong.\nreplacement_run=\naccepted=false",
                    stub_exit=exit_code,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.outputs().get("accepted"), "false")

    def test_reconciler_crash_is_never_accepted(self):
        self.add_reconciler()
        self.write_labels()
        for exit_code in (137, 134, 255):
            with self.subTest(exit_code=exit_code):
                open(self.github_output, "w").close()
                result = self.run_reconcile(stub_stdout="", stub_exit=exit_code)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.outputs(), {"accepted": "false", "replacement_run": ""})

    def test_a_stale_accepted_line_cannot_turn_a_failure_into_an_acceptance(self):
        """Acceptance comes from the exit code, never from parsing the transcript."""
        self.add_reconciler()
        self.write_labels()
        result = self.run_reconcile(
            stub_stdout="replacement_run=424242\naccepted=true",
            stub_exit=5,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs().get("accepted"), "false")
        self.assertEqual(self.outputs().get("replacement_run"), "")

    def test_missing_reconciler_fails_closed(self):
        self.write_labels()
        result = self.run_reconcile(stub_stdout="", stub_exit=0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"accepted": "false", "replacement_run": ""})
        self.assertIn("::error::", result.stdout)
        self.assertIn("pr_ci_superseded_reconcile.py is missing", result.stdout)
        with open(self.calls, encoding="utf-8") as handle:
            self.assertEqual(handle.read(), "", "the missing script is never executed")

    def test_unparsable_transcript_publishes_an_empty_replacement(self):
        self.add_reconciler()
        self.write_labels()
        result = self.run_reconcile(stub_stdout="accepted: fine, no id line", stub_exit=0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.outputs(), {"accepted": "true", "replacement_run": ""})


class GateStepTests(ShippedShellTestCase):
    """The shipped gate step: accepted supersede -> exit 0, everything else red."""

    def test_accepted_supersede_with_a_replacement_run_exits_zero(self):
        """ci#1069: not-red must mean exit 0, and the notice must name the run."""
        self.write_labels()
        result = self.run_gate(accepted="true", replacement_run="424242",
                               should_test="")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("::notice::", result.stdout)
        self.assertIn("424242", result.stdout,
                      "the notice must name the replacement run id")
        self.assertIn(GATE_ACCEPT_STRING, result.stdout)
        self.assertNotIn("::error::", result.stdout)
        self.assertNotIn(GATE_FAILURE_STRING, result.stdout)

    def test_accepted_supersede_without_a_replacement_run_still_exits_zero(self):
        """A closed PR / moved head is an acceptance with no run to name."""
        self.write_labels()
        result = self.run_gate(accepted="true", replacement_run="")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("::notice::", result.stdout)
        self.assertIn("a newer run for the same commit", result.stdout)
        self.assertNotIn("::error::", result.stdout)

    def test_accepted_supersede_does_not_emit_a_neutral_conclusion(self):
        """A `neutral` ending would not satisfy a required status context."""
        self.write_labels()
        result = self.run_gate(accepted="true", replacement_run="424242")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("neutral", result.stdout.lower())

    def test_yield_without_a_replacement_keeps_the_gate_red(self):
        self.write_labels()
        result = self.run_gate(accepted="false", replacement_run="")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn(GATE_FAILURE_STRING, result.stdout)
        self.assertIn("::error::", result.stdout)

    def test_reconciler_exit_one_keeps_the_gate_red(self):
        self.write_labels()
        result = self.run_gate(accepted="false", replacement_run="")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn(GATE_FAILURE_STRING, result.stdout)

    def test_a_genuine_suite_failure_stays_red_even_when_accepted(self):
        """A real `failure)` arm is not a supersede and must never be masked."""
        self.write_labels()
        result = self.run_gate(accepted="true", replacement_run="424242",
                               go_tests="failure")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("PR CI tests failed.", result.stdout)

    def test_missing_labels_file_keeps_the_gate_red(self):
        result = self.run_gate(accepted="true", replacement_run="424242")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("PR is missing the required test label.", result.stdout)


class EndToEndTests(ShippedShellTestCase):
    """Reconcile step output feeds the gate step - the actual production wiring."""

    def gate_with_outputs(self, outputs, **needs):
        shell = render(
            self.gate_shell,
            gate_expressions(outputs.get("accepted", ""), outputs.get("replacement_run", ""), **needs),
            GATE_STEP,
        )
        return self.run_shell(shell)

    def test_yield_with_a_gate_carrying_replacement_is_green_end_to_end(self):
        """ci#1069's bug: the yielded arm used to dye the gate red."""
        self.add_reconciler()
        self.write_labels()
        reconciled = self.run_reconcile(
            stub_stdout=(
                "accepted: yielded: replacement run 424242 (status in_progress) for aaaaaaaaaaaa "
                "owns the authoritative 'PR Test Merge Requirement' verdict for this commit."
                "\nreplacement_run=424242\naccepted=true"
            ),
            stub_exit=0,
        )
        self.assertEqual(reconciled.returncode, 0, reconciled.stderr)
        gate = self.gate_with_outputs(self.outputs(), should_test="")
        self.assertEqual(gate.returncode, 0, gate.stdout + gate.stderr)
        self.assertIn("::notice::", gate.stdout)
        self.assertIn("424242", gate.stdout)

    def test_yield_without_a_replacement_is_red_end_to_end(self):
        self.add_reconciler()
        self.write_labels()
        reconciled = self.run_reconcile(
            stub_stdout="yielded: handed the concurrency group over, no replacement.\nreplacement_run=\naccepted=false",
            stub_exit=5,
        )
        self.assertEqual(reconciled.returncode, 0, reconciled.stderr)
        self.assertEqual(self.outputs().get("accepted"), "false")
        gate = self.gate_with_outputs(self.outputs())
        self.assertEqual(gate.returncode, 1, gate.stdout + gate.stderr)
        self.assertIn(GATE_FAILURE_STRING, gate.stdout)

    def test_missing_reconciler_is_red_end_to_end(self):
        self.write_labels()
        reconciled = self.run_reconcile()
        self.assertEqual(reconciled.returncode, 0, reconciled.stderr)
        gate = self.gate_with_outputs(self.outputs())
        self.assertEqual(gate.returncode, 1, gate.stdout + gate.stderr)
        self.assertIn(GATE_FAILURE_STRING, gate.stdout)

    def test_accepted_supersede_skips_only_the_should_test_guard(self):
        """The untested-tree guard stays armed for every non-superseded run."""
        self.write_labels()
        skipped = self.run_gate(accepted="false", replacement_run="",
                                go_tests="success", should_test="")
        self.assertEqual(skipped.returncode, 1, skipped.stdout + skipped.stderr)
        self.assertIn("Tests were not executed despite the test label.", skipped.stdout)


class WorkflowPinTests(unittest.TestCase):
    """Textual pins that keep the behavioural fix from being refactored away."""

    def setUp(self):
        self.text = workflow_text()
        self.gate = extract_step(self.text, GATE_STEP)
        self.reconcile = extract_step(self.text, RECONCILE_STEP)

    def test_the_acceptance_notice_is_present_and_does_not_say_neutral(self):
        self.assertIn("::notice::", "".join(
            line for line in self.gate.splitlines() if "adopting the verdict" in line
        ))
        self.assertNotIn("neutral", self.gate.lower())

    def test_the_reconcile_step_publishes_both_outputs(self):
        self.assertIn('echo "accepted=${accepted}" >> "${GITHUB_OUTPUT}"', self.reconcile)
        self.assertIn('echo "replacement_run=${replacement_run}" >> "${GITHUB_OUTPUT}"',
                      self.reconcile)
        self.assertIn("continue-on-error: true", self.text)

    def test_the_gate_step_keeps_its_failure_string(self):
        self.assertIn(GATE_FAILURE_STRING, self.gate)


if __name__ == "__main__":
    sys.exit(unittest.main())
