#!/usr/bin/env python3
"""ci#1044 - unit tests for verify_deployment_bot_token_rotation.py (stdlib only).

The rotation is only "done" when a `[Dev promotion]` deployment run reports the
vault token as its source. These tests pin that reading: the CI-secret fallback
must never be mistaken for a completed rotation, the rejected-vault warning and
the precondition's `bot token cannot read` error must both fail the verdict, and
no token can reach the report.

Run with:

    python3 -m unittest development/scripts/test_verify_deployment_bot_token_rotation.py -v
"""

from __future__ import annotations

import importlib.util
import io
import json
import os
import unittest
from contextlib import redirect_stdout

_HERE = os.path.dirname(os.path.abspath(__file__))
_MODULE_PATH = os.path.join(_HERE, "verify_deployment_bot_token_rotation.py")
_spec = importlib.util.spec_from_file_location("verify_deployment_bot_token_rotation", _MODULE_PATH)
rot = importlib.util.module_from_spec(_spec)
assert _spec and _spec.loader
_spec.loader.exec_module(rot)

FAKE_VAULT_TOKEN = "github_pat_" + "A" * 40

GREEN_STEPS = {
    rot.PRECONDITION_STEP: "success",
    rot.DEPLOY_STEP: "success",
    rot.VERSION_STEP: "success",
}

VAULT_LOG = [
    "2026-11-11T14:00:00Z Using the bot token from deployment vault BOT_TOKEN "
    "(profile 'msgmate-io-pr-deployment').",
    "2026-11-11T14:00:01Z Bot token for msgmate-io/open-chat-go@dev verified "
    "and written to the 0600 file shared with the caller.",
]

CI_FALLBACK_LOG = [
    "2026-11-11T14:00:00Z ::warning::The deployment vault BOT_TOKEN for "
    "'msgmate-io-pr-deployment' cannot read msgmate-io/open-chat-go@dev "
    "(HTTP 404 - the credential cannot see the repository (contents:read "
    "missing), or the ref does not exist). Continuing with CI repository "
    "secret BOT_TOKEN; re-issue that vault item so the deployment identity "
    "stays usable (ci#1042).",
    "2026-11-11T14:00:01Z Using the bot token from CI repository secret BOT_TOKEN.",
]


def _verdicts(criteria):
    return [c.verdict for c in criteria]


def _criterion(criteria, prefix):
    for criterion in criteria:
        if criterion.name.startswith(prefix):
            return criterion
    raise AssertionError("no criterion starting with %r in %r" % (prefix, criteria))


class TokenSourceTests(unittest.TestCase):
    def test_vault_selection_is_the_only_completed_rotation(self):
        source, detail = rot.token_source(VAULT_LOG)
        self.assertEqual(source, rot.VAULT)
        self.assertIn("vault BOT_TOKEN", detail)
        self.assertEqual(_verdicts(rot.evaluate(VAULT_LOG, GREEN_STEPS)), [rot.OK] * 3)

    def test_ci_secret_fallback_is_not_a_rotation(self):
        source, _ = rot.token_source(CI_FALLBACK_LOG)
        self.assertEqual(source, rot.CI_FALLBACK)
        criteria = rot.evaluate(CI_FALLBACK_LOG, GREEN_STEPS)
        self.assertEqual(_criterion(criteria, "1").verdict, rot.FAIL)
        # A green deploy on the fallback is exactly the wrong-but-working state.
        self.assertEqual(_criterion(criteria, "3").verdict, rot.OK)

    def test_rejected_vault_warning_fails_even_without_a_selected_source(self):
        lines = CI_FALLBACK_LOG[:1]  # the warning only, no selected-source summary
        source, detail = rot.token_source(lines)
        self.assertEqual(source, rot.NO_TOKEN_SOURCE)
        self.assertIn("rejected", detail)

    def test_missing_summary_line_is_unknown_not_ok(self):
        criteria = rot.evaluate([], GREEN_STEPS)
        self.assertEqual(_criterion(criteria, "1").verdict, rot.UNKNOWN)

    def test_last_summary_line_wins(self):
        source, _ = rot.token_source(CI_FALLBACK_LOG + VAULT_LOG)
        self.assertEqual(source, rot.VAULT)


