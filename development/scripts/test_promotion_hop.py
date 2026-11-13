#!/usr/bin/env python3
"""Unit tests for the ci#1055 promotion-hop predicates.

    python3 -m unittest development/scripts/test_promotion_hop.py -v

Every case is a pure observation document, so the shipped decision logic is
pinned without a token, a cluster or a network call. The regression each case
guards is named in its docstring.
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import promotion_hop as ph  # noqa: E402

GATE = "PR Test Merge Requirement"
SUITE = "Full Suite Gate"

PROMOTION_PR = {
    "title": "[Dev promotion] Promote dev -> main - merging deploys ci.msgmate.io (ci#833)",
    "head_ref": "dev",
    "base_ref": "main",
    "draft": False,
    "mergeable": True,
    "merge_state_status": "CLEAN",
    "head_sha": "89d2d354",
}


def check(name: str, conclusion: str, started_at: str, status: str = "completed", app: str = "github-actions"):
    return {
        "name": name,
        "app": {"slug": app},
        "status": status,
        "conclusion": conclusion,
        "started_at": started_at,
    }


def document(**overrides):
    doc = {
        "pull_request": dict(PROMOTION_PR),
        "required_checks": [GATE, SUITE],
        "check_runs": [
            check(GATE, "success", "2026-11-11T18:40:35Z"),
            check(SUITE, "success", "2026-11-11T18:41:02Z"),
        ],
    }
    doc.update(overrides)
    return doc


class MergeVerdictTests(unittest.TestCase):
    def test_green_promotion_pr_merges(self):
        """The hop's happy path: every required check green on dev -> main."""
        verdict = ph.merge_verdict(document())
        self.assertEqual(verdict.decision, ph.MERGE, verdict.reasons)
        self.assertTrue(any("all 2 required check(s) are green" == r for r in verdict.reasons))
        self.assertTrue(any("89d2d354" in r for r in verdict.reasons))

    def test_superseded_duplicate_check_run_does_not_veto(self):
        """Blocker 2: a cancelled label-event duplicate leaves a stale FAILURE on
        the same SHA while the live run's check of the identical name is green.
        PR #21 at 89d2d354 read BLOCKED only because of that stale instance."""
        doc = document(
            pull_request=dict(PROMOTION_PR, merge_state_status="BLOCKED"),
            check_runs=[
                check(GATE, "failure", "2026-11-11T18:30:45Z"),
                check(GATE, "success", "2026-11-11T18:40:35Z"),
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
            ],
        )
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.MERGE, verdict.reasons)
        self.assertTrue(verdict.ignored, "the superseded instance must be reported")
        self.assertIn("failure", verdict.ignored[0])
        self.assertIn("success", verdict.ignored[0])

    def test_newest_instance_is_the_red_one(self):
        """The collapse must not be a whitewash: a fresh failure is still a veto."""
        doc = document(
            check_runs=[
                check(GATE, "success", "2026-11-11T18:30:45Z"),
                check(GATE, "failure", "2026-11-11T18:40:35Z"),
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
            ]
        )
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.REFUSE, verdict.reasons)
        self.assertTrue(any(repr(GATE) in r for r in verdict.reasons))
        self.assertTrue(verdict.ignored)

    def test_running_replacement_is_a_wait_not_a_red(self):
        """ci#1033: an unfinished newer attempt means pending, never red."""
        doc = document(
            pull_request=dict(PROMOTION_PR, merge_state_status="BLOCKED"),
            check_runs=[
                check(GATE, "cancelled", "2026-11-11T18:30:45Z"),
                check(GATE, "", "2026-11-11T18:40:35Z", status="in_progress"),
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
            ],
        )
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.WAIT, verdict.reasons)

    def test_cancelled_only_check_is_a_wait(self):
        """A cancelled attempt tested nothing; with no replacement the commit is
        simply not mergeable yet, and the reason must say so."""
        doc = document(check_runs=[check(GATE, "cancelled", "2026-11-11T18:30:45Z")])
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.WAIT, verdict.reasons)
        self.assertTrue(any("replacement" in r for r in verdict.reasons))

    def test_missing_required_check_waits(self):
        doc = document(check_runs=[check(GATE, "success", "2026-11-11T18:40:35Z")])
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.WAIT, verdict.reasons)
        self.assertTrue(any(SUITE in r for r in verdict.reasons))

    def test_behind_branch_waits(self):
        """`dev` requires strict status checks: a green but behind promotion PR
        must be refreshed instead of merged."""
        doc = document(pull_request=dict(PROMOTION_PR, merge_state_status="BEHIND"))
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.WAIT, verdict.reasons)

    def test_wrong_direction_refuses(self):
        """Only dev -> main is the promotion hop; the predicate must never be a
        generic 'merge this green PR' switch."""
        doc = document(pull_request=dict(PROMOTION_PR, head_ref="feat/anything"))
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.REFUSE, verdict.reasons)

    def test_draft_refuses(self):
        verdict = ph.merge_verdict(document(pull_request=dict(PROMOTION_PR, draft=True)))
        self.assertEqual(verdict.decision, ph.REFUSE, verdict.reasons)

    def test_conflicts_refuse(self):
        verdict = ph.merge_verdict(
            document(pull_request=dict(PROMOTION_PR, mergeable=False, merge_state_status="DIRTY"))
        )
        self.assertEqual(verdict.decision, ph.REFUSE, verdict.reasons)

    def test_blocked_without_duplicate_is_a_wait_with_an_explanation(self):
        """A BLOCKED read that no duplicate explains needs a human/review reason,
        not a silent merge attempt."""
        doc = document(pull_request=dict(PROMOTION_PR, merge_state_status="BLOCKED"))
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.WAIT, verdict.reasons)
        self.assertTrue(any("BLOCKED" in r for r in verdict.reasons))

    def test_status_context_spelling_is_accepted(self):
        """The statuses API spells the name `context`; a mixed rollup must still
        collapse correctly."""
        doc = document(
            check_runs=[
                {"context": GATE, "state": "failure", "started_at": "2026-11-11T18:30:45Z"},
                {"context": GATE, "state": "success", "started_at": "2026-11-11T18:40:35Z"},
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
            ]
        )
        verdict = ph.merge_verdict(doc)
        self.assertEqual(verdict.decision, ph.MERGE, verdict.reasons)

    def test_empty_document_refuses(self):
        """An unreadable/absent observation must never be read as 'merge': with
        no head/base the predicate cannot confirm the hop and refuses loudly."""
        verdict = ph.merge_verdict({})
        self.assertEqual(verdict.decision, ph.REFUSE, verdict.reasons)

    def test_blocked_duplicate_on_other_check_name_still_merges(self):
        """Only the *required* names gate the hop; a superseded duplicate of an
        unrelated informational check must not create a wait either."""
        doc = document(
            check_runs=[
                check(GATE, "success", "2026-11-11T18:40:35Z"),
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
                check("Private Sandbox Tests", "failure", "2026-11-11T18:42:00Z"),
            ]
        )
        self.assertEqual(ph.merge_verdict(doc).decision, ph.MERGE)


