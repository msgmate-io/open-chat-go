#!/usr/bin/env python3
"""Regression guard for ci#830 - the flaky `commits/{sha}/pulls` resolver.

`.github/workflows/auto-approve-consolidation.yaml` -> `Resolve candidate PRs`
asks `GET /repos/{repo}/commits/{sha}/pulls` which, for this repository,
intermittently answers HTTP 200/500 with an *empty or truncated* body. `gh api`
then exits 1 with `unexpected end of JSON input`, and the old resolver turned
that into a hard `::error::Failed to list the PRs ... (API/credentials)` - so a
single unlucky call starved auto-approve and left an otherwise green
consolidation PR `BLOCKED / REVIEW_REQUIRED`.

The fix (ci#830) is a bounded retry around the call plus a `gh pr list`
fallback matched on `headRefOid`, and the retry only accepts a *usable* payload:
`gh api --paginate` emits one JSON document per page, so the guard slurps and
flattens the documents (`jq -ces '... then add ...'`). A single page is
unchanged, a multi-page answer becomes one array, and an empty body, a truncated
body, `null` or an error object is rejected and retried.

This file extracts that step's shell verbatim from the workflow, substitutes the
`${{ ... }}` expressions and runs it against a stubbed `gh`, so the assertions
are about the shipped shell and not a copy. Deliberately stdlib-only: `Pin
Freshness Gate` runs it with the system `python3`.
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

STEP_NAME = "Resolve candidate PRs"
HEAD = "2628fb13cc848fd72226065f46dce7f9de6968ee"
REPO = "example/example"

CONSOLIDATION_BRANCH = "chore/consolidate-r64-2026-10-22"

# The step's `run:` body is indented ten spaces inside the YAML file.
RUN_INDENT = 10


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _resolver_script() -> str:
    """Extract the `Resolve candidate PRs` step's shell, dedented, verbatim."""
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
    """The extracted shell with the `${{ ... }}` expressions substituted."""
    script = _resolver_script()
    script = script.replace("${{ github.repository }}", REPO)
    script = script.replace("${{ github.event.workflow_run.head_sha }}", HEAD)
    assert "${{" not in script, "unsubstituted expression left in the resolver"
    return script


def _pull(number: int) -> dict:
    """One PR as `commits/{sha}/pulls` reports it (the shape downstream reads)."""
    return {
        "number": number,
        "base": {"ref": "main"},
        "head": {"ref": CONSOLIDATION_BRANCH, "repo": {"full_name": REPO}},
    }


def _api_page(*numbers: int) -> str:
    """One `gh api --paginate` page body holding the given PR numbers."""
    return json.dumps([_pull(number) for number in numbers])


class ResolverRun:
    """One execution of the shipped resolver against a stubbed `gh`."""

    def __init__(self, api_bodies: list, pr_list_body: str = "[]"):
        self.api_bodies = api_bodies
        self.pr_list_body = pr_list_body

    def run(self) -> tuple[int, str, str, list[str]]:
        work = tempfile.mkdtemp(prefix="ci830-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)

        def write(name: str, content: str) -> None:
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)

        # `gh` stub: the API listing answers from a scripted list of payloads
        # (a non-zero status is modelled as an empty body), the PR listing
        # answers its own body. Every invocation is logged.
        write(
            "gh",
            "#!/usr/bin/env bash\n"
            'printf \'%s\\n\' "$*" >> "$GH_LOG"\n'
            'if [ "$1" = "api" ]; then\n'
            '  n="$(cat "$GH_COUNTER" 2>/dev/null || echo 0)"\n'
            "  n=$((n + 1))\n"
            '  printf \'%s\' "$n" > "$GH_COUNTER"\n'
            '  body="$(sed -n "${n}p" "$GH_API_BODIES")"\n'
            '  case "$body" in\n'
            "    @fail) exit 1 ;;\n"
            "    *) printf '%s' \"$body\" ;;\n"
            "  esac\n"
            "  exit 0\n"
            "fi\n"
            'if [ "$1" = "pr" ] && [ "$2" = "list" ]; then\n'
            '  printf \'%s\' "$(cat "$GH_PR_LIST_BODY")"\n'
            "  exit 0\n"
            "fi\n"
            "exit 0\n",
        )
        # The retry backs off 5s/10s; keep the test instant.
        write("sleep", "#!/usr/bin/env bash\nexit 0\n")

        with open(os.path.join(work, "api_bodies"), "w", encoding="utf-8") as handle:
            handle.write("\n".join(self.api_bodies) + "\n")
        with open(os.path.join(work, "pr_list_body"), "w", encoding="utf-8") as handle:
            handle.write(self.pr_list_body)
        with open(os.path.join(work, "gh.log"), "w", encoding="utf-8") as handle:
            handle.write("")
        output_path = os.path.join(work, "github_output")
        open(output_path, "w", encoding="utf-8").close()

        script_path = os.path.join(work, "resolver.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write(_executable_script())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "GH_LOG": os.path.join(work, "gh.log"),
                "GH_COUNTER": os.path.join(work, "counter"),
                "GH_API_BODIES": os.path.join(work, "api_bodies"),
                "GH_PR_LIST_BODY": os.path.join(work, "pr_list_body"),
                "GITHUB_OUTPUT": output_path,
                "EVENT_NAME": "workflow_run",
                "PR_NUMBER_INPUT": "",
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )
        with open(os.path.join(work, "gh.log"), encoding="utf-8") as handle:
            calls = [line for line in handle.read().splitlines() if line]
        with open(output_path, encoding="utf-8") as handle:
            outputs = handle.read()
        return result.returncode, result.stdout, outputs, calls


