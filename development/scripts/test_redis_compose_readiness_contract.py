#!/usr/bin/env python3
"""Regression guard for the ci#722 compose half of the redis probe budget.

The coder-sandbox readiness gate (ci#666) reported the redis Deployment of the CI
deployment with `probe_timeout_too_low`, `probe_budget_too_short`,
`startup_probe_missing` and `redis_cpu_request_missing`. The deployment also runs
through the compose profiles, where the same equivocation applies: a healthcheck
that gives up after seconds of contention marks the container unhealthy, and an
unreserved redis is starved behind the agent workloads on a shared runner, which
takes the queue/state endpoint down and leaves freshly dispatched interactions
dead on arrival.

`development/scripts/redis_readiness_contract.py` pins the Kubernetes half.
These tests pin the compose half:

  * the compose profiles shipped by this repo satisfy the ci#722 numbers;
  * the "before" healthcheck of the shipped profiles reproduces exactly the
    findings the ticket reports;
  * a redis service without a probe/healthcheck is caught, and a `depends_on`
    entry of another service cannot shadow the real redis definition;
  * the CLI exit codes stay usable in a workflow (0 ok, 1 findings, 2 unreadable).

Deliberately stdlib-only: the Pin Freshness Gate CI job runs this file with the
system `python3`, which has no PyYAML.
"""

from __future__ import annotations

import io
import os
import pathlib
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import redis_compose_readiness_contract as contract  # noqa: E402

REPO_ROOT = pathlib.Path(HERE).resolve().parents[1]

#: The "before" healthcheck the ticket reports for the redis container.
BEFORE_HEALTHCHECK = """
services:
  backend:
    image: backend:test
    depends_on:
      redis:
        condition: service_healthy
  redis:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 1s
      retries: 10
"""

#: The "before" healthcheck the ticket reports for the redis container
#: (`probe_timeout_too_low` was also reported live because the probe timed out
#: under contention; the manifest's 3s timeout is what the contract floor
#: rejects).
BEFORE_FINDINGS = (
    contract.TIMEOUT_FINDING,
    contract.BUDGET_FINDING,
    contract.STARTUP_FINDING,
    contract.CPU_FINDING,
)

#: The compose equivalent of `development/redis-readiness/ci-722-redis-probes.patch.json`.
AFTER_HEALTHCHECK = """
services:
  backend:
    image: backend:test
    depends_on:
      redis:
        condition: service_healthy
  redis:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 2s
      retries: 12
      start_period: 5s
    deploy:
      resources:
        limits:
          cpus: "0.5"
        reservations:
          cpus: "0.1"
"""


class ShippedProfilesTest(unittest.TestCase):
    """Every redis-bearing compose profile of this repo meets ci#722."""

    def test_shipped_profiles_satisfy_contract(self):
        for name in contract.DEFAULT_FILES:
            with self.subTest(file=name):
                path = REPO_ROOT / name
                self.assertTrue(path.is_file(), "missing %s" % name)
                service = contract.parse_service(path.read_text(encoding="utf-8"))
                self.assertIsInstance(service, dict, "no redis service in %s" % name)
                self.assertEqual([], contract.evaluate(service))

    def test_shipped_profiles_declare_the_ci722_numbers(self):
        expected = {
            "timeout": 2.0,
            "budget": contract.MIN_SUSTAINED_FAILURE_SECONDS,
            "start_period": contract.MIN_STARTUP_SECONDS,
            "request_milli": contract.MIN_REDIS_CPU_RESERVATION_MILLI,
            "limit_milli": contract.MIN_REDIS_CPU_LIMIT_MILLI,
        }
        for name in contract.DEFAULT_FILES:
            service = contract.parse_service(
                (REPO_ROOT / name).read_text(encoding="utf-8")
            )
            healthcheck = service["healthcheck"]
            resources = service["deploy"]["resources"]
            with self.subTest(file=name):
                self.assertEqual(
                    expected["timeout"],
                    contract.duration_seconds(healthcheck.get("timeout"), None),
                )
                self.assertGreaterEqual(
                    contract.duration_seconds(healthcheck.get("interval"), 0)
                    * contract.retry_count(healthcheck.get("retries")),
                    expected["budget"],
                )
                self.assertGreaterEqual(
                    contract.duration_seconds(healthcheck.get("start_period"), 0),
                    expected["start_period"],
                )
                self.assertGreaterEqual(
                    contract.milli_cpus(resources["reservations"]["cpus"]),
                    expected["request_milli"],
                )
                self.assertGreaterEqual(
                    contract.milli_cpus(resources["limits"]["cpus"]),
                    expected["limit_milli"],
                )


