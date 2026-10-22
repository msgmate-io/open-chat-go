#!/usr/bin/env python3
"""Regression guard for ci#832 (stale run status + shifted run-line fields).

`.github/workflows/run-pr-ci-tests-and-deployments.yaml` contains three deploy
waiters (the approved-dispatch waiter plus the two merged-deploy siblings). Both
ci#832 defects lived in code they share:

1. **Shifted run-line fields.** Each waiter emitted its run as a tab-separated
   line and re-read it with `IFS=$'\\t' read -r ...`. Tab is an IFS *whitespace*
   character, so `read` collapses runs of tabs and an empty field shifts every
   later field one position left. The run's conclusion is empty until the run
   finishes, so from the first poll onward the waiter compared the run *url*
   against `completed`, logged the run *ref* as the conclusion and silently
   disabled the dispatched-ref/sha guards. Fixed by emitting the conclusion
   last and reading the fields in the emitted order.

2. **Stale top-level run status.** GitHub can leave a run's top-level `status`
   at `in_progress` long after every job of that run finished (observed on a
   real deploy: the run's single job went green 30+ minutes before the run
   record settled). Polling only the top-level status burned the whole
   `CI_WAIT_MAX_SECONDS` budget and dyed the job red although the deploy had
   succeeded. Fixed by consulting the run's job listing: when the top-level
   status is stale but every job is terminal, exit 0 if none failed, and fail
   fast with the job count when any did.

Deliberately stdlib-only: `Pin Freshness Gate` runs this file with the system
`python3` before the build-tools venv is guaranteed. The guard blocks are
extracted from the workflow verbatim and executed with a stubbed
`github_api_get`, so the assertions are about the shipped shell, not a copy.
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

GUARD_HEADER = 'if [ "$run_status" != "completed" ]; then'
JOBS_URL_FRAGMENT = '/actions/runs/${run_id}/jobs?per_page=100'

# Exit code the harness uses to mean "the guard fell through and kept polling"
# (it ran `continue` instead of exiting 0/1).
KEPT_POLLING = 7

# jq expression (as written in the emitted run line) -> the shell variable the
# waiter must read it back into, at the same position.
FIELD_TO_VAR = {
    ".id": "run_id",
    ".status": "run_status",
    ".html_url": "run_url",
    ".head_branch": "run_ref",
    ".head_sha": "run_sha",
    ".display_title": "run_title",
    ".name": "run_title",
    ".conclusion": "run_conclusion",
}

FIXTURES = {
    "all jobs terminal, none failed -> success": (
        '{"total_count": 1, "jobs": [{"status": "completed", "conclusion": "success"}]}',
        0,
    ),
    "jobs terminal, one cancelled, none failed -> success": (
        '{"total_count": 2, "jobs": ['
        '{"status": "completed", "conclusion": "cancelled"},'
        '{"status": "completed", "conclusion": "skipped"}]}',
        0,
    ),
    "jobs terminal, one failure -> fail fast": (
        '{"total_count": 2, "jobs": ['
        '{"status": "completed", "conclusion": "failure"},'
        '{"status": "completed", "conclusion": "success"}]}',
        1,
    ),
    "jobs terminal, one timed_out -> fail fast": (
        '{"total_count": 1, "jobs": [{"status": "completed", "conclusion": "timed_out"}]}',
        1,
    ),
    "jobs terminal, one startup_failure -> fail fast": (
        '{"total_count": 1, "jobs": [{"status": "completed", "conclusion": "startup_failure"}]}',
        1,
    ),
    "a job is still running -> keep polling": (
        '{"total_count": 2, "jobs": ['
        '{"status": "in_progress", "conclusion": null},'
        '{"status": "completed", "conclusion": "success"}]}',
        KEPT_POLLING,
    ),
    "a job is queued -> keep polling": (
        '{"total_count": 1, "jobs": [{"status": "queued", "conclusion": null}]}',
        KEPT_POLLING,
    ),
    "unreadable empty payload -> keep polling": ("", KEPT_POLLING),
    "unreadable non-json payload -> keep polling": ("<html>502</html>", KEPT_POLLING),
    "payload without total_count -> keep polling": ('{"jobs": []}', KEPT_POLLING),
    "empty object -> keep polling": ("{}", KEPT_POLLING),
    "job listing with a null entry -> keep polling": (
        '{"total_count": 2, "jobs": [null, {"status": "completed", "conclusion": "success"}]}',
        KEPT_POLLING,
    ),
}


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _guard_blocks() -> list[tuple[int, str]]:
    """Extract every ci#832 job-status guard block, verbatim.

    A block starts at the `if [ "$run_status" != "completed" ]; then` header
    that introduces a `jobs_json="$(github_api_get .../jobs...)` read and ends at
    the `fi` closing the inner failed-job branch (the line right after
    `exit 1`). Everything in between is the shipped shell.
    """
    lines = _workflow_text().splitlines()
    blocks: list[tuple[int, str]] = []
    for idx, line in enumerate(lines):
        if JOBS_URL_FRAGMENT not in line or "jobs_json=" not in line:
            continue
        start = None
        for back in range(idx, -1, -1):
            if lines[back].strip() == GUARD_HEADER:
                start = back
                break
        if start is None:
            raise AssertionError(
                "ci#832 guard at line %d is not preceded by a run-status check" % (idx + 1)
            )
        end = None
        seen_exit = False
        for fwd in range(idx, len(lines)):
            stripped = lines[fwd].strip()
            if stripped == "exit 1":
                seen_exit = True
                continue
            if seen_exit and stripped == "fi":
                end = fwd
                break
        if end is None:
            raise AssertionError("ci#832 guard at line %d has no closing fi" % (idx + 1))
        blocks.append((start + 1, "\n".join(lines[start : end + 1])))
    return blocks


def _field_key(field: str) -> str:
    """Normalise a jq field expression to its primary key (drop `// fallback`)."""
    key = field.split("//")[0].strip().strip("()").strip()
    return key


def _run_line_emissions() -> list[tuple[int, list[str]]]:
    """Every `[ ... ] | @tsv` run-line emission, as ordered jq field expressions.

    The approved-dispatch waiter emits its line on one line; the two
    merged-deploy siblings wrap the `| @tsv` onto the next line, so the match
    tolerates whitespace (including newlines) between the field list and the
    `@tsv` filter.
    """
    text = _workflow_text()
    emissions: list[tuple[int, list[str]]] = []
    pattern = re.compile(r"\[(?P<fields>[^\[\]]*)\]\s*\|\s*@tsv")
    for match in pattern.finditer(text):
        fields = [f.strip() for f in match.group("fields").split(",") if f.strip()]
        line_no = text[: match.start()].count("\n") + 1
        if any(_field_key(f) in FIELD_TO_VAR for f in fields):
            emissions.append((line_no, fields))
    return emissions


def _run_line_reads() -> list[tuple[int, list[str]]]:
    """Every tab-IFS run-line read statement, as its ordered variable list."""
    reads: list[tuple[int, list[str]]] = []
    pattern = re.compile(r"IFS=\$\'\\t\'\s+read\s+-r\s+(?P<vars>[A-Za-z0-9_ ]+)<<<")
    for line_no, line in enumerate(_workflow_text().splitlines(), start=1):
        match = pattern.search(line)
        if match:
            reads.append((line_no, match.group("vars").split()))
    return reads


def _mapped_fields(fields: list[str]) -> list[str]:
    mapped = []
    for field in fields:
        key = _field_key(field)
        if key not in FIELD_TO_VAR:
            raise AssertionError("unmapped run-line field %r" % field)
        mapped.append(FIELD_TO_VAR[key])
    return mapped


class DeployWaiterRunStatusTest(unittest.TestCase):
    """Pin the ci#832 guard behaviour and the run-line field ordering."""

    def test_all_three_waiters_carry_the_guard(self) -> None:
        blocks = _guard_blocks()
        self.assertEqual(
            len(blocks),
            3,
            "expected the approved-dispatch waiter plus both merged-deploy "
            "siblings to consult the job listing; found %d" % len(blocks),
        )
        first = blocks[0][1]
        for line_no, block in blocks[1:]:
            self.assertEqual(
                block,
                first,
                "the ci#832 guard at line %d drifted from the first copy" % line_no,
            )

    def test_guard_documents_both_failure_modes(self) -> None:
        for line_no, block in _guard_blocks():
            self.assertIn("ci#832", block, "guard at line %d is undocumented" % line_no)
            self.assertIn("jobs_pending", block)
            self.assertIn("jobs_failed", block)
            self.assertIn("jobs_cancelled", block)
            self.assertIn("jobs_total", block)

    def test_guard_reads_the_run_job_listing(self) -> None:
        for line_no, block in _guard_blocks():
            self.assertIn(JOBS_URL_FRAGMENT, block, "guard at line %d" % line_no)
            self.assertIn("github_api_get", block, "guard at line %d" % line_no)

    def test_no_waiter_reads_the_conclusion_in_a_non_last_position(self) -> None:
        reads = _run_line_reads()
        self.assertGreaterEqual(len(reads), 3)
        for line_no, variables in reads:
            self.assertEqual(
                variables[-1],
                "run_conclusion",
                "line %d reads the run line with the conclusion not last; a tab "
                "IFS collapses the empty conclusion and shifts every later field "
                "(ci#832)" % line_no,
            )
            self.assertEqual(
                len(set(variables)),
                len(variables),
                "line %d reads a duplicated field" % line_no,
            )
        for line_no, variables in reads:
            self.assertNotEqual(
                variables[:4],
                ["run_id", "run_status", "run_conclusion", "run_url"],
                "line %d still uses the pre-ci#832 shifted order" % line_no,
            )

    def test_emitted_run_lines_put_the_conclusion_last(self) -> None:
        emissions = _run_line_emissions()
        self.assertGreaterEqual(len(emissions), 3)
        for line_no, fields in emissions:
            keys = [_field_key(field) for field in fields]
            self.assertEqual(
                keys[-1],
                ".conclusion",
                "line %d emits the conclusion before the end of the run line; an "
                "empty conclusion would shift the trailing fields (ci#832)" % line_no,
            )
            # The ci#832 job-status guard fetches `/actions/runs/${run_id}/jobs`,
            # so every waiter that emits a run line must also emit (and read
            # back) the run id. The merged-deploy siblings omitted `.id` while
            # still using `run_id`, which aborted under `set -u` as soon as a
            # queued run matched (ci#839).
            self.assertIn(
                ".id",
                keys,
                "line %d emits a run line without .id, but its guard needs "
                "run_id" % line_no,
            )
        # The PR-deploy waiter also carries the dispatched ref/sha, which the
        # shift used to silently disable: those fields must stay before the
        # conclusion.
        pr_line_reads = [
            variables for _, variables in _run_line_reads() if "run_sha" in variables
        ]
        self.assertEqual(len(pr_line_reads), 1, "expected one dispatched-ref waiter")
        self.assertLess(
            pr_line_reads[0].index("run_sha"),
            pr_line_reads[0].index("run_conclusion"),
            "the dispatched sha must be read before the (initially empty) conclusion",
        )

    def test_emission_order_matches_read_order(self) -> None:
        """Each waiter must read its fields in exactly the order it emits them."""
        emissions = _run_line_emissions()
        reads = _run_line_reads()
        self.assertEqual(
            len(emissions),
            len(reads),
            "run-line emissions (%d) and reads (%d) are not paired"
            % (len(emissions), len(reads)),
        )
        for (emit_line, fields), (read_line, variables) in zip(emissions, reads):
            expected = _mapped_fields(fields)
            self.assertEqual(
                variables,
                expected,
                "read at line %d (%s) does not match the emission at line %d (%s)"
                % (read_line, variables, emit_line, expected),
            )

    def test_empty_conclusion_cannot_shift_the_fields(self) -> None:
        """Behavioural round-trip through the workflow's own emit/read order.

        Builds the tab-separated line the waiter would emit for an in-progress
        run (empty conclusion) and re-reads it with the workflow's own read
        statement. Before ci#832 the url was read as the conclusion and the ref
        as the sha; the assertions below fail on that regression.
        """
        emissions = _run_line_emissions()
        reads = _run_line_reads()
        values = {
            "run_id": "39823960049",
            "run_status": "in_progress",
            "run_url": "https://github.com/example/actions/runs/1",
            "run_ref": "main",
            "run_sha": "dfb8879deadbeef",
            "run_title": "Deploy msgmate-io-ci",
            "run_conclusion": "",
        }
        for (_, fields), (read_line, variables) in zip(emissions, reads):
            ordered = [_mapped_fields([f])[0] for f in fields]
            line = "\t".join(values[name] for name in ordered)
            script = (
                "set -u\n"
                "IFS=$'\\t' read -r %s <<< \"%s\"\n"
                "for name in %s; do\n"
                "  printf '%%s=%%s\\n' \"$name\" \"${!name}\"\n"
                "done\n"
            ) % (" ".join(variables), line, " ".join(variables))
            with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
                handle.write(script)
                script_path = handle.name
            try:
                result = subprocess.run(
                    ["bash", script_path], capture_output=True, text=True, check=False
                )
            finally:
                pathlib.Path(script_path).unlink(missing_ok=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            got = dict(
                line.split("=", 1) for line in result.stdout.splitlines() if "=" in line
            )
            for name, expected in values.items():
                if name not in variables:
                    continue
                self.assertEqual(
                    got.get(name),
                    expected,
                    "run-line read at line %d mapped %s=%r (expected %r); the "
                    "empty conclusion shifted the fields (ci#832)"
                    % (read_line, name, got.get(name), expected),
                )

    def test_jobs_json_default_is_not_brace_corrupted(self) -> None:
        """Pin the default expansion `${jobs_json:-"{}"}`, not `${jobs_json:-{}}`.

        Bash closes `${...:-{}}` early: for a *set* `jobs_json` the expansion
        yields the payload plus a stray `}` (``{"total_count":1}}``), which is
        invalid JSON, so jq exits 5 and every `|| echo <n>` fallback fires. The
        guard then saw `jobs_total=1\\n0`, `[: 1\\n0: integer expression
        expected` and silently fell through to polling - i.e. the ci#832 fix was
        inert. The escaped/nested-quoted default is the correct form.
        """
        text = _workflow_text()
        self.assertNotIn(
            "${jobs_json:-{}}",
            text,
            "a bare `${jobs_json:-{}}` default is brace-corrupted by bash; use "
            '`${jobs_json:-"{}"}` (ci#832)',
        )
        self.assertIn('${jobs_json:-"{}"}', text)
        for line_no, block in _guard_blocks():
            self.assertIn(
                '${jobs_json:-"{}"}',
                block,
                "guard at line %d left the corrupted default in place" % line_no,
            )

    def test_guard_treats_a_stale_top_level_status_as_resolved(self) -> None:
        """The guard's reason for existing: only settle when the jobs agree."""
        for line_no, block in _guard_blocks():
            self.assertIn(
                'if [ "${jobs_total:-0}" -gt 0 ] && [ "${jobs_pending:-1}" -eq 0 ]; then',
                block,
                "guard at line %d must require a non-empty, fully terminal job "
                "listing before settling" % line_no,
            )

    def test_guard_outcome_per_job_listing(self) -> None:
        for fixture, (payload, expected) in FIXTURES.items():
            with self.subTest(fixture=fixture):
                code, output = _run_all_guards(payload)
                self.assertEqual(
                    code,
                    expected,
                    "fixture %r exited %d (expected %d)\n%s"
                    % (fixture, code, expected, output),
                )
                if expected == 0:
                    self.assertIn("::notice::", output)
                    self.assertIn("ci#832", output)
                elif expected == 1:
                    self.assertIn("::error::", output)
                else:
                    self.assertIn("GUARD_RESULT=kept-polling", output)


def _preamble() -> str:
    """Harness preamble: the waiter's variables with a stubbed API fetch."""
    return (
        "set -u\n"
        'run_status="in_progress"\n'
        'run_id="39823960049"\n'
        'ci_repo="example/example-ci"\n'
        'poll_interval="0"\n'
        'github_api_get() { cat "$FIXTURE"; }\n'
        "interruptible_sleep() { :; }\n"
        "for attempt in 1; do\n"
    )


def _epilogue() -> str:
    """Close the guard's outer run-status `if`, then report a fall-through.

    The shipped guard block ends at the `fi` closing its *inner* failed-job
    branch; the outer `if [ "$run_status" != "completed" ]` is closed here so the
    extracted snippet is syntactically complete.
    """
    return (
        "fi\n"
        "done\n"
        'echo "GUARD_RESULT=kept-polling"\n'
        "exit %d\n" % KEPT_POLLING
    )


def _run_all_guards(payload: str) -> tuple[int, str]:
    """Run every extracted guard block with a stubbed job-listing fetch.

    The guard's `github_api_get` is replaced by a stub that prints a fixture job
    listing, so the shipped shell (not a copy) decides the outcome. All three
    copies must agree; a divergent copy fails the caller. Falls through the
    guard as `KEPT_POLLING` instead of a real `continue` loop.
    """
    blocks = _guard_blocks()
    outputs: list[str] = []
    codes: set[int] = set()
    for line_no, block in blocks:
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = pathlib.Path(tmp)
            fixture_path = tmp_path / "jobs.json"
            fixture_path.write_text(payload, encoding="utf-8")
            script_path = tmp_path / "guard.sh"
            script_path.write_text(
                _preamble() + block + "\n" + _epilogue(),
                encoding="utf-8",
            )
            result = subprocess.run(
                ["bash", str(script_path)],
                capture_output=True,
                text=True,
                check=False,
                env={
                    "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
                    "FIXTURE": str(fixture_path),
                },
            )
            codes.add(result.returncode)
            outputs.append(
                "--- guard block at line %d (exit %d) ---\n%s%s"
                % (line_no, result.returncode, result.stdout, result.stderr)
            )
    combined = "\n".join(outputs)
    if len(codes) != 1:
        raise AssertionError(
            "the ci#832 guard copies disagree on an identical job listing: %s\n%s"
            % (sorted(codes), combined)
        )
    return codes.pop(), combined


if __name__ == "__main__":
    unittest.main()
