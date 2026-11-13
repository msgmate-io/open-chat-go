#!/usr/bin/env python3
"""Regression guard for ci#1067 - "green, awaiting a maintainer merge" vs a regression.

`.github/workflows/auto-approve-consolidation.yaml` -> `Approve and merge` ended
**red** on 2026-11-12 (run 43298179797) for a fully green promotion PR (25
SUCCESS, 20 SKIPPED, `MERGEABLE`) whose only missing ingredient was a
write-access approval:

    GraphQL: Repository rule violations found
    At least 1 approving review is required by reviewers with write access.
    ...
    Could not merge PR #36 (no usable approval and no ruleset bypass ...).

Because that produced a red run, a red `auto-approve` run no longer meant
"something is broken". ci#1067 splits the outcomes:

* `token_rejected` - `CONSOLIDATE_APPROVER_TOKEN` is configured but the API
  refuses it (HTTP 401 / no resolvable `/user`): the step names it, emits a
  `::notice::` and exits 0 *before* the PR loop (every call would fail with the
  same 401 and be misreported as an unreadable PR).
* `token_missing` - no approver token and no resolvable fallback identity: warn,
  keep going, let the review call be the authoritative check.
* a ruleset refusal (`approving review|review is required by reviewers with
  write access`) is classified, reported and never retried with the 10s backoff;
* green-but-unapproved PRs land in `awaiting_human` and in the job summary as
  "### Auto-approve: N green PR(s) awaiting a maintainer merge"; the run is green;
* an unreadable PR, a conflicting PR or a merge failing for any other reason
  still fails the run.

Like `test_auto_approve_flaky_pulls.py` / `test_auto_approve_stale_check_instances.py`,
this file extracts the step's shell **verbatim** from the workflow and runs it
against stubbed `gh`/`sleep` in a temp `bin/` on `PATH`. No network, no token, no
user data. Deliberately stdlib-only: the PR CI job runs it with the system
`python3`.
"""

from __future__ import annotations

import json
import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "auto-approve-consolidation.yaml"

STEP_NAME = "Approve and merge"

REPO = "example/example"
RUN_URL = "https://github.com/example/example/actions/runs/43298179797"
# A value shaped like a token: it must never reach stdout or the summary.
FAKE_TOKEN = "ghs_fakeApproverTokenDoNotLeak0123456789"
RULE_REFUSAL = (
    "GraphQL: Repository rule violations found\n"
    "At least 1 approving review is required by reviewers with write access."
)

GREEN_CHECK = {
    "name": "Run PR CI Tests and Deployments",
    "status": "COMPLETED",
    "conclusion": "SUCCESS",
    "detailsUrl": "https://github.com/example/example/actions/runs/43298179797",
    "app": {"id": 15368},
    "startedAt": "2026-11-12T10:00:00Z",
    "completedAt": "2026-11-12T10:30:00Z",
}


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _step_shell() -> str:
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

    run_at = None
    for idx in range(start + 1, len(lines)):
        if lines[idx].strip() == "run: |":
            run_at = idx
            break
    assert run_at is not None, f"step {STEP_NAME!r} has no `run: |` block"

    body: list[str] = []
    indent = None
    for line in lines[run_at + 1 :]:
        if not line.strip():
            body.append("")
            continue
        current = len(line) - len(line.lstrip())
        if indent is None:
            indent = current
        if current < indent:
            break
        body.append(line[indent:])
    while body and not body[-1].strip():
        body.pop()
    assert body, "the step's `run:` block is empty"
    assert "{{" not in "\n".join(body), "unsubstituted expression in the step shell"
    return "\n".join(body)


