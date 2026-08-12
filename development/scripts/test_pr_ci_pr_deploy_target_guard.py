#!/usr/bin/env python3
"""Unit tests for the ci#890 PR-deploy target guard.

Deliberately stdlib-only: the `Pin Freshness Gate` job runs the guard tests with
the system `python3`, before the build-tools venv (PyYAML) is guaranteed.
"""

from __future__ import annotations

import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).resolve().parent / "pr_ci_pr_deploy_target_guard.py"
SPEC = importlib.util.spec_from_file_location("pr_ci_pr_deploy_target_guard", MODULE_PATH)
assert SPEC and SPEC.loader
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


def payload(**overrides):
    base = {
        "deployment_name": guard.PR_DEPLOYMENT,
        "namespace_override": "open-chat-pr-42",
        "deploy_host_override": "my-branch-pr-42.pr-env.msgmate.io",
        "helm_release_override": "backend-pr-42",
        "pr_number": "42",
        "pr_state": "open",
    }
    base.update(overrides)
    return base


class PrPreviewDispatchTests(unittest.TestCase):
    def test_pr_scoped_dispatch_is_allowed(self):
        self.assertEqual(guard.evaluate(payload()), [])

    def test_closed_pr_is_refused(self):
        problems = guard.evaluate(payload(pr_state="closed"))
        self.assertTrue(any("closed/merged PR" in p for p in problems))

    def test_merged_pr_is_refused(self):
        problems = guard.evaluate(payload(pr_state="merged"))
        self.assertTrue(any("closed/merged PR" in p for p in problems))

    def test_blank_overrides_are_refused(self):
        problems = guard.evaluate(payload(namespace_override="", deploy_host_override="",
                                          helm_release_override=""))
        joined = "\n".join(problems)
        self.assertIn("missing NAMESPACE_OVERRIDE", joined)
        self.assertIn("missing DEPLOY_HOST_OVERRIDE", joined)
        self.assertIn("missing HELM_RELEASE_OVERRIDE", joined)

    def test_missing_pr_number_is_refused(self):
        problems = guard.evaluate(payload(pr_number=""))
        self.assertTrue(any("no PR number" in p for p in problems))

    def test_ci_namespace_is_refused(self):
        problems = guard.evaluate(payload(namespace_override=guard.CI_NAMESPACE))
        self.assertTrue(any("CI deployment namespace" in p for p in problems))

    def test_ci_host_is_refused(self):
        problems = guard.evaluate(payload(deploy_host_override=guard.CI_HOST))
        self.assertTrue(any("CI deployment host" in p for p in problems))

    def test_foreign_preview_host_is_refused(self):
        problems = guard.evaluate(payload(deploy_host_override="other-pr-99.pr-env.msgmate.io"))
        self.assertTrue(any("does not belong to PR #42" in p for p in problems))

    def test_non_preview_host_is_refused(self):
        problems = guard.evaluate(payload(deploy_host_override="preview.example.com"))
        self.assertTrue(any("not a .pr-env.msgmate.io host" in p for p in problems))

    def test_mismatched_namespace_and_release_are_refused(self):
        problems = guard.evaluate(payload(namespace_override="open-chat-pr-99",
                                          helm_release_override="backend-pr-99"))
        self.assertTrue(any("not PR-scoped" in p for p in problems))


class SharedDeploymentDispatchTests(unittest.TestCase):
    def test_plain_ci_deploy_without_overrides_is_allowed(self):
        self.assertEqual(guard.evaluate({"deployment_name": guard.CI_DEPLOYMENT}), [])

    def test_ci_deploy_with_pr_overrides_is_refused(self):
        problems = guard.evaluate({
            "deployment_name": guard.CI_DEPLOYMENT,
            "namespace_override": "open-chat-pr-7",
            "deploy_host_override": "x-pr-7.pr-env.msgmate.io",
            "helm_release_override": "backend-pr-7",
        })
        self.assertTrue(any("would redirect the CI deployment" in p for p in problems))

    def test_non_preview_deploy_with_pr_overrides_is_refused(self):
        problems = guard.evaluate({"deployment_name": "msgmate-io-pr-deployment-prod",
                                   "namespace_override": "open-chat-pr-7"})
        self.assertTrue(any("is not a PR preview" in p for p in problems))

    def test_unnamed_deployment_without_overrides_is_allowed(self):
        self.assertEqual(guard.evaluate({}), [])


class CliTests(unittest.TestCase):
    def test_cli_exit_codes(self):
        self.assertEqual(guard.main(["--deployment-name", guard.CI_DEPLOYMENT]), 0)
        self.assertEqual(
            guard.main(["--deployment-name", guard.PR_DEPLOYMENT,
                        "--pr-number", "42", "--pr-state", "closed",
                        "--namespace-override", "open-chat-pr-42",
                        "--deploy-host-override", "b-pr-42.pr-env.msgmate.io",
                        "--helm-release-override", "backend-pr-42"]),
            1,
        )

    def test_cli_rejects_bad_json(self):
        self.assertEqual(guard.main(["--payload-json", "{not json"]), 1)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
