#!/usr/bin/env python3
"""Guards the ci#1066 merge-protection preflight of the auto-approve workflow.

Reason for the guard: the `dev` -> `main` promotion hop looked healthy for a
while *because the gate was off*. `Main (Staging) Merge Protection` was disabled
(`rules/branches/main` returned `[]`), so an armed `--auto` merge landed
unattended and a green promotion PR proved nothing about the write-access
approval the ruleset demands once it is re-enabled. The workflow now classifies
the live protection state before anything merges:

  review_required_enforced - the ruleset enforces N approving reviews: the hop
                             needs an approver token / bypass actor (reported).
  review_required_masked   - no approving review is enforced: the unattended hop
                             works only because the gate is off (warned loudly).
  protection_unknown       - the rules API could not be read.

The guard runs the shipped shell against a stubbed `gh` (no token, no network)
and asserts the three states, that the reporting is a notice/warning and never
an error, and that the step can never turn the auto-approve run red - a red run
must keep meaning "a real regression".

Like `test_auto_approve_human_required.py` / `test_auto_approve_stale_check_instances.py`,
the targeted CI step is extracted verbatim from the workflow, so a rename breaks
this guard instead of silently dropping the coverage.
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

STEP_NAME = "Preflight main merge protection (ci#1066)"

REPO = "example/example"
APPROVE_STEP = "Approve and merge"


def _workflow_text() -> str:
    return WORKFLOW.read_text(encoding="utf-8")


def _step_shell() -> str:
    """Extract the preflight step's shell, dedented, verbatim."""
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


def emit(text):
    sys.stdout.write(text if text.endswith("\\n") else text + "\\n")


if len(args) > 1 and args[0] == "api":
    endpoint = args[1]
    if endpoint == "repos/%s/rules/branches/main" % cfg["repo"]:
        if cfg.get("rules_rc", 0) != 0:
            sys.stderr.write("gh: Not Found (HTTP 404)\\n")
            sys.exit(int(cfg["rules_rc"]))
        emit(cfg.get("rules_body", "[]"))
        sys.exit(0)
    if endpoint == "repos/%s/rulesets" % cfg["repo"]:
        if cfg.get("rulesets_rc", 0) != 0:
            sys.exit(int(cfg["rulesets_rc"]))
        emit(cfg.get("rulesets_body", "[]"))
        sys.exit(0)
    prefix = "repos/%s/rulesets/" % cfg["repo"]
    if endpoint.startswith(prefix):
        ident = endpoint[len(prefix) :]
        details = cfg.get("ruleset_details", {})
        if ident not in details:
            sys.stderr.write("gh: Not Found (HTTP 404)\\n")
            sys.exit(1)
        emit(json.dumps(details[ident]))
        sys.exit(0)