GH_STUB = """#!/usr/bin/env python3
import json, os, sys

args = sys.argv[1:]
with open(os.environ["GH_LOG"], "a", encoding="utf-8") as handle:
    handle.write(" ".join(args) + "\\n")
with open(os.environ["GH_CONFIG"], encoding="utf-8") as handle:
    cfg = json.load(handle)

def _json_flag(argv):
    for idx, arg in enumerate(argv):
        if arg == "--json" and idx + 1 < len(argv):
            return argv[idx + 1]
    return ""

def _emit(text):
    sys.stdout.write(text if text.endswith("\\n") else text + "\\n")

if args and args[0] == "api":
    if len(args) > 1 and args[1] == "user":
        login = cfg.get("user_login") or ""
        if cfg.get("user_fail") or not login:
            sys.stderr.write("gh: HTTP 401: Bad credentials\\n")
            sys.exit(1)
        _emit(login)
        sys.exit(0)
    if len(args) > 1 and args[1].startswith("repos/"):
        # ci#1066 write-access probe: `gh api repos/<repo> --jq '.permissions.push'`
        if cfg.get("permissions_probe_fail"):
            sys.exit(1)
        value = cfg.get("permissions_push", "true")
        _emit("true" if value is True else "false" if value is False else str(value))
        sys.exit(0)
    sys.exit(0)

if len(args) > 1 and args[0] == "pr":
    verb = args[1]
    if verb == "view":
        if cfg.get("pr_view_fail"):
            sys.exit(1)
        wanted = _json_flag(args)
        pr = cfg["pr"]
        if wanted == "state":
            _emit(pr.get("state", "OPEN"))
        elif wanted == "labels":
            names = [name for name in pr.get("labels", [])]
            if "--jq" in args:
                # The workflow's own jq picks the deployment labels and joins
                # them; the stub answers that projection directly.
                _emit(", ".join(n for n in names if n in ("deploy", "sandbox-test")))
            else:
                _emit(json.dumps([{"name": name} for name in names]))
        elif wanted == "comments":
            bodies = [body for body in pr.get("comments", [])]
            if "--jq" in args:
                _emit(str(sum(1 for b in bodies if "Awaiting maintainer merge (ci#1066)" in b)))
            else:
                _emit(json.dumps([{"body": body} for body in bodies]))
        else:
            _emit(json.dumps(pr))
        sys.exit(0)
    if verb == "review":
        sys.exit(int(cfg.get("review_rc", 0)))
    if verb == "merge":
        admin = "--admin" in args
        rc = int(cfg.get("admin_merge_rc" if admin else "merge_rc", 0))
        out = cfg.get("admin_merge_output" if admin else "merge_output", "")
        if out:
            sys.stderr.write(out + "\\n")
        sys.exit(rc)
    if verb == "comment":
        sys.exit(int(cfg.get("comment_rc", 0)))

sys.exit(0)
"""

# `attempt_merge` backs off 10s per retry; the guard asserts there are *no*
# sleeps for a ruleset refusal, so the stub records instead of sleeping.
SLEEP_STUB = """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$GH_SLEEP_LOG"
exit 0
"""


def _pr(**overrides) -> dict:
    pr = {
        "state": "OPEN",
        "isDraft": False,
        "baseRefName": "main",
        "headRefName": "dev",
        "author": {"login": "cur1ousdude"},
        "mergeable": "MERGEABLE",
        "mergeStateStatus": "CLEAN",
        "reviewDecision": "REVIEW_REQUIRED",
        "reviews": [],
        "labels": [],
        "statusCheckRollup": [dict(GREEN_CHECK)],
    }
    pr.update(overrides)
    return pr


class ApproveRun:
    """One execution of the shipped `Approve and merge` step against stubs."""

    def __init__(
        self,
        *,
        has_approver_token: bool,
        user_login: str = "",
        user_fail: bool = True,
        prs: str = "36",
        **config,
    ):
        self.has_approver_token = has_approver_token
        self.user_login = user_login
        self.user_fail = user_fail
        self.prs = prs
        self.config = dict(config)

    def run(self) -> tuple[int, str, str, list[str], list[str]]:
        work = tempfile.mkdtemp(prefix="ci1067-approve-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)

        def write(name: str, content: str) -> str:
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)
            return path

        write("gh", GH_STUB)
        write("sleep", SLEEP_STUB)

        config = {"pr": _pr(), "user_login": self.user_login, "user_fail": self.user_fail}
        config.update(self.config)
        config_path = os.path.join(work, "gh_config.json")
        with open(config_path, "w", encoding="utf-8") as handle:
            json.dump(config, handle)

        gh_log = os.path.join(work, "gh.log")
        sleep_log = os.path.join(work, "sleep.log")
        summary_path = os.path.join(work, "summary.md")
        for path in (gh_log, sleep_log, summary_path):
            open(path, "w", encoding="utf-8").close()

        script_path = os.path.join(work, "approve.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write(_step_shell())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "GH_TOKEN": FAKE_TOKEN,
                "GH_LOG": gh_log,
                "GH_CONFIG": config_path,
                "GH_SLEEP_LOG": sleep_log,
                "HAS_APPROVER_TOKEN": "true" if self.has_approver_token else "false",
                "EVENT_NAME": "workflow_run",
                "PRS": self.prs,
                "RUN_URL": RUN_URL,
                "REPOSITORY": REPO,
                "GITHUB_STEP_SUMMARY": summary_path,
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )

        with open(gh_log, encoding="utf-8") as handle:
            calls = [line for line in handle.read().splitlines() if line]
        with open(sleep_log, encoding="utf-8") as handle:
            sleeps = [line for line in handle.read().splitlines() if line]
        with open(summary_path, encoding="utf-8") as handle:
            summary = handle.read()
        return result.returncode, result.stdout + result.stderr, summary, calls, sleeps


