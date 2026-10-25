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


class WorkflowWiringTests(unittest.TestCase):
    """The guard is only useful if the dispatch job actually runs it first."""

    WORKFLOW = (
        pathlib.Path(__file__).resolve().parents[2]
        / ".github"
        / "workflows"
        / "run-pr-ci-tests-and-deployments.yaml"
    )

    @classmethod
    def setUpClass(cls):
        cls.text = cls.WORKFLOW.read_text(encoding="utf-8")

    def test_dispatch_job_runs_the_guard_step(self):
        guard_step = self.text.index("name: Verify deploy dispatch target (ci#890)")
        dispatch_step = self.text.index(
            "name: Dispatch deployment workflow in CI repository"
        )
        self.assertLess(
            guard_step,
            dispatch_step,
            "the guard must run before the deploy is dispatched",
        )
        self.assertIn(
            "python3 development/scripts/pr_ci_pr_deploy_target_guard.py",
            self.text[guard_step:dispatch_step],
        )

    def test_guard_step_receives_the_payload_inputs(self):
        guard_step = self.text.index("name: Verify deploy dispatch target (ci#890)")
        dispatch_step = self.text.index(
            "name: Dispatch deployment workflow in CI repository"
        )
        step = self.text[guard_step:dispatch_step]
        # Same job outputs the dispatch payload is built from, so the guard can
        # never disagree with what is about to be dispatched.
        for output in (
            "outputs.deployment_name",
            "outputs.pr_namespace",
            "outputs.pr_deploy_host",
            "outputs.pr_helm_release",
            "outputs.pr_number",
        ):
            self.assertIn(output, step, "missing guard input %s" % output)
        # The closed/merged-PR rule needs the *live* PR state, not the event.
        self.assertIn('PR_STATE="$PR_STATE"', step)

    def test_gate_job_keeps_running_the_guard_tests(self):
        self.assertIn(
            "python3 -m unittest development/scripts/test_pr_ci_pr_deploy_target_guard.py",
            self.text,
        )


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
