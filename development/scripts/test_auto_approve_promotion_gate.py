#!/usr/bin/env python3
"""ci#1066: the auto-approve step must *classify* an approval block.

The `main` ruleset demands "1 approving review by reviewers with write access".
A `github.token` review does not satisfy that, and the `dev` -> `main` promotion
PR's author cannot approve its own PR, so on this repository the workflow could
not produce the approval it needs at all: every promotion run ended red with

    GraphQL: Repository rule violations found
    At least 1 approving review is required by reviewers with write access
    Could not merge PR #36 (no usable approval and no ruleset bypass ...)

Even though the PR itself was fully green (ci#1066). That red run is not a
regression signal, it is an admin-configuration state, so the shipped step now
distinguishes three cases:

* ``CONSOLIDATE_APPROVER_TOKEN`` missing  -> the `dev` -> `main` hop reports
  ``::notice::... Awaiting maintainer merge (ci#1066)``, posts one idempotent PR
  comment and does **not** fail the run;
* token rejected (dead identity) or accepted but without write access -> fatal,
  because no approval can ever be posted until an admin re-issues it;
* any other block (in particular a consolidation PR against `dev`, whose only
  required gate is the check) -> still fails hard.

These tests extract the shipped ``Approve and merge`` step verbatim and run it
against a stubbed ``gh`` that reproduces the ci#1066 API responses
(``mergeStateStatus=BLOCKED``, ``reviewDecision=REVIEW_REQUIRED``, failing
``gh pr review``/``gh pr merge``), so the classification cannot silently regress.

Run with: ``python3 -m unittest development/scripts/test_auto_approve_promotion_gate.py -v``
"""

from __future__ import annotations

import json
import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

WORKFLOW = (
    pathlib.Path(__file__).resolve().parents[2]
    / ".github"
    / "workflows"
    / "auto-approve-consolidation.yaml"
)

STEP_NAME = "Approve and merge"

REPO = "example/example"

# The step's `run:` body is indented ten spaces inside the YAML file.
RUN_INDENT = 10

AWAITING_MARKER = "Awaiting maintainer merge (ci#1066)"


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _step_script() -> str:
    """Extract the `Approve and merge` step's shell, dedented, verbatim."""
    lines = _workflow_text().splitlines()
    try:
        start = next(
            idx for idx, line in enumerate(lines) if line.strip() == f"- name: {STEP_NAME}"
        )
    except StopIteration:  # pragma: no cover - a rename is the failure
        raise AssertionError(
            f"step {STEP_NAME!r} not found in {WORKFLOW.name}: was it renamed "
            "without updating this guard?"
        ) from None

    body: list[str] = []
    in_run = False
    for line in lines[start + 1 :]:
        if not in_run:
            if line.strip() == "run: |":
                in_run = True
            continue
        if line.strip() and len(line) - len(line.lstrip()) < RUN_INDENT:
            break
        body.append(line[RUN_INDENT:] if line.strip() else "")

    assert body, "the step's `run:` block is empty"
    return "\n".join(body)


def _executable_script() -> str:
    """The extracted shell with any `${{ ... }}` expressions substituted."""
    script = _step_script()
    for expression, value in (
        ("${{ github.repository }}", REPO),
        ("${{ github.event_name }}", "workflow_run"),
        ("${{ github.event.workflow_run.html_url }}", "https://example/run/1"),
    ):
        script = script.replace(expression, value)
    assert "${{" not in script, "unsubstituted workflow expression in the step body"
    return script


def _rollup(status: str = "COMPLETED", conclusion: str = "SUCCESS") -> list:
    """A green (or given) required CI check plus one skipped context."""
    return [
        {
            "name": "Run PR CI Tests and Deployments",
            "status": status,
            "conclusion": conclusion,
            "detailsUrl": "https://github.com/example/example/actions/runs/111",
            "app": {"id": 15368},
            "completedAt": "2026-10-20T13:40:00Z",
        },
        {
            "name": "Sandbox E2E (skipped)",
            "status": "COMPLETED",
            "conclusion": "SKIPPED",
            "detailsUrl": "https://github.com/example/example/actions/runs/112",
            "app": {"id": 15368},
            "completedAt": "2026-10-20T13:35:00Z",
        },
    ]


def _promotion_pr(number: int = 36) -> dict:
    """The promotion PR exactly as ci#1066 reported it: green but BLOCKED."""
    return {
        "number": number,
        "state": "OPEN",
        "isDraft": False,
        "baseRefName": "main",
        "headRefName": "dev",
        "author": {"login": "cur1ousdude"},
        "mergeable": "MERGEABLE",
        "mergeStateStatus": "BLOCKED",
        "reviewDecision": "REVIEW_REQUIRED",
        "reviews": [
            {
                "state": "DISMISSED",
                "author": {"login": "tbscode"},
            }
        ],
        "statusCheckRollup": _rollup(),
    }


