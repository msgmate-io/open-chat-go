#!/usr/bin/env python3
"""Regression guard for ci#847 - stale check-run instances blocking a green PR.

`.github/workflows/auto-approve-consolidation.yaml` -> `Approve and merge`
decides from the *raw* `statusCheckRollup` of the PR. GitHub keeps a
newest-per-context semantics for branch protection: when a run is superseded (a
re-run, or a push that cancels the in-flight run) the superseded instance is
still reported in the rollup - as `CANCELLED` - next to the green replacement.
Reading the raw rollup therefore made this gate veto a fully green consolidation
PR *forever*: PR #1057's head `3a5d4291` had its required context
`Run PR CI Tests and Deployments` published as CANCELLED by the superseded run
while the replacement was SUCCESS, so the auto-approve gate refused to merge and
the PR sat `BLOCKED / REVIEW_REQUIRED` until its head moved.

The fix is the `JQ_NEWEST_CHECKS` jq definition shipped in that step: it collapses
the rollup onto the newest instance per `(context, app)` - ordered by the later of
`completed_at` / `started_at` (a skipped run can complete *before* it starts),
then by the workflow-run id parsed out of `details_url` - before either gate
(`failing_or_pending`, `required_ci_green`) looks at it. The Go required-check
resolver does the same in `backend/nativecoder/requiredchecks.go`.

This file extracts that jq definition *verbatim* from the workflow and runs the
two gate programs against fixtures shaped after the real rollup, plus a verbatim
excerpt of PR #1057's required contexts, so the assertions are about the shipped
program and not a copy. Deliberately stdlib-only: `Pin Freshness Gate` runs it
with the system `python3`.
"""

from __future__ import annotations

import json
import pathlib
import re
import subprocess
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "auto-approve-consolidation.yaml"

STEP_NAME = "Approve and merge"

REQUIRED_CONTEXT = "Run PR CI Tests and Deployments"
CHECK_APP_ID = 15368

# The jq definition the step defines as a shell variable, verbatim.
DEF_RE = re.compile(r"JQ_NEWEST_CHECKS='\n(.*?)'\n", re.S)

FAILING_TAIL = """
[ newest[]
  | select(
      ((.status // "") | test("^(QUEUED|IN_PROGRESS|PENDING|WAITING|REQUESTED)$"))
      or ((.conclusion // "") | test("^(FAILURE|CANCELLED|TIMED_OUT|ACTION_REQUIRED|STARTUP_FAILURE|STALE)$"))
      or ((.state // "") | test("^(FAILURE|ERROR|PENDING)$"))
    )
  | (.name // .context // "unknown") ] | join(", ")
"""

REQUIRED_TAIL = """
[ newest[]
  | select((.name // .context // "") == "%s")
  | select((.conclusion // .state // "") == "SUCCESS") ] | length
""" % REQUIRED_CONTEXT


def _definition() -> str:
    """Extract the shipped `JQ_NEWEST_CHECKS` definition, verbatim."""
    text = WORKFLOW.read_text(encoding="utf-8")
    if STEP_NAME not in text:
        raise AssertionError(f"step {STEP_NAME!r} is gone from {WORKFLOW.name}")
    match = DEF_RE.search(text)
    if match is None:
        raise AssertionError(
            "JQ_NEWEST_CHECKS is gone from the merge step: the stale-instance "
            "normalization (ci#847) is no longer applied"
        )
    return match.group(1)


def _run(program: str, rollup: object) -> str:
    """Run a gate program (shipped definition + gate tail) over a rollup."""
    with tempfile.NamedTemporaryFile("w", suffix=".jq", delete=False) as handle:
        handle.write(program)
        path = pathlib.Path(handle.name)
    try:
        done = subprocess.run(
            ["jq", "-r", "-f", str(path), "-"],
            input=json.dumps(rollup),
            capture_output=True,
            text=True,
            check=False,
        )
    finally:
        path.unlink(missing_ok=True)
    if done.returncode != 0:
        raise AssertionError(f"jq failed: {done.stderr.strip()}")
    return done.stdout.strip()


def _check(
    name: str,
    conclusion: str,
    *,
    started: str,
    completed: str | None = None,
    run_id: int = 1,
    app: int = CHECK_APP_ID,
) -> dict:
    entry: dict = {
        "__typename": "CheckRun",
        "name": name,
        "status": "COMPLETED",
        "conclusion": conclusion,
        "startedAt": started,
        "detailsUrl": f"https://github.com/o/n/actions/runs/{run_id}/job/9",
        "app": {"id": app},
    }
    if completed is not None:
        entry["completedAt"] = completed
    return entry


def _rollup(*entries: dict) -> dict:
    return {"statusCheckRollup": list(entries)}


