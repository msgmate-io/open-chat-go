#!/usr/bin/env python3
"""Regression guard for the chart-side half of ci#722 (redis probe budget).

The readiness gate (ci#666) keeps reporting the CI ``redis-container``
Deployment with ``probe_timeout_too_low``, ``probe_budget_too_short``,
``startup_probe_missing`` and ``redis_cpu_request_missing``. The gate patches the
*live* object (read-only, operator-applied), but the Deployment is re-rendered on
every deploy from the private Helm chart - so the durable fix has to be in the
chart template (``backend/templates/redis.yaml``), which still renders the
kubelet probe defaults and no CPU request.

These tests pin:

  * the chart template *as it is today* (fixture
    ``development/redis-readiness/chart-redis-template.before.yaml``) renders
    exactly the reported findings - i.e. the chart is the root cause of the
    recurring re-failure, not just the live object;
  * the patched template (fixture ``...after.yaml``, the file the checked-in
    ``ci-722-chart.patch`` produces) satisfies the *live* contract
    (``redis_readiness_contract.evaluate``) with no findings;
  * the chart half renders exactly the probe/CPU state the gate's own ci#722
    patch writes, so the two halves cannot drift apart;
  * the Go-template renderer (``if``/``else``/``end``, inline values,
    ``| default``) behaves, including its error cases.

Deliberately stdlib-only: the Pin Freshness Gate CI job runs this file with the
system ``python3``, before the build-tools venv is guaranteed.
"""

from __future__ import annotations

import difflib
import io
import os
import pathlib
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import redis_chart_readiness_contract as chart  # noqa: E402
import redis_readiness_contract as contract  # noqa: E402

FIXTURES = pathlib.Path(HERE).resolve().parents[0] / "redis-readiness"
BEFORE = FIXTURES / "chart-redis-template.before.yaml"
AFTER = FIXTURES / "chart-redis-template.after.yaml"
CHART_PATCH = FIXTURES / "ci-722-chart.patch"

STARTUP_MISSING = contract.PROBE_CONTRACT[2][1]


def run_main(argv):
    buffer = io.StringIO()
    with redirect_stdout(buffer):
        code = chart.main(argv)
    return code, buffer.getvalue()


class RenderTests(unittest.TestCase):
    def test_disabled_persistence_blocks_are_dropped(self):
        rendered = chart.render_chart_template(BEFORE.read_text(encoding="utf-8"))
        self.assertIn("kind: Deployment", rendered)
        self.assertNotIn("volumeMounts", rendered)
        self.assertNotIn("redis-data", rendered)

    def test_enabling_persistence_renders_the_volume_and_the_default_claim(self):
        values = dict(chart.CHART_VALUES, **{"redis.persistence.enabled": True})
        rendered = chart.render_chart_template(BEFORE.read_text(encoding="utf-8"), values)
        self.assertIn("volumeMounts", rendered)
        self.assertIn("mountPath: /data", rendered)
        self.assertIn("claimName: backend-redis-data", rendered)

    def test_values_are_substituted_into_the_container(self):
        container = chart.load_chart_container(BEFORE)
        self.assertEqual(container["name"], "redis-container")
        self.assertEqual(container["image"], "redis:7-alpine")
        self.assertEqual(container["imagePullPolicy"], "IfNotPresent")
        self.assertEqual(container["ports"], [{"containerPort": contract.REDIS_PORT}])
        self.assertEqual(container["args"][0], "redis-server")

    def test_unresolved_expression_is_an_error(self):
        with self.assertRaises(chart.ChartParseError):
            chart.render_chart_template("x: {{ .Values.not.in.the.chart }}\n")

    def test_unbalanced_control_flow_is_an_error(self):
        with self.assertRaises(chart.ChartParseError):
            chart.render_chart_template("{{- if .Values.redis.enabled }}\nx: 1\n")
        with self.assertRaises(chart.ChartParseError):
            chart.render_chart_template("{{- end }}\n")

    def test_a_template_without_the_redis_container_is_an_error(self):
        with self.assertRaises(chart.ChartParseError):
            chart.parse_chart_template("apiVersion: v1\nkind: Service\n")