def _consolidation_pr(number: int = 43) -> dict:
    return {
        "number": number,
        "state": "OPEN",
        "isDraft": False,
        "baseRefName": "dev",
        "headRefName": "chore/consolidate-r64-2026-11-12",
        "author": {"login": "cur1ousdude"},
        "mergeable": "MERGEABLE",
        "mergeStateStatus": "BLOCKED",
        "reviewDecision": "REVIEW_REQUIRED",
        "reviews": [],
        "statusCheckRollup": _rollup(),
    }


# The `gh` stub. It answers the reads the step performs and reproduces the
# ci#1066 write failures: `gh pr review` and `gh pr merge` (plain and --admin)
# both fail, which is what a ruleset requiring a write-access approval does to
# the acting identities. Every invocation is logged so the tests can assert on
# the calls (in particular that the awaiting-merge note is posted at most once).
_GH_STUB = r"""#!/usr/bin/env python3
import json, os, sys

argv = sys.argv[1:]
with open(os.environ["GH_LOG"], "a", encoding="utf-8") as log:
    log.write(" ".join(argv) + "\n")


def fail(message):
    sys.stderr.write(message + "\n")
    sys.exit(1)


def fixture(name):
    with open(os.path.join(os.environ["GH_FIXTURES"], name), encoding="utf-8") as handle:
        return handle.read().strip()


def flag_value(flag):
    if flag in argv:
        return argv[argv.index(flag) + 1]
    return ""


if not argv:
    fail("no arguments")

if argv[0] == "api":
    if "user" in argv:
        if fixture("user") == "@fail":
            fail("HTTP 401: Bad credentials")
        print(fixture("user"))
        sys.exit(0)
    if "--method" in argv:  # PUT .../update-branch
        print("{}")
        sys.exit(0)
    # `repos/<repo> --jq '.permissions.push // false'` permission probe.
    permissions = fixture("permissions")
    if permissions == "@fail":
        fail("HTTP 500")
    print("true" if permissions == "true" else "false")
    sys.exit(0)

if argv[0] == "pr":
    action = argv[1] if len(argv) > 1 else ""
    if action == "view":
        fields = flag_value("--json")
        if fields == "state":
            print(fixture("state"))
            sys.exit(0)
        if fields == "comments":
            print(fixture("comment_count"))
            sys.exit(0)
        if fields == "labels":
            print("")
            sys.exit(0)
        print(fixture("pr"))
        sys.exit(0)
    if action == "review":
        fail("GraphQL: Could not resolve to a node with the global id (cannot approve)")
    if action == "merge":
        fail(
            "GraphQL: Repository rule violations found\n"
            "At least 1 approving review is required by reviewers with write access."
        )
    if action == "comment":
        sys.exit(0)
    fail("unexpected gh pr " + action)

fail("unexpected gh invocation: " + " ".join(argv))
"""


class ApproveAndMergeRun:
    """One execution of the shipped `Approve and merge` step against a stub."""

    def __init__(
        self,
        pr: dict,
        *,
        has_approver_token: bool,
        user: str = "github-actions[bot]",
        permissions: str = "false",
        state: str = "OPEN",
        existing_notes: int = 0,
        prs: str = "",
    ) -> None:
        self.pr = pr
        self.has_approver_token = has_approver_token
        self.user = user
        self.permissions = permissions
        self.state = state
        self.existing_notes = existing_notes
        self.prs = prs or str(pr["number"])

    def run(self) -> tuple[int, str, list[str]]:
        work = tempfile.mkdtemp(prefix="ci1066-")
        bindir = os.path.join(work, "bin")
        fixtures = os.path.join(work, "fixtures")
        os.makedirs(bindir)
        os.makedirs(fixtures)

        def write_stub(name: str, content: str) -> None:
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)

        write_stub("gh", _GH_STUB)
        # The merge retry backs off 10s; keep the test instant.
        write_stub("sleep", "#!/usr/bin/env bash\nexit 0\n")

        for name, value in (
            ("user", self.user),
            ("permissions", self.permissions),
            ("state", self.state),
            ("comment_count", str(self.existing_notes)),
            ("pr", json.dumps(self.pr)),
        ):
            with open(os.path.join(fixtures, name), "w", encoding="utf-8") as handle:
                handle.write(value)

        log_path = os.path.join(work, "gh.log")
        open(log_path, "w", encoding="utf-8").close()

        script_path = os.path.join(work, "approve_and_merge.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write(_executable_script())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "GH_LOG": log_path,
                "GH_FIXTURES": fixtures,
                "GH_TOKEN": "stub-token",
                "HAS_APPROVER_TOKEN": "true" if self.has_approver_token else "false",
                "EVENT_NAME": "workflow_run",
                "PRS": self.prs,
                "RUN_URL": "https://example/run/1",
                "REPOSITORY": REPO,
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )
        with open(log_path, encoding="utf-8") as handle:
            calls = [line for line in handle.read().splitlines() if line]
        return result.returncode, result.stdout + result.stderr, calls


def _comments(calls: list[str]) -> list[str]:
    return [call for call in calls if call.startswith("pr comment ")]


