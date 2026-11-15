#!/usr/bin/env python3
"""Guards the ci#1066 promotion-hop readiness predicate.

Reason for the guard: the `dev` -> `main` hop looked healthy for a while *because
the gate was off*. `Main (Staging) Merge Protection` (rulesets `26947244` /
`26947249`) was disabled, so `rules/branches/main` answered `[]`, an armed
`--auto` merge landed unattended and a green promotion PR proved nothing about
the write-access approval the ruleset demands once it is re-enabled. The ticket
is therefore an *ordering* problem:

  1. approver identity (`CONSOLIDATE_APPROVER_TOKEN` with write access) or a
     ruleset `bypass_actors` entry for the automation identity first,
  2. ruleset enforcement `active` second.

`development/scripts/promotion_hop_readiness.py` renders that decision as a pure
predicate over one JSON observation document. This guard pins the verdicts, the
`safe_to_enable` flag (the whole point: it must be `false` while the approver
path is missing, so nobody re-enables the ruleset and re-breaks the hop), the
exit codes and the CLI contract - offline, no token, no network.

Run: python3 -m unittest development/scripts/test_promotion_hop_readiness.py -v
"""

from __future__ import annotations

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = REPO_ROOT / "development" / "scripts" / "promotion_hop_readiness.py"

sys.path.insert(0, str(SCRIPT.parent))

import promotion_hop_readiness as readiness  # noqa: E402


def observation(**overrides):
    """A ruleset that enforces one write-access approval on `main`, no approver."""
    doc = {
        "ruleset": {
            "name": "Main (Staging) Merge Protection",
            "enforcement": "active",
            "covers_main": True,
            "required_approving_reviews": 1,
            "bypass_actors": [],
        },
        "approver": {
            "configured": False,
            "login": "",
            "resolved": False,
            "write_access": None,
        },
        "automation": {"login": "cur1ousdude", "app_slug": "github-actions"},
    }
    for key, value in overrides.items():
        if isinstance(value, dict) and isinstance(doc.get(key), dict):
            doc[key] = {**doc[key], **value}
        else:
            doc[key] = value
    return doc


class ApproverReadinessTest(unittest.TestCase):
    def test_unset_token_is_not_ready(self):
        self.assertFalse(readiness.approver_ready(observation()["approver"]))

    def test_unresolvable_token_is_not_ready(self):
        approver = {"configured": True, "resolved": False, "write_access": True}
        self.assertFalse(readiness.approver_ready(approver))

    def test_read_only_token_is_not_ready(self):
        approver = {"configured": True, "resolved": True, "write_access": False}
        self.assertFalse(readiness.approver_ready(approver))

    def test_unknown_permissions_are_not_ready(self):
        # An installation token that cannot report repository permissions must not
        # be assumed write-capable: the check exists to prove the approval lands.
        approver = {"configured": True, "resolved": True, "write_access": None}
        self.assertFalse(readiness.approver_ready(approver))

    def test_write_access_token_is_ready(self):
        approver = {"configured": True, "resolved": True, "write_access": True}
        self.assertTrue(readiness.approver_ready(approver))


class BypassReadinessTest(unittest.TestCase):
    def test_no_bypass_actors(self):
        self.assertFalse(readiness.bypass_ready(observation()["ruleset"], {"login": "bot"}))

    def test_integration_bypass_actor_covers_automation(self):
        ruleset = {"bypass_actors": [{"actor_type": "Integration", "actor_id": 42}]}
        self.assertTrue(readiness.bypass_ready(ruleset, {"app_slug": "github-actions"}))

    def test_unknown_actor_type_is_ignored(self):
        ruleset = {"bypass_actors": [{"actor_type": "DeployKey", "actor_id": 1}]}
        self.assertFalse(readiness.bypass_ready(ruleset, {"login": "bot"}))

    def test_bypass_without_a_known_identity_is_not_claimed(self):
        ruleset = {"bypass_actors": [{"actor_type": "Integration", "actor_id": 42}]}
        self.assertFalse(readiness.bypass_ready(ruleset, {}))


class ReviewRequirementTest(unittest.TestCase):
    def test_active_ruleset_with_reviews_enforces(self):
        self.assertTrue(readiness.review_required(observation()["ruleset"]))

    def test_disabled_ruleset_does_not_enforce(self):
        ruleset = observation(ruleset={"enforcement": "disabled"})["ruleset"]
        self.assertFalse(readiness.review_required(ruleset))

    def test_evaluate_enforcement_does_not_enforce(self):
        ruleset = observation(ruleset={"enforcement": "evaluate"})["ruleset"]
        self.assertFalse(readiness.review_required(ruleset))

    def test_ruleset_not_covering_main_does_not_enforce(self):
        ruleset = observation(ruleset={"covers_main": False})["ruleset"]
        self.assertFalse(readiness.review_required(ruleset))

    def test_zero_required_reviews_does_not_enforce(self):
        ruleset = observation(ruleset={"required_approving_reviews": 0})["ruleset"]
        self.assertFalse(readiness.review_required(ruleset))