class AutoApproveHumanRequiredTest(unittest.TestCase):
    def test_the_step_shell_is_valid(self) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(_step_shell())
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_the_two_credential_outcomes_are_named(self) -> None:
        script = _step_shell()
        self.assertIn("ci#1067", script)
        self.assertIn('credential_state="token_rejected"', script)
        self.assertIn('credential_state="token_missing"', script)
        self.assertIn("approval_rule_violation()", script)
        self.assertIn("approving review|review is required by reviewers with write access", script)
        # A refusal must not be retried: the classification returns before the
        # backoff.
        self.assertRegex(script, r"approval_rule_violation \"\$output\"[\s\S]{0,400}?return 2")
        self.assertIn("awaiting a maintainer merge", script)

    def test_missing_token_reports_awaiting_human_and_exits_zero(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=False,
            user_fail=True,
            pr=_pr(reviewDecision="REVIEW_REQUIRED"),
            merge_rc=1,
            merge_output=RULE_REFUSAL,
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
            review_rc=1,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("token missing", summary)
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("PR #36", summary)
        self.assertIn(RUN_URL, summary)
        self.assertIn("::warning::No CONSOLIDATE_APPROVER_TOKEN configured", out)
        # The PR was actually inspected (unlike the rejected-token path).
        self.assertTrue(any(call.startswith("pr view") for call in calls), calls)
        self.assertNotIn(FAKE_TOKEN, out + summary)

    def test_rejected_token_exits_zero_before_the_pr_loop(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True, user_fail=True
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("token rejected", summary)
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("CONSOLIDATE_APPROVER_TOKEN", out)
        self.assertIn("::notice::", out)
        self.assertNotIn("::error::", out)
        # No PR-loop call at all: with a 401 each one would be misreported as an
        # unreadable PR.
        self.assertFalse(
            [call for call in calls if call.startswith("pr view")], calls
        )
        self.assertFalse([call for call in calls if call.startswith("pr merge")], calls)
        self.assertNotIn(FAKE_TOKEN, out + summary)

    def test_ruleset_refusal_is_not_retried(self) -> None:
        rc, out, summary, calls, sleeps = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            pr=_pr(author={"login": "cur1ousdude"}),
            merge_rc=1,
            merge_output=RULE_REFUSAL,
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
        ).run()
        self.assertEqual(rc, 0, out)
        merges = [call for call in calls if call.startswith("pr merge")]
        self.assertEqual(len(merges), 2, merges)  # one per path: plain, then --admin
        self.assertEqual(len([call for call in merges if "--admin" in call]), 1, merges)
        self.assertEqual(sleeps, [], "a ruleset refusal must never sleep/retry")
        self.assertIn("not retrying the merge", out)
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("PR #36", summary)
        self.assertNotIn(FAKE_TOKEN, out + summary)

    def test_self_authored_pr_is_reported_without_self_approval(self) -> None:
        rc, out, summary, calls, sleeps = ApproveRun(
            has_approver_token=True,
            user_login="cur1ousdude",
            user_fail=False,
            pr=_pr(author={"login": "cur1ousdude"}),
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertFalse(
            [call for call in calls if call.startswith("pr review")],
            "an author must never approve their own PR",
        )
        self.assertIn("self-authored", summary)
        self.assertEqual(sleeps, [])
        self.assertNotIn(FAKE_TOKEN, out + summary)

    def test_unreadable_pr_still_fails(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            pr_view_fail=True,
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("Could not read PR #36", out)
        self.assertFalse([call for call in calls if call.startswith("pr merge")], calls)
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_conflicting_pr_still_fails(self) -> None:
        rc, out, _, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            pr=_pr(mergeStateStatus="DIRTY", mergeable="CONFLICTING"),
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("conflicts", out)
        self.assertFalse([call for call in calls if call.startswith("pr merge")], calls)

    def test_failed_checks_are_never_merged_nor_called_green(self) -> None:
        red = dict(GREEN_CHECK, conclusion="FAILURE", detailsUrl=".../actions/runs/1")
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            pr=_pr(statusCheckRollup=[red]),
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("is not green", out)
        self.assertFalse([call for call in calls if call.startswith("pr merge")], calls)
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_an_unprivileged_approver_is_named_and_reported(self) -> None:
        """ci#1066: a resolvable identity without write access cannot approve."""
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            permissions_push=False,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("write access", out)
        self.assertIn("::notice::", out)
        self.assertNotIn("::error::", out)
        self.assertIn("approver lacks write access", summary)
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("PR #36", summary)
        # The write-access probe must actually be consulted, and no approval is
        # attempted for an identity the ruleset cannot accept.
        self.assertTrue(
            any(call.startswith("api repos/") for call in calls),
            calls,
        )
        self.assertFalse([call for call in calls if call.startswith("pr review")], calls)
        self.assertFalse([call for call in calls if call.startswith("pr merge")], calls)
        self.assertNotIn(FAKE_TOKEN, out + summary)

    def test_an_unverifiable_write_access_probe_keeps_going(self) -> None:
        """A probe failure must not be mistaken for 'no write access'."""
        rc, out, _, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            permissions_probe_fail=True,
            merge_rc=0,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("Could not verify whether", out)
        self.assertIn("write-access approver identity: true", out)
        self.assertTrue([call for call in calls if call.startswith("pr review")], calls)

    def test_the_promotion_hop_is_flagged_in_the_pr_exactly_once(self) -> None:
        """ci#1066: a green blocked dev->main hop states it in the PR, once."""
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=False,
            user_fail=True,
            pr=_pr(reviewDecision="REVIEW_REQUIRED"),
            merge_rc=1,
            merge_output=RULE_REFUSAL,
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
            review_rc=1,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("::notice::", out)
        self.assertIn("Awaiting maintainer merge (ci#1066)", out)
        comments = [call for call in calls if call.startswith("pr comment")]
        self.assertEqual(len(comments), 1, calls)
        self.assertIn("ci#1066", summary)

        # Second run: the note already exists, so it is not repeated.
        rc2, out2, _, calls2, _ = ApproveRun(
            has_approver_token=False,
            user_fail=True,
            pr=_pr(
                reviewDecision="REVIEW_REQUIRED",
                comments=["Awaiting maintainer merge (ci#1066): already here."],
            ),
            merge_rc=1,
            merge_output=RULE_REFUSAL,
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
            review_rc=1,
        ).run()
        self.assertEqual(rc2, 0, out2)
        self.assertIn("not repeating it", out2)
        self.assertEqual(
            [call for call in calls2 if call.startswith("pr comment")], [], calls2
        )

    def test_a_blocked_consolidation_pr_gets_no_promotion_note(self) -> None:
        """The waiting comment belongs to the dev->main hop, not to dev PRs."""
        rc, out, _, calls, _ = ApproveRun(
            has_approver_token=False,
            user_fail=True,
            pr=_pr(
                baseRefName="dev",
                headRefName="chore/consolidate-r85",
                reviewDecision="REVIEW_REQUIRED",
            ),
            merge_rc=1,
            merge_output=RULE_REFUSAL,
            admin_merge_rc=1,
            admin_merge_output=RULE_REFUSAL,
            review_rc=1,
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("awaiting a maintainer merge", out.lower())
        self.assertNotIn("Awaiting maintainer merge (ci#1066)", out)
        self.assertEqual([call for call in calls if call.startswith("pr comment")], [], calls)

    def test_a_failing_merge_for_another_reason_still_fails(self) -> None:
        rc, out, summary, _, sleeps = ApproveRun(
            has_approver_token=True,
            user_login="msgmate-reviewer",
            user_fail=False,
            merge_rc=1,
            merge_output="pull request is not mergeable: base branch was modified",
            admin_merge_rc=1,
            admin_merge_output="HTTP 500: Internal Server Error",
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("could not be auto-approved/merged", out)
        self.assertNotIn("awaiting a maintainer merge", summary)
        # Genuine transient failures keep their bounded backoff.
        self.assertTrue(sleeps)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