sys.exit(0)
"""

MAIN_RULESET = {
    "id": 26947244,
    "name": "Main (Staging) Merge Protection",
    "target": "branch",
    "enforcement": "active",
    "bypass_actors": [],
    "conditions": {"ref_name": {"include": ["refs/heads/main"], "exclude": []}},
    "rules": [
        {"type": "deletion"},
        {
            "type": "pull_request",
            "parameters": {
                "required_approving_review_count": 1,
                "dismiss_stale_reviews_on_push": True,
            },
        },
    ],
}

REVIEW_RULE = {
    "type": "pull_request",
    "parameters": {"required_approving_review_count": 1},
}


class PreflightRun:
    """One execution of the shipped preflight step against a stubbed `gh`."""

    def __init__(
        self,
        *,
        rules_body=None,
        rules_rc: int = 0,
        rulesets_body=None,
        rulesets_rc: int = 0,
        ruleset_details: dict | None = None,
        has_approver_token: bool = False,
    ):
        self.rules_body = [] if rules_body is None else rules_body
        self.rules_rc = rules_rc
        self.rulesets_rc = rulesets_rc
        self.rulesets_body = (
            [{"id": MAIN_RULESET["id"]}] if rulesets_body is None else rulesets_body
        )
        self.ruleset_details = (
            {str(MAIN_RULESET["id"]): MAIN_RULESET}
            if ruleset_details is None
            else ruleset_details
        )
        self.has_approver_token = has_approver_token

    def run(self) -> tuple[int, str, str, str, list[str]]:
        work = tempfile.mkdtemp(prefix="ci1066-preflight-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)
        stub = os.path.join(bindir, "gh")
        with open(stub, "w", encoding="utf-8") as handle:
            handle.write(GH_STUB)
        os.chmod(stub, os.stat(stub).st_mode | stat.S_IEXEC)

        config = {
            "repo": REPO,
            "rules_body": json.dumps(self.rules_body),
            "rules_rc": self.rules_rc,
            "rulesets_rc": self.rulesets_rc,
            "rulesets_body": json.dumps(self.rulesets_body),
            "ruleset_details": self.ruleset_details,
        }
        config_path = os.path.join(work, "gh_config.json")
        with open(config_path, "w", encoding="utf-8") as handle:
            json.dump(config, handle)

        gh_log = os.path.join(work, "gh.log")
        summary_path = os.path.join(work, "summary.md")
        output_path = os.path.join(work, "output.txt")
        for path in (gh_log, summary_path, output_path):
            open(path, "w", encoding="utf-8").close()

        script_path = os.path.join(work, "preflight.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write(_step_shell())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "GH_TOKEN": "ghs_fakeTokenThatMustNeverBeRendered0123456789",
                "GH_LOG": gh_log,
                "GH_CONFIG": config_path,
                "HAS_APPROVER_TOKEN": "true" if self.has_approver_token else "false",
                "REPO": REPO,
                "GITHUB_STEP_SUMMARY": summary_path,
                "GITHUB_OUTPUT": output_path,
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )

        with open(gh_log, encoding="utf-8") as handle:
            calls = [line for line in handle.read().splitlines() if line]
        with open(summary_path, encoding="utf-8") as handle:
            summary = handle.read()
        with open(output_path, encoding="utf-8") as handle:
            output = handle.read()
        return result.returncode, result.stdout + result.stderr, summary, output, calls


class MergeProtectionPreflightTest(unittest.TestCase):
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

    def test_the_preflight_runs_before_anything_merges(self) -> None:
        text = _workflow_text()
        preflight = text.index(f"- name: {STEP_NAME}")
        approve = text.index(f"- name: {APPROVE_STEP}")
        self.assertLess(preflight, approve, "the preflight must precede the merge step")
        # Both steps are gated on the same PR set, so the preflight always
        # observes exactly the PRs the merge step is about to act on.
        gate = "if: ${{ steps.prs.outputs.prs != '' }}"
        self.assertIn(gate, text[preflight:approve])

    def test_enforced_review_requirement_is_reported_as_enforced(self) -> None:
        rc, out, summary, output, _ = PreflightRun(
            rules_body=[REVIEW_RULE], has_approver_token=True
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("review_required_enforced", output)
        self.assertIn("::notice::", out)
        self.assertNotIn("::error::", out)
        self.assertIn("`review_required_enforced`", summary)
        self.assertIn("| Required approving reviews on `main` | 1 |", summary)
        self.assertIn("Main (Staging) Merge Protection", summary)
        self.assertIn("| `CONSOLIDATE_APPROVER_TOKEN` configured | true |", summary)

    def test_a_disabled_ruleset_is_named_as_masking_the_defect(self) -> None:
        rc, out, summary, output, _ = PreflightRun(rules_body=[]).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("review_required_masked", output)
        self.assertIn("::warning::", out)
        self.assertNotIn("::error::", out)
        self.assertIn("not active", out)
        self.assertIn("`review_required_masked`", summary)
        self.assertIn("**not enforced**", summary)
        # The three remediation paths are named so a maintainer knows the fix.
        self.assertIn("approver token", summary)
        self.assertIn("bypass actor", summary)
        self.assertIn("maintainer merge", summary)
        self.assertIn("| `CONSOLIDATE_APPROVER_TOKEN` configured | false |", summary)

    def test_an_unreadable_rules_api_is_unknown_and_never_red(self) -> None:
        rc, out, summary, output, _ = PreflightRun(rules_rc=1).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("protection_unknown", output)
        self.assertIn("::warning::", out)
        self.assertNotIn("::error::", out)
        self.assertIn("`protection_unknown`", summary)

    def test_a_non_array_rules_payload_is_not_mistaken_for_masked(self) -> None:
        rc, out, summary, output, _ = PreflightRun(rules_body={"message": "Not Found"}).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("protection_unknown", output)
        self.assertNotIn("review_required_masked", output)

    def test_bypass_actors_are_counted(self) -> None:
        ruleset = dict(MAIN_RULESET)
        ruleset["bypass_actors"] = [
            {"actor_id": 12345, "actor_type": "Integration", "bypass_mode": "always"}
        ]
        rc, out, summary, output, calls = PreflightRun(
            rules_body=[REVIEW_RULE],
            ruleset_details={str(ruleset["id"]): ruleset},
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("review_required_enforced", output)
        self.assertIn("| Bypass actors on the `main` ruleset(s) | 1 |", summary)
        self.assertIn("bypass actors 1", out)
        # The effective rules and the ruleset detail were both consulted.
        self.assertTrue(any("rules/branches/main" in c for c in calls), calls)
        self.assertTrue(any("rulesets/" in c for c in calls), calls)

    def test_a_non_branch_ruleset_is_ignored(self) -> None:
        tag_ruleset = dict(MAIN_RULESET, id=99, name="Tag protection", target="tag")
        rc, _, summary, _, _ = PreflightRun(
            rules_body=[REVIEW_RULE],
            rulesets_body=[{"id": 99}],
            ruleset_details={"99": tag_ruleset},
        ).run()
        self.assertEqual(rc, 0)
        self.assertIn("no active branch ruleset for refs/heads/main", summary)

    def test_the_token_value_is_never_rendered(self) -> None:
        rc, out, summary, _, _ = PreflightRun(rules_body=[REVIEW_RULE]).run()
        self.assertEqual(rc, 0)
        self.assertNotIn("ghs_fakeTokenThatMustNeverBeRendered0123456789", out + summary)

    def test_a_ruleset_api_failure_cannot_fail_the_run(self) -> None:
        """A red auto-approve run must keep meaning "a real regression"."""
        rc, out, _, _, _ = PreflightRun(rules_body=[REVIEW_RULE], rulesets_rc=1).run()
        self.assertEqual(rc, 0, out)


if __name__ == "__main__":
    unittest.main()