#!/usr/bin/env python3
"""ci#1044 - unit tests for check_bot_token_capabilities.py (stdlib only).

The capability matrix decides whether a candidate deployment-profile BOT_TOKEN
may be written to the vault, so its verdicts are pinned here: the required scope
set from the ticket, the 404-means-not-selected classification that reproduces
the git exit 128 of run 42959618854, the honest `UNVERIFIED` for the write
capability no read-only probe can prove, and the rule that the credential never
reaches the output.

Run with:

    python3 -m unittest development/scripts/test_check_bot_token_capabilities.py -v
"""

from __future__ import annotations

import importlib.util
import io
import json
import os
import unittest
from contextlib import redirect_stdout

_HERE = os.path.dirname(os.path.abspath(__file__))
_MODULE_PATH = os.path.join(_HERE, "check_bot_token_capabilities.py")
_spec = importlib.util.spec_from_file_location("check_bot_token_capabilities", _MODULE_PATH)
bot = importlib.util.module_from_spec(_spec)
assert _spec and _spec.loader
_spec.loader.exec_module(bot)

FAKE_FINE_GRAINED = "github_pat_" + "A" * 40
FAKE_CLASSIC = "ghp_" + "B" * 36


def _verdicts(findings):
    return {f.capability: f.verdict for f in findings}


def _finding(findings, capability):
    for finding in findings:
        if finding.capability == capability:
            return finding
    raise AssertionError("no finding for %r in %r" % (capability, findings))


def _full_access(package=bot.DEPLOY_PACKAGE):
    return [{"type": "repository", "name": package,
             "actions": ["pull", "push"]}]


def _healthy_observations(**overrides):
    observations = {
        "token_shape": bot.FINE_GRAINED,
        "contents_status": 200,
        "pulls_status": 200,
        "registry_status": 200,
        "registry_access": _full_access(),
    }
    observations.update(overrides)
    return observations


class TokenShapeTests(unittest.TestCase):
    def test_fine_grained_pat_is_the_only_accepted_shape(self):
        self.assertEqual(bot.token_shape(FAKE_FINE_GRAINED), bot.FINE_GRAINED)
        self.assertEqual(bot.token_shape(FAKE_CLASSIC), bot.CLASSIC)
        self.assertEqual(bot.token_shape("ghs_" + "c" * 20), bot.GITHUB_APP)
        self.assertEqual(bot.token_shape(""), bot.UNKNOWN_SHAPE)

    def test_strip_credential_drops_copy_paste_line_noise(self):
        self.assertEqual(bot.strip_credential("\n  token-value \r\n"), "token-value")
        self.assertEqual(bot.strip_credential(None), "")