class ApprovalGateClassificationTest(unittest.TestCase):
    def test_the_step_shell_is_valid(self) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(_executable_script())
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_promotion_pr_without_approver_identity_awaits_a_maintainer(self) -> None:
        """ci#1066: green promotion PR + no write-access identity is not a failure."""
        code, output, calls = ApproveAndMergeRun(
            _promotion_pr(), has_approver_token=False
        ).run()

        self.assertEqual(code, 0, output)
        self.assertIn(AWAITING_MARKER, output)
        self.assertIn("::notice::", output)
        # No failure: a red run must keep meaning "a real regression".
        self.assertNotIn("::error::", output)
        # The merge was attempted (and correctly refused by the ruleset), and
        # the PR got exactly one awaiting-maintainer-merge note.
        self.assertTrue(
            any(call.startswith("pr merge 36 --repo example/example --merge") for call in calls),
            calls,
        )
        self.assertEqual(len(_comments(calls)), 1, calls)
        self.assertIn("ci#1066", _comments(calls)[0])

    def test_the_awaiting_note_is_not_repeated(self) -> None:
        """The comment is idempotent: a re-run must not spam the PR."""
        code, output, calls = ApproveAndMergeRun(
            _promotion_pr(), has_approver_token=False, existing_notes=1
        ).run()

        self.assertEqual(code, 0, output)
        self.assertIn("already carries the awaiting-maintainer-merge note", output)
        self.assertEqual(_comments(calls), [], calls)

    def test_rejected_approver_token_is_fatal(self) -> None:
        """A configured-but-dead token must page the admin, not be swallowed."""
        code, output, _ = ApproveAndMergeRun(
            _promotion_pr(), has_approver_token=True, user="@fail"
        ).run()

        self.assertEqual(code, 1, output)
        self.assertIn("rejected by the API (HTTP 401)", output)

    def test_approver_token_without_write_access_is_fatal(self) -> None:
        """ci#1066 root cause 1: the token identity is not a write-access reviewer."""
        code, output, _ = ApproveAndMergeRun(
            _promotion_pr(), has_approver_token=True, permissions="false"
        ).run()

        self.assertEqual(code, 1, output)
        self.assertIn("no write access", output)

    def test_a_write_access_identity_is_used_for_the_approval(self) -> None:
        """The happy path still tries to approve as the configured identity."""
        code, output, calls = ApproveAndMergeRun(
            _promotion_pr(),
            has_approver_token=True,
            user="some-maintainer",
            permissions="true",
        ).run()

        self.assertTrue(
            any(call.startswith("pr review 36 --repo example/example --approve") for call in calls),
            calls,
        )
        # The stub cannot approve, and there is no bypass, so this still ends in
        # the explicit "token configured but unusable" failure rather than the
        # maintainer-merge notice.
        self.assertEqual(code, 1, output)
        self.assertIn("CONSOLIDATE_APPROVER_TOKEN is configured", output)
        self.assertNotIn(AWAITING_MARKER, output)

    def test_a_blocked_consolidation_pr_still_fails(self) -> None:
        """The `dev` integration hop only needs the check; a block there is real."""
        code, output, _ = ApproveAndMergeRun(
            _consolidation_pr(), has_approver_token=False
        ).run()

        self.assertEqual(code, 1, output)
        self.assertNotIn(AWAITING_MARKER, output)
        self.assertIn("Could not merge PR #43", output)

    def test_the_ruleset_bypass_is_tried_before_reporting_the_block(self) -> None:
        """The awaiting path must not shadow the registered-bypass route."""
        code, output, calls = ApproveAndMergeRun(
            _promotion_pr(), has_approver_token=False
        ).run()

        self.assertEqual(code, 0, output)
        self.assertTrue(
            any(call.startswith("pr merge 36") and call.endswith("--admin") for call in calls),
            calls,
        )

    def test_not_green_promotion_pr_is_not_classified_as_awaiting(self) -> None:
        """A failing check keeps the hard failure: that *is* a regression."""
        pr = _promotion_pr()
        pr["statusCheckRollup"] = _rollup(conclusion="FAILURE")
        code, output, _ = ApproveAndMergeRun(pr, has_approver_token=False).run()

        self.assertEqual(code, 0, output)
        self.assertNotIn(AWAITING_MARKER, output)
        self.assertIn("is not green", output)


class WorkflowDocumentationTest(unittest.TestCase):
    def test_the_workflow_records_the_ci_1066_policy(self) -> None:
        text = _workflow_text()
        self.assertIn("ci#1066", text)
        self.assertIn(AWAITING_MARKER, text)

    def test_the_preflight_checks_the_write_access(self) -> None:
        """ci#1066 root cause 1: distinguish missing, rejected and unprivileged."""
        script = _step_script()
        self.assertIn("repos/${REPO}", script)
        self.assertIn("permissions.push", script)
        self.assertIn("APPROVER_READY", script)


if __name__ == "__main__":
    unittest.main()