class ContractTests(unittest.TestCase):
    def test_before_fixture_reproduces_the_ticket_findings(self):
        container = chart.load_chart_container(BEFORE)
        self.assertEqual(
            chart.check_container(container),
            [
                contract.TIMEOUT_FINDING,
                contract.TIMEOUT_FINDING,
                contract.BUDGET_FINDING,
                STARTUP_MISSING,
                contract.CPU_FINDING,
            ],
        )

    def test_after_fixture_satisfies_the_live_contract(self):
        container = chart.load_chart_container(AFTER)
        self.assertEqual(chart.check_container(container), [])
        self.assertEqual(contract.evaluate(container), [])

    def test_the_live_ci722_patch_clears_the_chart_rendered_container(self):
        """The chart half and the live half must agree on the same target state."""
        deployment = {
            "spec": {
                "template": {
                    "spec": {"containers": [chart.load_chart_container(BEFORE)]},
                }
            }
        }
        patched = contract.apply_patch(deployment, contract.build_patch())
        patched_container = patched["spec"]["template"]["spec"]["containers"][0]
        self.assertEqual(contract.evaluate(patched_container), [])

        after = chart.load_chart_container(AFTER)
        for probe_key in ("livenessProbe", "readinessProbe", "startupProbe"):
            self.assertEqual(after[probe_key], patched_container[probe_key])
        self.assertEqual(after["resources"]["requests"], {"cpu": "100m"})
        self.assertEqual(after["resources"]["limits"], {"cpu": "500m"})

    def test_probe_on_another_port_is_reported(self):
        container = chart.load_chart_container(AFTER)
        container["readinessProbe"]["tcpSocket"]["port"] = 1234
        self.assertIn(chart.PROBE_PORT_FINDING, chart.check_container(container))

    def test_checked_in_chart_patch_is_exactly_the_fixture_diff(self):
        self.assertTrue(CHART_PATCH.exists(), CHART_PATCH)
        text = CHART_PATCH.read_text(encoding="utf-8")
        before = BEFORE.read_text(encoding="utf-8")
        after = AFTER.read_text(encoding="utf-8")
        self.assertNotEqual(before, after)

        expected = (
            "diff --git a/backend/templates/redis.yaml b/backend/templates/redis.yaml\n"
            + "".join(
                difflib.unified_diff(
                    before.splitlines(keepends=True),
                    after.splitlines(keepends=True),
                    "a/backend/templates/redis.yaml",
                    "b/backend/templates/redis.yaml",
                )
            )
        )
        self.assertEqual(text, expected, "the checked-in patch must be the fixture diff")

        added = [l[1:] for l in text.splitlines() if l.startswith("+") and not l.startswith("+++")]
        for line in added:
            self.assertIn(line, after)
        for field in ("timeoutSeconds", "failureThreshold", "startupProbe", "cpu"):
            self.assertTrue(
                any(field in line for line in added),
                "the chart patch must add %s" % field,
            )
        self.assertNotIn("startupProbe", before)
        self.assertIn("startupProbe", after)


class CliTests(unittest.TestCase):
    def test_before_fixture_exits_one(self):
        code, output = run_main(["--chart-file", str(BEFORE)])
        self.assertEqual(code, 1)
        self.assertIn("contract=failed", output)
        self.assertIn(contract.CPU_FINDING, output)

    def test_after_fixture_exits_zero(self):
        code, output = run_main(["--chart-file", str(AFTER)])
        self.assertEqual(code, 0)
        self.assertIn("findings=[]", output)
        self.assertIn("contract=ok", output)

    def test_chart_dir_resolves_the_template(self):
        with tempfile.TemporaryDirectory() as tmp:
            template_dir = pathlib.Path(tmp) / "backend" / "templates"
            template_dir.mkdir(parents=True)
            (template_dir / "redis.yaml").write_text(AFTER.read_text(encoding="utf-8"))
            code, output = run_main(["--chart-dir", tmp])
        self.assertEqual(code, 0, output)
        self.assertIn("backend/templates/redis.yaml", output)

    def test_missing_file_exits_two(self):
        code, output = run_main(["--chart-file", str(FIXTURES / "does-not-exist.yaml")])
        self.assertEqual(code, 2)
        self.assertIn("error=", output)

    def test_no_input_exits_two(self):
        code, output = run_main([])
        self.assertEqual(code, 2)
        self.assertIn("error=", output)


if __name__ == "__main__":
    unittest.main()