class CapabilityMatrixTests(unittest.TestCase):
    def test_required_scope_set_matches_the_ticket(self):
        """ci#1044: contents:read, pull_requests:write, packages:read/write."""
        self.assertEqual(
            bot.CAPABILITIES,
            ("token shape", "contents:read", "pull_requests:read",
             "pull_requests:write", "packages:read", "packages:write"))

    def test_healthy_token_has_no_blocking_problem(self):
        findings = bot.evaluate(_healthy_observations())
        self.assertFalse(bot.failed(findings))
        self.assertEqual(bot.problems(findings), [])
        verdicts = _verdicts(findings)
        self.assertEqual(verdicts["token shape"], bot.OK)
        self.assertEqual(verdicts["contents:read"], bot.OK)
        self.assertEqual(verdicts["packages:read"], bot.OK)
        self.assertEqual(verdicts["packages:write"], bot.OK)

    def test_write_capability_is_unverified_not_invented(self):
        """No read-only endpoint proves pull_requests:write on a fine-grained PAT."""
        findings = bot.evaluate(_healthy_observations())
        write = _finding(findings, "pull_requests:write")
        self.assertEqual(write.verdict, bot.UNVERIFIED)
        self.assertIn("preview", write.detail)
        self.assertFalse(bot.failed(findings))

    def test_classic_pat_is_rejected_by_shape(self):
        findings = bot.evaluate(_healthy_observations(token_shape=bot.CLASSIC))
        shape = _finding(findings, "token shape")
        self.assertEqual(shape.verdict, bot.MISSING)
        self.assertIn(bot.DEPLOY_REPO, shape.detail)
        self.assertIn("42959618854", shape.detail)
        self.assertTrue(bot.failed(findings))

    def test_repository_not_selected_is_reported_as_the_git_128_cause(self):
        """Run 42959618854: a token scoped elsewhere answers 404, git exits 128."""
        findings = bot.evaluate(_healthy_observations(contents_status=404))
        contents = _finding(findings, "contents:read")
        self.assertEqual(contents.verdict, bot.MISSING)
        self.assertIn("404", contents.detail)
        self.assertIn("exit 128", contents.detail)
        self.assertTrue(bot.failed(findings))

    def test_expired_token_is_named(self):
        findings = bot.evaluate(_healthy_observations(contents_status=401))
        contents = _finding(findings, "contents:read")
        self.assertEqual(contents.verdict, bot.MISSING)
        self.assertIn("expired or revoked", contents.detail)

    def test_missing_pull_request_scope_is_named(self):
        findings = bot.evaluate(_healthy_observations(pulls_status=403))
        pulls = _finding(findings, "pull_requests:read")
        self.assertEqual(pulls.verdict, bot.MISSING)
        self.assertTrue(bot.failed(findings))

    def test_registry_without_scope_blocks_both_package_capabilities(self):
        findings = bot.evaluate(_healthy_observations(registry_access=[]))
        self.assertEqual(_finding(findings, "packages:read").verdict, bot.MISSING)
        self.assertEqual(_finding(findings, "packages:write").verdict, bot.MISSING)
        self.assertIn("pr-<n>-<sha>",
                      _finding(findings, "packages:write").detail)
        self.assertTrue(bot.failed(findings))

    def test_pull_only_registry_scope_reports_the_missing_push(self):
        access = [{"type": "repository", "name": bot.DEPLOY_PACKAGE,
                   "actions": ["pull"]}]
        findings = bot.evaluate(_healthy_observations(registry_access=access))
        self.assertEqual(_finding(findings, "packages:read").verdict, bot.OK)
        write = _finding(findings, "packages:write")
        self.assertEqual(write.verdict, bot.MISSING)
        self.assertIn("'push'", write.detail)
        self.assertTrue(bot.failed(findings))

    def test_registry_scope_for_another_package_is_not_counted(self):
        findings = bot.evaluate(_healthy_observations(
            registry_access=_full_access("msgmate-io/other-app")))
        self.assertEqual(_finding(findings, "packages:read").verdict, bot.MISSING)

    def test_unreachable_registry_is_reported_not_assumed(self):
        findings = bot.evaluate(_healthy_observations(registry_status=0))
        self.assertEqual(_finding(findings, "packages:read").verdict, bot.MISSING)
        self.assertIn("no response", _finding(findings, "packages:read").detail)


class OutputHygieneTests(unittest.TestCase):
    def test_report_never_contains_the_credential(self):
        findings = bot.evaluate(_healthy_observations())
        report = bot.render(findings, bot.token_shape(FAKE_FINE_GRAINED))
        self.assertIn("contents:read", report)
        self.assertNotIn(FAKE_FINE_GRAINED, report)
        self.assertNotIn(FAKE_FINE_GRAINED[len("github_pat_"):], report)

    def test_probe_observations_carry_no_credential(self):
        class StubProbe(bot.Probe):
            def github_status(self, path):  # noqa: ARG002 - stub
                return 200

            def registry_scope(self):
                return 200, _full_access()

        observations = StubProbe(FAKE_FINE_GRAINED).observations()
        serialised = json.dumps(observations)
        self.assertNotIn(FAKE_FINE_GRAINED, serialised)
        self.assertEqual(observations["token_shape"], bot.FINE_GRAINED)


class CliTests(unittest.TestCase):
    def _run(self, *argv):
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = bot.main(list(argv))
        return code, buffer.getvalue()

    def test_offline_mode_passes_a_healthy_token(self):
        code, output = self._run(
            "--observations-json", json.dumps(_healthy_observations()))
        self.assertEqual(code, 0)
        self.assertIn("pull_requests:write", output)
        self.assertIn("UNVERIFIED", output)

    def test_offline_mode_fails_the_rejected_vault_token(self):
        code, output = self._run("--observations-json", json.dumps(
            _healthy_observations(contents_status=404)))
        self.assertEqual(code, 1)
        self.assertIn("::error::ci#1044:", output)
        self.assertIn("msgmate-io-pr-deployment", output)

    def test_no_candidate_token_fails_closed(self):
        saved = {k: os.environ.pop(k, None)
                 for k in ("BOT_TOKEN_CANDIDATE", "BOT_TOKEN")}
        try:
            code, output = self._run()
        finally:
            for key, value in saved.items():
                if value is not None:
                    os.environ[key] = value
        self.assertEqual(code, 1)
        self.assertIn("no candidate token", output)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