class BeforeAndAfterTest(unittest.TestCase):
    """The reported "before" fails, the emitted contract clears it."""

    def test_before_reproduces_the_reported_findings(self):
        findings = contract.check_text(BEFORE_HEALTHCHECK)
        self.assertEqual(sorted(BEFORE_FINDINGS), sorted(findings))

    def test_after_clears_every_finding(self):
        self.assertEqual([], contract.check_text(AFTER_HEALTHCHECK))

    def test_timeout_below_two_seconds_is_a_finding(self):
        text = AFTER_HEALTHCHECK.replace("timeout: 2s", "timeout: 1s")
        self.assertIn(contract.TIMEOUT_FINDING, contract.check_text(text))

    def test_sixty_second_budget_boundary(self):
        # 12 * 5s = 60s is accepted; 11 * 5s = 55s is not.
        ok = AFTER_HEALTHCHECK.replace("retries: 12", "retries: 12")
        self.assertNotIn(contract.BUDGET_FINDING, contract.check_text(ok))
        short = AFTER_HEALTHCHECK.replace("retries: 12", "retries: 11")
        self.assertIn(contract.BUDGET_FINDING, contract.check_text(short))

    def test_start_period_grace_required(self):
        text = AFTER_HEALTHCHECK.replace("      start_period: 5s\n", "")
        self.assertIn(contract.STARTUP_FINDING, contract.check_text(text))

    def test_reservation_above_limit_is_a_finding(self):
        text = AFTER_HEALTHCHECK.replace('cpus: "0.1"', 'cpus: "1.0"')
        self.assertIn(
            contract.CPU_LIMIT_BELOW_REQUEST_FINDING, contract.check_text(text)
        )

    def test_reservation_below_ci722_number_is_a_finding(self):
        text = AFTER_HEALTHCHECK.replace('cpus: "0.1"', 'cpus: "0.05"')
        self.assertIn(
            contract.CPU_REQUEST_TOO_LOW_FINDING, contract.check_text(text)
        )


class StructureTest(unittest.TestCase):
    """The parser resolves the real service, not a shadowing key."""

    def test_missing_healthcheck_is_reported(self):
        text = "services:\n  redis:\n    image: redis:7-alpine\n"
        self.assertIn(contract.PROBE_MISSING_FINDING, contract.check_text(text))

    def test_missing_service_is_reported(self):
        text = "services:\n  backend:\n    image: backend:test\n"
        self.assertEqual(
            [contract.SERVICE_MISSING_FINDING], contract.check_text(text)
        )

    def test_depends_on_entry_does_not_shadow_the_service(self):
        service = contract.parse_service(BEFORE_HEALTHCHECK)
        self.assertEqual("redis:7-alpine", service.get("image"))
        self.assertEqual(sorted(BEFORE_FINDINGS), sorted(contract.evaluate(service)))

    def test_comments_and_quotes_are_ignored(self):
        text = (
            "# ci#722\nservices:\n  redis:\n    image: redis:7-alpine\n"
            "    healthcheck:\n      test: 'redis-cli ping'\n"
            "      interval: 5s\n      timeout: '2s'\n      retries: 12\n"
            "      start_period: 5s\n    deploy:\n      resources:\n"
            "        limits:\n          cpus: 0.5\n"
            "        reservations:\n          cpus: 100m\n"
        )
        self.assertEqual([], contract.check_text(text))

    def test_absent_healthcheck_fields_use_compose_defaults(self):
        text = (
            "services:\n  redis:\n    image: redis:7-alpine\n"
            "    healthcheck:\n      test: [\"CMD\", \"redis-cli\", \"ping\"]\n"
        )
        findings = contract.check_text(text)
        # compose defaults: interval 30s x retries 3 = 90s budget, timeout 30s,
        # start_period 0 -> only the missing startup grace and CPU reservation.
        self.assertIn(contract.STARTUP_FINDING, findings)
        self.assertIn(contract.CPU_FINDING, findings)
        self.assertNotIn(contract.TIMEOUT_FINDING, findings)
        self.assertNotIn(contract.BUDGET_FINDING, findings)


class CliTest(unittest.TestCase):
    """Exit codes stay usable as a workflow gate."""

    def _run(self, argv):
        buffer = io.StringIO()
        with redirect_stdout(buffer):
            code = contract.main(argv)
        return code, buffer.getvalue()

    def test_ok_profile_exits_zero(self):
        code, out = self._run(["--file", str(REPO_ROOT / "docker-compose.ci.yaml")])
        self.assertEqual(0, code)
        self.assertIn("contract=ok", out)

    def test_violating_profile_exits_one(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "compose.yaml"
            path.write_text(BEFORE_HEALTHCHECK, encoding="utf-8")
            code, out = self._run(["--file", str(path)])
        self.assertEqual(1, code)
        self.assertIn("::error::", out)
        self.assertIn(contract.BUDGET_FINDING, out)

    def test_unreadable_file_exits_two(self):
        code, out = self._run(["--file", "/nonexistent/docker-compose.yaml"])
        self.assertEqual(2, code)
        self.assertIn("::error::", out)


if __name__ == "__main__":
    unittest.main()