class PreconditionTests(unittest.TestCase):
    def test_precondition_error_fails_the_verdict(self):
        steps = dict(GREEN_STEPS, **{rot.PRECONDITION_STEP: "failure"})
        criteria = rot.evaluate(
            VAULT_LOG + [
                "::error::bot token cannot read msgmate-io/open-chat-go@dev: "
                "no usable credential.",
            ],
            steps,
        )
        self.assertEqual(
            _verdicts(criteria), [rot.OK, rot.FAIL, rot.OK]
        )
        self.assertIn("cannot read", _criterion(criteria, "2").detail)

    def test_precondition_failure_is_caught_from_the_log_alone(self):
        criteria = rot.evaluate(
            VAULT_LOG + ["::error::bot token cannot read msgmate-io/open-chat-go@dev"],
            {},
        )
        self.assertEqual(_criterion(criteria, "2").verdict, rot.FAIL)

    def test_absent_step_is_unknown(self):
        criteria = rot.evaluate(VAULT_LOG, {})
        self.assertEqual(_criterion(criteria, "2").verdict, rot.UNKNOWN)
        self.assertEqual(_criterion(criteria, "3").verdict, rot.UNKNOWN)


class StepAssertionTests(unittest.TestCase):
    def test_deploy_step_failure_fails_the_verdict(self):
        steps = dict(GREEN_STEPS, **{rot.DEPLOY_STEP: "failure"})
        criteria = rot.evaluate(VAULT_LOG, steps)
        self.assertEqual(_verdicts(criteria), [rot.OK, rot.OK, rot.FAIL])
        self.assertIn(rot.DEPLOY_STEP, _criterion(criteria, "3").detail)

    def test_version_assertion_failure_fails_the_verdict(self):
        steps = dict(GREEN_STEPS, **{rot.VERSION_STEP: "failure"})
        self.assertEqual(_criterion(rot.evaluate(VAULT_LOG, steps), "3").verdict, rot.FAIL)

    def test_step_conclusions_flattens_jobs_document(self):
        jobs = {"jobs": [
            {"name": "deploy-staging", "steps": [
                {"name": rot.PRECONDITION_STEP, "conclusion": "success"},
                {"name": rot.DEPLOY_STEP, "conclusion": "success"},
                {"name": rot.VERSION_STEP, "conclusion": "skipped"},
                {"name": "", "conclusion": "success"},
            ]},
            {"name": "other", "steps": [{"name": rot.VERSION_STEP, "conclusion": "success"}]},
        ]}
        steps = rot.step_conclusions(jobs)
        self.assertEqual(steps[rot.VERSION_STEP], "success")
        self.assertNotIn("", steps)
        self.assertEqual(_verdicts(rot.evaluate(VAULT_LOG, steps)), [rot.OK] * 3)

    def test_step_conclusions_ignores_malformed_entries(self):
        self.assertEqual(rot.step_conclusions(None), {})
        self.assertEqual(rot.step_conclusions([None, 5, {"steps": None}]), {})


class ReportTests(unittest.TestCase):
    def test_report_never_carries_a_credential(self):
        log = ["Using the bot token from deployment vault BOT_TOKEN",
               "::add-mask::" + FAKE_VAULT_TOKEN] + list(GREEN_STEPS)
        report = rot.render(rot.evaluate(log, GREEN_STEPS))
        self.assertNotIn(FAKE_VAULT_TOKEN, report)
        self.assertEqual(report.count(FAKE_VAULT_TOKEN), 0)
        self.assertIn("ROTATED", report)

    def test_cli_reports_failure_with_a_nonzero_exit(self):
        payload = json.dumps({"log_lines": CI_FALLBACK_LOG, "steps": GREEN_STEPS})
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = rot.main(["--observations-json", payload])
        self.assertEqual(code, 1)
        self.assertIn("NOT ROTATED", buffer.getvalue())

    def test_cli_accepts_a_jobs_document(self):
        payload = json.dumps({
            "log_lines": VAULT_LOG,
            "jobs": [{"name": "deploy-staging", "steps": [
                {"name": name, "conclusion": "success"} for name in GREEN_STEPS
            ]}],
        })
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = rot.main(["--observations-json", payload])
        self.assertEqual(code, 0, buffer.getvalue())
        self.assertIn("ROTATED", buffer.getvalue())

    def test_inconclusive_run_is_not_reported_as_rotated(self):
        report = rot.render(rot.evaluate(VAULT_LOG, {}))
        self.assertIn("INCONCLUSIVE", report)
        self.assertNotIn("ROTATED:", report)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