class AutoApproveFlakyPullsTest(unittest.TestCase):
    def test_the_resolver_shell_is_valid(self) -> None:
        script = _executable_script()
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(script)
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_the_guard_is_documented_and_bounded(self) -> None:
        script = _resolver_script()
        self.assertIn("ci#830", script)
        self.assertIn("gh api --paginate", script)
        # Bounded retry with backoff, not an unbounded loop.
        self.assertRegex(script, r"for attempt in 1 2 3; do")
        self.assertIn('sleep "$((attempt * 5))"', script)
        # The fallback matches the triggering commit, not just the branch name.
        self.assertIn("--json number,headRefName,headRefOid", script)
        self.assertIn(".headRefOid == $sha", script)
        self.assertIn('startswith("chore/consolidate-")', script)

    def test_the_guard_slurps_paginated_documents(self) -> None:
        """`--paginate` emits one document per page; the old check rejected it."""
        script = _resolver_script()
        self.assertIn("jq -ces", script)
        self.assertIn("add", script)
        self.assertNotIn(
            "jq -e 'type == \"array\"'",
            script,
            "the single-document array check cannot accept a multi-page "
            "`[...][...]` answer and must stay replaced by the slurp+add guard "
            "(ci#830)",
        )

    def test_flaky_then_good_payload_resolves_without_the_fallback(self) -> None:
        rc, out, outputs, calls = ResolverRun(
            ["@fail", _api_page(999)]
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("prs=999", outputs)
        self.assertIn("ci#830", out)
        api_calls = [c for c in calls if c.startswith("api ")]
        self.assertEqual(len(api_calls), 2, calls)
        self.assertEqual([c for c in calls if c.startswith("pr list")], [])

    def test_truncated_body_is_rejected_and_retried(self) -> None:
        rc, out, outputs, _ = ResolverRun(
            [_api_page(999)[:20], _api_page(999)]
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("prs=999", outputs)

    def test_multipage_answer_is_flattened(self) -> None:
        """`[{a}][{b}]` is not one JSON document; the guard must still use it."""
        rc, out, outputs, calls = ResolverRun(
            [_api_page(999) + _api_page(998)]
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("prs=999,998", outputs)
        self.assertEqual([c for c in calls if c.startswith("pr list")], [])

    def test_empty_array_is_a_clean_no_op(self) -> None:
        """`[]` means the commit is not a PR head: exit 0, no PR, no fallback."""
        rc, out, outputs, calls = ResolverRun(["[]"]).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("nothing to approve", out)
        self.assertIn("prs=", outputs)
        self.assertNotIn("prs=9", outputs)
        self.assertEqual([c for c in calls if c.startswith("pr list")], [])

    def test_null_payload_is_rejected_and_retried(self) -> None:
        rc, out, outputs, _ = ResolverRun(["null", _api_page(999)]).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("prs=999", outputs)

    def test_all_attempts_flaky_falls_back_to_the_pull_listing(self) -> None:
        rc, out, outputs, calls = ResolverRun(
            ["@fail", "@fail", "@fail"],
            pr_list_body='[{"number":999,"headRefName":"%s","headRefOid":"%s"},'
            '{"number":1,"headRefName":"chore/consolidate-old","headRefOid":"deadbeef"}]'
            % (CONSOLIDATION_BRANCH, HEAD),
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("Falling back to the pull listing", out)
        self.assertIn("prs=999", outputs)
        self.assertNotIn("prs=999,1", outputs)
        self.assertEqual(len([c for c in calls if c.startswith("api ")]), 3, calls)
        self.assertEqual(len([c for c in calls if c.startswith("pr list")]), 1, calls)

    def test_fully_flaky_api_with_empty_fallback_is_a_clean_no_op(self) -> None:
        """Both listings unusable but *valid* (`[]`) must not hard-fail.

        Before ci#830 the resolver died on the very first flaky call. With the
        fix it retries, then falls back; an empty fallback listing means "no
        consolidation PR for this commit", which is a no-op, not a red job -
        otherwise a flaky endpoint still starves auto-approve.
        """
        rc, out, outputs, calls = ResolverRun(["@fail", "@fail", "@fail"]).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("nothing to approve", out)
        self.assertIn("ci#830", out)
        self.assertEqual(len([c for c in calls if c.startswith("api ")]), 3, calls)
        self.assertEqual(len([c for c in calls if c.startswith("pr list")]), 1, calls)

    def test_unparseable_fallback_still_fails_loudly(self) -> None:
        """Both paths unusable and the fallback body is not JSON: fail closed."""
        rc, out, _, _ = ResolverRun(
            ["@fail", "@fail", "@fail"],
            pr_list_body="<html>502 Bad Gateway</html>",
        ).run()
        self.assertEqual(rc, 1, out)
        self.assertIn("::error::", out)
        self.assertIn("API/credentials", out)
        # The error body must never be mistaken for a PR reference (ci#538).
        self.assertNotIn("Unexpected non-numeric PR reference", out)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
