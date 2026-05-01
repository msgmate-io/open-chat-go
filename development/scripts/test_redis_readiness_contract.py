#!/usr/bin/env python3
"""Regression guard for ci#722 (redis probe budget / CPU guarantee).

The coder-sandbox readiness gate (ci#666) flagged the redis Deployment of the CI
deployment with `probe_timeout_too_low`, `probe_budget_too_short`,
`startup_probe_missing` and `redis_cpu_request_missing`: a redis pod without a
probe budget and without a CPU request can be evicted or fail to schedule under
load, which takes the queue/state endpoint down and leaves freshly dispatched
interactions dead on arrival.

The gate is read-only and hands the operator a strategic-merge patch. These
tests pin the repo-side half of that contract:

  * the "before" manifest of the ticket reproduces exactly the reported findings
    in the reported order;
  * applying the emitted ci#722 patch to it clears every finding (the
    "after the emitted patch: all contract checks ok" claim of the gate);
  * the checked-in patch artifact cannot drift from the code that emits it;
  * the CLI exit codes stay usable in a workflow (0 ok, 1 findings, 2 missing
    container).

Deliberately stdlib-only: the Pin Freshness Gate CI job runs this file with the
system `python3`, before the build-tools venv is guaranteed.
"""

from __future__ import annotations

import io
import json
import os
import pathlib
import sys
import unittest
from contextlib import redirect_stdout

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import redis_readiness_contract as contract  # noqa: E402

PATCH_ARTIFACT = (
    pathlib.Path(HERE).resolve().parents[0]
    / "redis-readiness"
    / "ci-722-redis-probes.patch.json"
)


def deployment(container_overrides=None, container_name=contract.CONTAINER_NAME):
    """The redis Deployment as the gate found it before the ci#722 patch."""
    container = {
        "name": container_name,
        "image": "redis:7-alpine",
        "livenessProbe": {
            "exec": {"command": ["redis-cli", "ping"]},
            "periodSeconds": 5,
            "timeoutSeconds": 1,
        },
        "readinessProbe": {
            "exec": {"command": ["redis-cli", "ping"]},
            "periodSeconds": 10,
            "failureThreshold": 3,
            "timeoutSeconds": 1,
        },
        "resources": {"limits": {"memory": "256Mi"}},
    }
    if container_overrides:
        container.update(container_overrides)
    return {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": "redis-container", "namespace": "msgmate-io-ci"},
        "spec": {
            "template": {
                "spec": {
                    "containers": [container, {"name": "metrics-sidecar"}],
                }
            }
        },
    }


def run_main(argv):
    """Run main(), returning (exit code, stdout)."""
    buffer = io.StringIO()
    with redirect_stdout(buffer):
        code = contract.main(argv)
    return code, buffer.getvalue()