class VerdictTest(unittest.TestCase):
    def test_enforced_without_approver_needs_configuration(self):
        verdict = readiness.evaluate(observation())
        self.assertEqual(verdict["verdict"], readiness.NEEDS_APPROVER)
        self.assertEqual(verdict["exit_code"], readiness.EXIT_NEEDS_CONFIG)
        self.assertFalse(verdict["safe_to_enable"])
        self.assertIn("CONSOLIDATE_APPROVER_TOKEN", " ".join(verdict["remediation"]))
        self.assertIn("bypass_actors", " ".join(verdict["remediation"]))

    def test_enforced_with_write_access_approver_is_ready(self):
        verdict = readiness.evaluate(
            observation(approver={"configured": True, "resolved": True, "write_access": True})
        )
        self.assertEqual(verdict["verdict"], readiness.READY_APPROVER)
        self.assertEqual(verdict["exit_code"], readiness.EXIT_READY)
        self.assertTrue(verdict["safe_to_enable"])
        self.assertEqual(verdict["remediation"], [])

    def test_enforced_with_bypass_actor_is_ready(self):
        verdict = readiness.evaluate(
            observation(ruleset={"bypass_actors": [{"actor_type": "Integration", "actor_id": 7}]})
        )
        self.assertEqual(verdict["verdict"], readiness.READY_BYPASS)
        self.assertEqual(verdict["exit_code"], readiness.EXIT_READY)
        self.assertTrue(verdict["safe_to_enable"])

    def test_masked_ruleset_is_reported_and_not_safe_to_enable(self):
        # The live state of ci#1066: `rules/branches/main` = [] and no approver.
        verdict = readiness.evaluate(
            observation(
                ruleset={
                    "enforcement": "disabled",
                    "covers_main": False,
                    "required_approving_reviews": 0,
                }
            )
        )
        self.assertEqual(verdict["verdict"], readiness.MASKED)
        self.assertEqual(verdict["exit_code"], readiness.EXIT_MASKED)
        self.assertFalse(verdict["safe_to_enable"])
        self.assertIn("proves nothing", verdict["reason"])
        self.assertTrue(verdict["remediation"])

    def test_masked_ruleset_with_approver_is_safe_to_enable(self):
        verdict = readiness.evaluate(
            observation(
                ruleset={"enforcement": "disabled", "covers_main": False},
                approver={"configured": True, "resolved": True, "write_access": True},
            )
        )
        self.assertEqual(verdict["verdict"], readiness.MASKED)
        self.assertTrue(verdict["safe_to_enable"])
        self.assertEqual(verdict["remediation"], [])

    def test_missing_ruleset_observation_is_unknown(self):
        verdict = readiness.evaluate({})
        self.assertEqual(verdict["verdict"], readiness.UNKNOWN)
        self.assertEqual(verdict["exit_code"], readiness.EXIT_UNKNOWN)
        self.assertFalse(verdict["safe_to_enable"])

    def test_garbage_values_do_not_raise(self):
        verdict = readiness.evaluate(
            {"ruleset": "nope", "approver": [1, 2], "automation": None}
        )
        self.assertEqual(verdict["verdict"], readiness.UNKNOWN)

    def test_render_mentions_the_verdict_and_the_enable_decision(self):
        text = readiness.render(readiness.evaluate(observation()))
        self.assertIn("needs_approver", text)
        self.assertIn("safe to set the `main` ruleset to `active`: no", text)


