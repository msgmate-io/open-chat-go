#!/usr/bin/env python3
"""Regression guard for ci#1066 - "human required" must not look like a crash.

`.github/workflows/auto-approve-consolidation.yaml` -> `Approve and merge`
used to end red whenever the base-branch ruleset demanded an approving review
with write access that the automation could not supply. The promotion PR was
fully green, only a maintainer approval was missing, yet the run reported

    GraphQL: Repository rule violations found / At least 1 approving review
    is required by reviewers with write access
    Could not merge PR #36 (no usable approval and no ruleset bypass ...)

three times (with 10s backoff between attempts) and exited 1. A red
`auto-approve` run therefore no longer meant "something is broken", which is
the property this file defends.

The ci#1066 behaviour asserted here:

* `token_missing`      - no `CONSOLIDATE_APPROVER_TOKEN` and an unresolvable
                         fallback identity: warn, keep going, and if the merge
                         is then refused by the review rule, report the PR as
                         awaiting a maintainer merge and stay GREEN.
* `token_rejected`     - a token *is* configured but the API rejects it: name
                         that precisely in the job summary, stop before the PR
                         loop turns every read into a bogus error, stay GREEN.
* review-rule refusal  - the refusal is classified and NOT retried (no 10s
                         backoff loop, no duplicate GraphQL blobs).
* real regressions     - an unreadable PR or a PR with conflicts is still a
                         red run, so green keeps its meaning.

Like the sibling guards this file extracts the step's shell verbatim from the
workflow and runs it against a stubbed `gh`, so the assertions are about the
shipped shell and not a copy. Deliberately stdlib-only.
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

# The step's `run:` body is indented ten spaces inside the YAML file.
RUN_INDENT = 10

# The refusal text a ruleset with "require an approving review by a writer"
# actually produces; the classifier keys off this wording.
REVIEW_RULE_REFUSAL = (
    "GraphQL: Repository rule violations found\n"
    "At least 1 approving review is required by reviewers with write access. "
    "(mergePullRequest)\n"
)

GREEN_CHECKS = [
    {
        "name": "Run PR CI Tests and Deployments",
        "status": "COMPLETED",
        "conclusion": "SUCCESS",
        "app": {"id": 15368},
        "detailsUrl": "https://github.com/example/example/actions/runs/43298179797",
        "completedAt": "2026-11-12T17:20:00Z",
        "startedAt": "2026-11-12T17:02:00Z",
    }
]


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


def _promotion_pr(number: int = 36, **overrides) -> dict:
    """A green `dev` -> `main` promotion PR as `gh pr view --json ...` reports it."""
    payload = {
        "state": "OPEN",
        "isDraft": False,
        "baseRefName": "main",
        "headRefName": "dev",
        "author": {"login": "someone-else"},
        "mergeable": "MERGEABLE",
        "mergeStateStatus": "BLOCKED",
        "reviewDecision": "REVIEW_REQUIRED",
        "reviews": [],
        "statusCheckRollup": list(GREEN_CHECKS),
        "labels": [{"name": "test"}],
    }
    payload.update(overrides)
    return {"number": number, "payload": payload}


GH_STUB = """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$GH_LOG"
case "${1:-}" in
  api)
    if [ "${2:-}" = "user" ]; then
      if [ "${GH_USER_LOGIN:-}" = "__fail__" ]; then
        exit 1
      fi
      printf '%s' "${GH_USER_LOGIN:-}"
      exit 0
    fi
    # `api --method PUT .../update-branch` and other mutations are no-ops.
    exit 0
    ;;
  pr)
    case "${2:-}" in
      view)
        file="$GH_PR_VIEW_DIR/${3}.json"
        [ -f "$file" ] || exit 1
        expr=""
        shift 3
        while [ $# -gt 0 ]; do
          if [ "$1" = "--jq" ]; then
            expr="$2"
            shift 2
          else
            shift
          fi
        done
        if [ -n "$expr" ]; then
          jq -r "$expr" < "$file"
        else
          cat "$file"
        fi
        exit 0
        ;;
      review)
        exit "${GH_REVIEW_RC:-0}"
        ;;
      merge)
        case "${GH_MERGE_MODE:-ok}" in
          ok)
            exit 0
            ;;
          review_rule)
            printf '%s' "$GH_REVIEW_RULE_REFUSAL" >&2
            exit 1
            ;;
          *)
            echo "merge failed" >&2
            exit 1
            ;;
        esac
        ;;
      comment)
        exit 0
        ;;
    esac
    ;;