class DeploymentVerdictTests(unittest.TestCase):

    def observed(self, **overrides):
        doc = {
            "rollout_complete": True,
            "rollout_status": 'deployment "open-chat-backend" successfully rolled out',
            "pods_ready": 3,
            "pods_desired": 3,
            "expected_version": "1.4.2",
            "health": {
                "url": "https://ci.msgmate.io/api/v1/metrics/health",
                "status_code": 200,
                "body": '{"status":"ok","version":"1.4.2"}',
            },
        }
        doc.update(overrides)
        return doc

    def test_healthy_deployment(self):
        verdict = ph.deployment_verdict(self.observed())
        self.assertEqual(verdict.decision, ph.HEALTHY, verdict.reasons)
        self.assertTrue(any("3/3 pods ready" in r for r in verdict.reasons))

    def test_unfinished_rollout_is_broken(self):
        verdict = ph.deployment_verdict(self.observed(rollout_complete=False, rollout_status="Waiting for rollout"))
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)

    def test_not_ready_pod_is_broken(self):
        verdict = ph.deployment_verdict(self.observed(pods_ready=2))
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)
        self.assertTrue(any("2 of 3" in r for r in verdict.reasons))

    def test_zero_desired_pods_is_broken(self):
        verdict = ph.deployment_verdict(self.observed(pods_ready=0, pods_desired=0))
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)

    def test_missing_readiness_is_broken(self):
        verdict = ph.deployment_verdict(self.observed(pods_ready=None, pods_desired=None))
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)

    def test_non_200_health_is_broken(self):
        """ci#739: a 403 from the auth-wrapped API looked like a dead host."""
        verdict = ph.deployment_verdict(
            self.observed(health={"status_code": 403, "body": "forbidden"})
        )
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)
        self.assertTrue(any("403" in r for r in verdict.reasons))

    def test_version_mismatch_is_broken(self):
        """ci#890: ci.msgmate.io served a VERSION that only existed on unmerged
        branches. A merge that did not become the served version is not live."""
        verdict = ph.deployment_verdict(
            self.observed(health={"status_code": 200, "body": '{"status":"ok","version":"1.5.0-dev"}'})
        )
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)
        self.assertTrue(any("ci#890" in r for r in verdict.reasons))

    def test_non_ok_status_is_broken(self):
        verdict = ph.deployment_verdict(
            self.observed(health={"status_code": 200, "body": '{"status":"degraded","version":"1.4.2"}'})
        )
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)

    def test_non_json_health_body_is_broken(self):
        verdict = ph.deployment_verdict(self.observed(health={"status_code": 200, "body": "<html>"}))
        self.assertEqual(verdict.decision, ph.BROKEN, verdict.reasons)


class RenderAndCliTests(unittest.TestCase):

    def test_render_names_ignored_instances(self):
        doc = document(
            pull_request=dict(PROMOTION_PR, merge_state_status="BLOCKED"),
            check_runs=[
                check(GATE, "failure", "2026-11-11T18:30:45Z"),
                check(GATE, "success", "2026-11-11T18:40:35Z"),
                check(SUITE, "success", "2026-11-11T18:41:02Z"),
            ],
        )
        text = ph.render(ph.merge_verdict(doc))
        self.assertIn("decision: MERGE", text)
        self.assertIn("ignored superseded check-run instances", text)

    def test_cli_exit_codes_are_the_contract(self):
        """An agent branches on the exit code, so pin it: merge 0, wait 1, refuse 2."""
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "doc.json")
            with open(path, "w", encoding="utf-8") as handle:
                json.dump(document(), handle)
            self.assertEqual(ph.main(["merge", "--observations-json", "@" + path]), 0)

            with open(path, "w", encoding="utf-8") as handle:
                json.dump(document(pull_request=dict(PROMOTION_PR, head_ref="dev", base_ref="production")), handle)
            self.assertEqual(ph.main(["merge", "--observations-json", path]), 2)

        self.assertEqual(ph.EXIT_CODES[ph.WAIT], 1)
        self.assertEqual(ph.EXIT_CODES[ph.BROKEN], 1)


if __name__ == "__main__":
    unittest.main()