class CliTest(unittest.TestCase):
    def _run(self, payload, *args):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as handle:
            handle.write(payload)
            path = handle.name
        try:
            return subprocess.run(
                [sys.executable, str(SCRIPT), path, *args],
                capture_output=True,
                text=True,
                check=False,
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)

    def test_exit_code_needs_configuration(self):
        result = self._run(json.dumps(observation()))
        self.assertEqual(result.returncode, readiness.EXIT_NEEDS_CONFIG, result.stderr)
        self.assertIn("needs_approver", result.stdout)

    def test_exit_code_ready(self):
        payload = observation(
            approver={"configured": True, "resolved": True, "write_access": True}
        )
        result = self._run(json.dumps(payload))
        self.assertEqual(result.returncode, readiness.EXIT_READY, result.stderr)

    def test_exit_code_masked(self):
        payload = observation(ruleset={"enforcement": "disabled", "covers_main": False})
        result = self._run(json.dumps(payload))
        self.assertEqual(result.returncode, readiness.EXIT_MASKED, result.stderr)

    def test_json_output_is_machine_readable(self):
        result = self._run(json.dumps(observation()), "--json")
        verdict = json.loads(result.stdout)
        self.assertEqual(verdict["verdict"], readiness.NEEDS_APPROVER)
        self.assertIn("safe_to_enable", verdict)

    def test_unreadable_document_is_unknown_not_a_crash(self):
        result = self._run("{not json")
        self.assertEqual(result.returncode, readiness.EXIT_UNKNOWN, result.stderr)
        self.assertIn("unknown", result.stdout)