esac
exit 0
"""

SLEEP_STUB = """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$SLEEP_LOG"
exit 0
"""


class ApproveRun:
    """One execution of the shipped step against a stubbed `gh`."""

    def __init__(
        self,
        prs: str = "36",
        has_approver_token: bool = False,
        user_login: str | None = "cur1ousdude",
        prs_payload: list[dict] | None = None,
        review_rc: int = 0,
        merge_mode: str = "ok",
        event_name: str = "workflow_run",
    ):
        self.prs = prs
        self.has_approver_token = has_approver_token
        self.user_login = user_login
        self.prs_payload = prs_payload if prs_payload is not None else [_promotion_pr()]
        self.review_rc = review_rc
        self.merge_mode = merge_mode
        self.event_name = event_name

    def run(self) -> tuple[int, str, str, list[str], list[str]]:
        work = tempfile.mkdtemp(prefix="ci1066-approve-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)
        view_dir = os.path.join(work, "prs")
        os.makedirs(view_dir)

        def write(name: str, content: str) -> str:
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)
            return path

        write("gh", GH_STUB)
        write("sleep", SLEEP_STUB)

        for entry in self.prs_payload:
            with open(
                os.path.join(view_dir, f"{entry['number']}.json"), "w", encoding="utf-8"
            ) as handle:
                json.dump(entry["payload"], handle)

        gh_log = os.path.join(work, "gh.log")
        sleep_log = os.path.join(work, "sleep.log")
        summary_path = os.path.join(work, "summary.md")
        for path in (gh_log, sleep_log, summary_path):
            with open(path, "w", encoding="utf-8"):
                pass

        script_path = os.path.join(work, "approve.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write("#!/usr/bin/env bash\n" + _step_script())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "GH_LOG": gh_log,
                "GH_PR_VIEW_DIR": view_dir,
                "GH_REVIEW_RC": str(self.review_rc),
                "GH_MERGE_MODE": self.merge_mode,
                "GH_REVIEW_RULE_REFUSAL": REVIEW_RULE_REFUSAL,
                "GH_USER_LOGIN": "__fail__" if self.user_login is None else self.user_login,
                "SLEEP_LOG": sleep_log,
                "GITHUB_STEP_SUMMARY": summary_path,
                "GH_TOKEN": "stub-token",
                "PRS": self.prs,
                "EVENT_NAME": self.event_name,
                "HAS_APPROVER_TOKEN": "true" if self.has_approver_token else "false",
                "RUN_URL": RUN_URL,
                "REPOSITORY": REPO,
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
        return result.returncode, result.stdout, summary, calls, sleeps


def _merge_calls(calls: list[str]) -> list[str]:
    return [call for call in calls if call.startswith("pr merge")]


class AutoApproveHumanRequiredTest(unittest.TestCase):
    def test_the_step_shell_is_valid(self) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(_step_script())
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_the_classification_is_documented(self) -> None:
        script = _step_script()
        self.assertIn("ci#1066", script)
        # Both credential outcomes are named, not merged into one warning.
        self.assertIn("token_rejected", script)
        self.assertIn("token_missing", script)
        # The review-rule refusal is recognised as non-retryable.
        self.assertIn("approval_rule_violation()", script)
        self.assertIn("review is required by reviewers with write access", script)
        # ... and it is reported as human work rather than as a failure.
        self.assertIn("awaiting a maintainer merge", script)

    def test_no_approver_token_stays_green_and_names_a_maintainer_merge(self) -> None:
        rc, out, summary, calls, sleeps = ApproveRun(
            has_approver_token=False,
            user_login=None,
            review_rc=1,
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertNotIn("::error::", out)
        # The operator is told what the automation could not do.
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("#36", summary)
        self.assertIn("CONSOLIDATE_APPROVER_TOKEN", summary)
        self.assertIn("maintainer merge", out)
        # No retry storm: the refusal is classified, not retried.
        self.assertEqual(sleeps, [], "a ruleset refusal must not be retried with backoff")
        self.assertEqual(len(_merge_calls(calls)), 1, calls)

    def test_a_rejected_token_is_named_and_the_run_stays_green(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login=None,
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertNotIn("::error::", out)
        self.assertIn("CONSOLIDATE_APPROVER_TOKEN", summary)
        self.assertIn("rejected", summary.lower())
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("#36", summary)
        # The step must not limp through the PR loop with unusable credentials:
        # reading the PR would fail and be misreported as an unreadable PR.
        self.assertEqual(_merge_calls(calls), [])
        self.assertFalse(
            any(call.startswith("pr view") for call in calls),
            f"token_rejected must stop before the PR loop: {calls}",
        )

    def test_posted_approval_that_the_ruleset_still_rejects_is_human_work(self) -> None:
        rc, out, summary, calls, sleeps = ApproveRun(
            has_approver_token=True,
            user_login="cur1ousdude",
            review_rc=0,
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertNotIn("::error::", out)
        self.assertIn("awaiting a maintainer merge", summary)
        self.assertIn("still reports it as missing", summary)
        self.assertEqual(sleeps, [], "a ruleset refusal must not be retried with backoff")
        # One plain attempt plus one bypass attempt - never a third, never a loop.
        self.assertEqual(len(_merge_calls(calls)), 2, calls)

    def test_a_self_authored_pr_is_reported_not_approved(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            user_login="cur1ousdude",
            prs_payload=[_promotion_pr(author={"login": "cur1ousdude"})],
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("cannot self-approve", summary)
        self.assertEqual([c for c in calls if c.startswith("pr review")], [])

    def test_an_unreadable_pr_is_still_a_red_run(self) -> None:
        rc, out, summary, _, _ = ApproveRun(
            has_approver_token=True,
            prs_payload=[],
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("::error::Could not read PR #36", out)
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_a_conflicting_pr_is_still_a_red_run(self) -> None:
        rc, out, summary, _, _ = ApproveRun(
            has_approver_token=True,
            prs_payload=[_promotion_pr(mergeStateStatus="DIRTY")],
            merge_mode="review_rule",
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("::error::PR #36 has conflicts", out)
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_a_failing_check_is_never_merged_and_never_called_human_work(self) -> None:
        red = dict(GREEN_CHECKS[0], conclusion="FAILURE")
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            prs_payload=[_promotion_pr(statusCheckRollup=[red])],
            merge_mode="ok",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("is not green", out)
        self.assertEqual(_merge_calls(calls), [])
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_a_successful_merge_is_reported_once(self) -> None:
        rc, out, summary, calls, _ = ApproveRun(
            has_approver_token=True,
            merge_mode="ok",
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("Merged PR #36", out)
        self.assertEqual(len([c for c in calls if c.startswith("pr comment")]), 1, calls)
        self.assertNotIn("awaiting a maintainer merge", summary)

    def test_the_summary_never_claims_a_token_value(self) -> None:
        _, out, summary, _, _ = ApproveRun(
            has_approver_token=True, user_login=None, merge_mode="review_rule"
        ).run()
        for text in (out, summary):
            self.assertNotIn("stub-token", text, "the step must never echo the token")


if __name__ == "__main__":  # pragma: no cover
    unittest.main()