class FindingsTests(unittest.TestCase):
    def test_reproduces_the_ticket_findings_in_order(self):
        container = contract.find_container(deployment())
        self.assertIsNotNone(container)
        self.assertEqual(
            contract.evaluate(container),
            [
                "probe_timeout_too_low",
                "probe_budget_too_short",
                "probe_timeout_too_low",
                "startup_probe_missing",
                "redis_cpu_request_missing",
            ],
        )

    def test_healthy_container_has_no_findings(self):
        healthy = contract.apply_patch(
            deployment(), contract.build_patch()
        )
        container = contract.find_container(healthy)
        self.assertEqual(contract.evaluate(container), [])

    def test_absent_probe_timeout_uses_the_kubernetes_default(self):
        # No timeoutSeconds means the 1s Kubernetes default, which is already
        # below the 2s contract, even with a sufficient failure budget.
        container = {
            "name": contract.CONTAINER_NAME,
            "livenessProbe": {"periodSeconds": 10, "failureThreshold": 5},
            "readinessProbe": {"periodSeconds": 10, "failureThreshold": 5},
            "startupProbe": {
                "periodSeconds": 5,
                "failureThreshold": 12,
                "initialDelaySeconds": 0,
            },
            "resources": {"requests": {"cpu": "100m"}},
        }
        self.assertEqual(
            contract.evaluate(container),
            ["probe_timeout_too_low", "probe_timeout_too_low", "probe_timeout_too_low"],
        )

    def test_sufficient_budget_but_low_timeout_is_reported(self):
        overrides = {
            "livenessProbe": {"periodSeconds": 10, "failureThreshold": 5, "timeoutSeconds": 1},
            "readinessProbe": {"periodSeconds": 10, "failureThreshold": 5, "timeoutSeconds": 2},
            "startupProbe": {
                "periodSeconds": 5,
                "failureThreshold": 12,
                "initialDelaySeconds": 0,
                "timeoutSeconds": 2,
                "tcpSocket": {"port": contract.REDIS_PORT},
            },
            "resources": {"requests": {"cpu": "100m"}},
        }
        findings = contract.evaluate(
            contract.find_container(deployment(overrides))
        )
        self.assertEqual(findings, ["probe_timeout_too_low"])

    def test_startup_probe_budget_is_tighter_than_the_others(self):
        # 3 * 10s = 30s satisfies liveness/readiness but not the 60s startup
        # budget, so a short startup probe is still reported.
        overrides = {
            "livenessProbe": {"periodSeconds": 10, "failureThreshold": 3, "timeoutSeconds": 2},
            "readinessProbe": {"periodSeconds": 10, "failureThreshold": 3, "timeoutSeconds": 2},
            "startupProbe": {
                "periodSeconds": 10,
                "failureThreshold": 3,
                "initialDelaySeconds": 0,
                "timeoutSeconds": 2,
                "tcpSocket": {"port": contract.REDIS_PORT},
            },
            "resources": {"requests": {"cpu": "100m"}},
        }
        findings = contract.evaluate(
            contract.find_container(deployment(overrides))
        )
        self.assertEqual(findings, ["probe_budget_too_short"])

    def test_startup_probe_needs_a_zero_initial_delay(self):
        overrides = {
            "livenessProbe": {"periodSeconds": 10, "failureThreshold": 3, "timeoutSeconds": 2},
            "readinessProbe": {"periodSeconds": 10, "failureThreshold": 3, "timeoutSeconds": 2},
            "startupProbe": {
                "periodSeconds": 5,
                "failureThreshold": 12,
                "initialDelaySeconds": 30,
                "timeoutSeconds": 2,
                "tcpSocket": {"port": contract.REDIS_PORT},
            },
            "resources": {"requests": {"cpu": "100m"}},
        }
        findings = contract.evaluate(
            contract.find_container(deployment(overrides))
        )
        self.assertEqual(findings, ["probe_budget_too_short"])

    def test_cpu_limit_alone_is_not_a_guarantee(self):
        container = contract.find_container(
            deployment({"resources": {"limits": {"cpu": "500m"}}})
        )
        self.assertIn("redis_cpu_request_missing", contract.evaluate(container))

    def test_missing_container_is_detected(self):
        self.assertIsNone(contract.find_container(deployment(), "not-redis"))


class PatchTests(unittest.TestCase):
    def test_patch_merges_by_container_name_without_dropping_siblings(self):
        merged = contract.apply_patch(deployment(), contract.build_patch())
        containers = merged["spec"]["template"]["spec"]["containers"]
        self.assertEqual([c["name"] for c in containers], [contract.CONTAINER_NAME, "metrics-sidecar"])
        redis = containers[0]
        self.assertEqual(redis["image"], "redis:7-alpine")
        self.assertEqual(redis["startupProbe"]["tcpSocket"]["port"], contract.REDIS_PORT)
        self.assertEqual(redis["resources"]["limits"], {"memory": "256Mi", "cpu": "500m"})

    def test_patch_does_not_mutate_its_inputs(self):
        manifest = deployment()
        before = json.dumps(manifest, sort_keys=True)
        contract.apply_patch(manifest, contract.build_patch())
        self.assertEqual(json.dumps(manifest, sort_keys=True), before)

    def test_checked_in_patch_matches_the_emitted_patch(self):
        self.assertTrue(PATCH_ARTIFACT.exists(), PATCH_ARTIFACT)
        self.assertEqual(
            json.loads(PATCH_ARTIFACT.read_text(encoding="utf-8")),
            contract.build_patch(),
        )

    def test_print_patch_round_trips_through_json(self):
        code, output = run_main(["--print-patch"])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output), contract.build_patch())


class CliTests(unittest.TestCase):
    def test_manifest_with_findings_exits_one(self):
        code, output = run_main(["--deployment-json", json.dumps(deployment())])
        self.assertEqual(code, 1)
        self.assertIn("redis_readiness findings=[", output)
        self.assertIn("startup_probe_missing", output)

    def test_remediated_manifest_exits_zero(self):
        remediated = contract.apply_patch(deployment(), contract.build_patch())
        code, output = run_main(["--deployment-json", json.dumps(remediated)])
        self.assertEqual(code, 0)
        self.assertIn("findings=[]", output)
        self.assertIn("contract=ok", output)

    def test_deployment_without_the_redis_container_exits_one(self):
        code, output = run_main(
            ["--deployment-json", json.dumps(deployment(container_name="other"))]
        )
        self.assertEqual(code, 1)
        self.assertIn("redis_container_missing", output)


if __name__ == "__main__":
    unittest.main()