class LiveObservationTest(unittest.TestCase):
    """The live translation: API payloads -> observation document -> verdict.

    The masked state of ci#1066 was invisible because the *effective* rules answer
    and the *ruleset* resource were read in different places. These cases pin the
    translation that joins them, offline, against a stubbed `gh`.
    """

    def _run(self, responses):
        calls = []

        def run(args):
            calls.append(list(args))
            key = " ".join(args)
            for pattern, answer in responses.items():
                if key == pattern:
                    return answer
            return 1, ""

        return run, calls

    def test_disabled_ruleset_is_masked_and_not_safe_to_enable(self):
        run, calls = self._run(
            {
                "api repos/o/r/rules/branches/main": (0, "[]"),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 26947244}])),
                "api repos/o/r/rulesets/26947244": (
                    0,
                    json.dumps(
                        {
                            "id": 26947244,
                            "name": "Main (Staging) Merge Protection",
                            "target": "branch",
                            "enforcement": "disabled",
                            "bypass_actors": [],
                            "conditions": {"ref_name": {"include": ["refs/heads/main"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (0, json.dumps({"login": "cur1ousdude"})),
                "api repos/o/r": (0, json.dumps({"permissions": {"push": False}})),
            }
        )
        doc = readiness.collect_observation("o/r", run=run, approver_configured=False)
        verdict = readiness.evaluate(doc)
        self.assertEqual(verdict["verdict"], readiness.MASKED)
        self.assertFalse(verdict["safe_to_enable"])
        self.assertEqual(doc["ruleset"]["name"], "Main (Staging) Merge Protection")
        self.assertEqual(doc["approver"]["login"], "cur1ousdude")
        self.assertFalse(doc["approver"]["write_access"])
        self.assertTrue(any("rules/branches/main" in " ".join(c) for c in calls))

    def test_active_ruleset_without_approver_needs_configuration(self):
        run, _ = self._run(
            {
                "api repos/o/r/rules/branches/main": (
                    0,
                    json.dumps(
                        [
                            {
                                "type": "pull_request",
                                "parameters": {"required_approving_review_count": 1},
                            }
                        ]
                    ),
                ),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 26947244}])),
                "api repos/o/r/rulesets/26947244": (
                    0,
                    json.dumps(
                        {
                            "id": 26947244,
                            "name": "Main (Staging) Merge Protection",
                            "target": "branch",
                            "enforcement": "active",
                            "bypass_actors": [],
                            "conditions": {"ref_name": {"include": ["refs/heads/main"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (1, ""),
            }
        )
        verdict = readiness.evaluate(
            readiness.collect_observation("o/r", run=run, approver_configured=False)
        )
        self.assertEqual(verdict["verdict"], readiness.NEEDS_APPROVER)
        self.assertFalse(verdict["safe_to_enable"])

    def test_active_ruleset_with_write_access_approver_is_ready(self):
        run, _ = self._run(
            {
                "api repos/o/r/rules/branches/main": (
                    0,
                    json.dumps(
                        [
                            {
                                "type": "pull_request",
                                "parameters": {"required_approving_review_count": 1},
                            }
                        ]
                    ),
                ),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 26947244}])),
                "api repos/o/r/rulesets/26947244": (
                    0,
                    json.dumps(
                        {
                            "id": 26947244,
                            "name": "Main (Staging) Merge Protection",
                            "target": "branch",
                            "enforcement": "active",
                            "bypass_actors": [],
                            "conditions": {"ref_name": {"include": ["refs/heads/main"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (0, json.dumps({"login": "maintainer"})),
                "api repos/o/r": (0, json.dumps({"permissions": {"push": True}})),
            }
        )
        verdict = readiness.evaluate(
            readiness.collect_observation("o/r", run=run, approver_configured=True)
        )
        self.assertEqual(verdict["verdict"], readiness.READY_APPROVER)
        self.assertTrue(verdict["safe_to_enable"])

    def test_a_bypass_actor_on_the_ruleset_is_ready(self):
        run, _ = self._run(
            {
                "api repos/o/r/rules/branches/main": (
                    0,
                    json.dumps(
                        [
                            {
                                "type": "pull_request",
                                "parameters": {"required_approving_review_count": 1},
                            }
                        ]
                    ),
                ),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 26947244}])),
                "api repos/o/r/rulesets/26947244": (
                    0,
                    json.dumps(
                        {
                            "id": 26947244,
                            "name": "Main (Staging) Merge Protection",
                            "target": "branch",
                            "enforcement": "active",
                            "bypass_actors": [
                                {"actor_id": 7, "actor_type": "Integration", "bypass_mode": "always"}
                            ],
                            "conditions": {"ref_name": {"include": ["refs/heads/main"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (0, json.dumps({"login": "cur1ousdude"})),
                "api repos/o/r": (0, json.dumps({"permissions": {"push": False}})),
            }
        )
        verdict = readiness.evaluate(
            readiness.collect_observation("o/r", run=run, approver_configured=False)
        )
        self.assertEqual(verdict["verdict"], readiness.READY_BYPASS)

    def test_an_active_ruleset_that_does_not_apply_is_reported_as_masked(self):
        # The ruleset says `active` but `rules/branches/main` answers `[]`: the
        # effective answer wins, otherwise the masked state would look healthy.
        run, _ = self._run(
            {
                "api repos/o/r/rules/branches/main": (0, "[]"),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 26947244}])),
                "api repos/o/r/rulesets/26947244": (
                    0,
                    json.dumps(
                        {
                            "id": 26947244,
                            "name": "Main (Staging) Merge Protection",
                            "target": "branch",
                            "enforcement": "active",
                            "bypass_actors": [],
                            "conditions": {"ref_name": {"include": ["refs/heads/main"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (1, ""),
            }
        )
        doc = readiness.collect_observation("o/r", run=run, approver_configured=False)
        self.assertEqual(doc["ruleset"]["enforcement"], "disabled")
        self.assertEqual(readiness.evaluate(doc)["verdict"], readiness.MASKED)

    def test_a_tag_ruleset_is_ignored(self):
        run, _ = self._run(
            {
                "api repos/o/r/rules/branches/main": (0, "[]"),
                "api repos/o/r/rulesets": (0, json.dumps([{"id": 99}])),
                "api repos/o/r/rulesets/99": (
                    0,
                    json.dumps(
                        {
                            "id": 99,
                            "name": "Tag protection",
                            "target": "tag",
                            "enforcement": "active",
                            "conditions": {"ref_name": {"include": ["refs/tags/v*"]}},
                            "rules": [
                                {
                                    "type": "pull_request",
                                    "parameters": {"required_approving_review_count": 1},
                                }
                            ],
                        }
                    ),
                ),
                "api user": (1, ""),
            }
        )
        doc = readiness.collect_observation("o/r", run=run, approver_configured=False)
        self.assertEqual(doc["ruleset"]["required_approving_reviews"], 0)
        self.assertEqual(readiness.evaluate(doc)["verdict"], readiness.MASKED)

    def test_an_unreadable_api_is_unknown_never_masked(self):
        run, _ = self._run({})
        doc = readiness.collect_observation("o/r", run=run, approver_configured=False)
        self.assertEqual(doc, {})
        self.assertEqual(readiness.evaluate(doc)["verdict"], readiness.UNKNOWN)

    def test_a_missing_repo_is_unknown(self):
        run, calls = self._run({})
        self.assertEqual(readiness.collect_observation("", run=run), {})
        self.assertEqual(calls, [])

    def test_the_live_cli_never_renders_a_token(self):
        # `--live` with no reachable API must degrade to `unknown`, not crash and
        # not echo the credential.
        env = dict(os.environ)
        env.update(
            {
                "PATH": os.pathsep.join(["/nonexistent-bin", env.get("PATH", "")]),
                "CONSOLIDATE_APPROVER_TOKEN": "ghp_fakeTokenThatMustNeverBeRendered0123456789",
                "GITHUB_REPOSITORY": "o/r",
            }
        )
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--live"],
            capture_output=True,
            text=True,
            check=False,
            env=env,
        )
        self.assertEqual(result.returncode, readiness.EXIT_UNKNOWN, result.stderr)
        self.assertIn("unknown", result.stdout)
        self.assertNotIn("ghp_fakeTokenThatMustNeverBeRendered0123456789", result.stdout)


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