# Verbatim excerpt of PR #1057's rollup for the required context: the superseded
# run finished *before* its green replacement started.
SUPERSEDED = _check(
    REQUIRED_CONTEXT,
    "CANCELLED",
    started="2026-10-23T03:00:14Z",
    completed="2026-10-23T03:00:34Z",
    run_id=39892624806,
)
REPLACEMENT = _check(
    REQUIRED_CONTEXT,
    "SUCCESS",
    started="2026-10-23T03:03:29Z",
    completed="2026-10-23T03:03:49Z",
    run_id=39892668623,
)


class StaleCheckInstanceGuard(unittest.TestCase):
    def setUp(self) -> None:
        definition = _definition()
        self.failing = definition + FAILING_TAIL
        self.required = definition + REQUIRED_TAIL

    def test_superseded_instance_does_not_veto_its_green_replacement(self) -> None:
        """The ci#847 case: CANCELLED (superseded) + SUCCESS (newest)."""
        rollup = _rollup(
            _check("CodeQL", "SUCCESS", started="2026-10-23T03:03:26Z", run_id=2),
            SUPERSEDED,
            REPLACEMENT,
        )
        self.assertEqual(_run(self.failing, rollup), "")
        self.assertEqual(_run(self.required, rollup), "1")

    def test_legacy_status_contexts_are_still_evaluated(self) -> None:
        """A legacy commit-status context (`context`/`state`) is not collapsed
        into a same-named check-run, and a red one still blocks."""
        red_status = {
            "__typename": "StatusContext",
            "context": "ci/legacy-status",
            "state": "FAILURE",
        }
        rollup = _rollup(SUPERSEDED, REPLACEMENT, red_status)
        self.assertEqual(_run(self.failing, rollup), "ci/legacy-status")
        self.assertEqual(_run(self.required, rollup), "1")

    def test_lowercase_enumeration_never_goes_green(self) -> None:
        """Guard against a silent false green: an unrecognized casing is simply
        not matched, so the required context is not reported green."""
        lowercase = {"statusCheckRollup": [dict(SUPERSEDED, conclusion="cancelled")]}
        self.assertEqual(_run(self.required, lowercase), "0")

    def test_newest_instance_wins_even_when_it_is_red(self) -> None:
        """Safety: only a superseded instance is dropped, never a real failure."""
        rollup = _rollup(
            _check("Full Suite Gate", "SUCCESS", started="2026-10-23T03:00:00Z", run_id=1),
            _check("Full Suite Gate", "CANCELLED", started="2026-10-23T04:00:00Z", run_id=2),
        )
        self.assertEqual(_run(self.failing, rollup), "Full Suite Gate")

    def test_pending_replacement_still_blocks(self) -> None:
        """A superseded CANCELLED plus a *pending* replacement must not merge."""
        pending = _check(REQUIRED_CONTEXT, "STALE", started="2026-10-23T05:00:00Z", run_id=3)
        pending["status"] = "IN_PROGRESS"
        pending.pop("conclusion")
        rollup = _rollup(SUPERSEDED, REPLACEMENT, pending)
        self.assertEqual(_run(self.failing, rollup), REQUIRED_CONTEXT)
        self.assertEqual(_run(self.required, rollup), "0")

    def test_complete_before_start_and_id_tiebreak(self) -> None:
        """A skipped run completes before it starts; ids break a timestamp tie."""
        skipped = _check(
            "Update PR Comment After Release",
            "SKIPPED",
            started="2026-10-23T03:00:12Z",
            completed="2026-10-23T03:00:11Z",
            run_id=1,
        )
        replacement = _check(
            "Update PR Comment After Release",
            "SUCCESS",
            started="2026-10-23T03:03:26Z",
            completed="2026-10-23T03:03:26Z",
            run_id=2,
        )
        self.assertEqual(_run(self.failing, _rollup(skipped, replacement)), "")

        stamp = "2026-10-23T03:00:00Z"
        older = _check("CodeQL", "FAILURE", started=stamp, completed=stamp, run_id=100)
        newer = _check("CodeQL", "SUCCESS", started=stamp, completed=stamp, run_id=200)
        self.assertEqual(_run(self.failing, _rollup(older, newer)), "")

    def test_distinct_apps_of_one_name_stay_separate(self) -> None:
        """A same-named context from another app is not collapsed into ours."""
        rollup = _rollup(
            _check(REQUIRED_CONTEXT, "SUCCESS", started="2026-10-23T03:03:29Z", run_id=2),
            _check(
                REQUIRED_CONTEXT,
                "FAILURE",
                started="2026-10-23T03:03:29Z",
                run_id=2,
                app=57789,
            ),
        )
        self.assertEqual(_run(self.failing, rollup), REQUIRED_CONTEXT)

    def test_empty_rollup_is_untouched(self) -> None:
        self.assertEqual(_run(self.failing, _rollup()), "")
        self.assertEqual(_run(self.required, _rollup()), "0")


if __name__ == "__main__":  # pragma: no cover
    unittest.main(verbosity=2)
