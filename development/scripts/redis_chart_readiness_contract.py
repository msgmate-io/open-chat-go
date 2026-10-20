#!/usr/bin/env python3
"""ci#722 - chart-side half of the redis probe budget / CPU guarantee.

The coder-sandbox readiness gate (ci#666) reports the CI ``redis-container``
Deployment with ``probe_timeout_too_low``, ``probe_budget_too_short``,
``startup_probe_missing`` and ``redis_cpu_request_missing``. The gate is
read-only: it derives a strategic-merge patch for the live object and never
applies it. But that Deployment is re-rendered on every deploy from the private
Helm chart (``msgmate-io/open-chat-go-helm-chart``,
``backend/templates/redis.yaml``), and that template still renders the kubelet
probe defaults (no ``timeoutSeconds``, no ``failureThreshold``, no
``startupProbe``) plus no CPU request - which is exactly the finding set. So a
live patch is undone by the next ``helm upgrade`` and redis can be evicted from
``redis-service`` again under CPU saturation (ci#638: the queue/state endpoint
goes away and freshly dispatched interactions are dead on arrival).

This script checks the chart template itself against the same contract as
``redis_readiness_contract.py`` (the live-Deployment checker):

  * it renders the Go-template subset the chart's redis template uses
    (``if`` / ``else`` / ``end`` on ``.Values`` and inline ``{{ .Values.* }}``
    substitutions, including the ``| default`` filter) with the chart's own
    redis values,
  * extracts the ``redis-container`` container, and
  * evaluates the ci#638/ci#722 probe + CPU contract on it, so "the chart
    renders a container the readiness gate would accept" is checkable in CI,
    before any deploy.

The chart repository grants the automation identity no write access, so the
durable record of the chart change is carried in
``development/redis-readiness/ci-722-chart.patch`` together with the rendered
before/after fixtures this checker is pinned against
(``development/scripts/test_redis_chart_readiness_contract.py``).

Usage:
  development/scripts/redis_chart_readiness_contract.py \\
      --chart-file development/redis-readiness/chart-redis-template.after.yaml
  development/scripts/redis_chart_readiness_contract.py --chart-dir /path/to/open-chat-go-helm-chart
  development/scripts/redis_chart_readiness_contract.py --print-rendered --chart-file ...

Exit codes: 0 contract satisfied, 1 findings, 2 usage/parse/IO error.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import redis_readiness_contract as contract  # noqa: E402

CONTAINER_NAME = contract.CONTAINER_NAME
REDIS_PORT = contract.REDIS_PORT

#: Chart path of the template this contract is about (relative to the chart root).
CHART_TEMPLATE = "backend/templates/redis.yaml"

#: The chart's own redis values (``backend/values.yaml``; no deployment overlay
#: changes the redis block). The renderer needs them to evaluate the ``if``
#: guards and the inline expressions. Only these keys are used by the template.
CHART_VALUES = {
    "redis.enabled": True,
    "redis.image.repository": "redis",
    "redis.image.tag": "7-alpine",
    "redis.image.pullPolicy": "IfNotPresent",
    "redis.service.port": REDIS_PORT,
    "redis.persistence.enabled": False,
    "redis.persistence.mountPath": "/data",
    "redis.persistence.size": "1Gi",
    "redis.persistence.accessMode": "ReadWriteOnce",
    "redis.persistence.existingClaim": None,
    "rootNamespace": "chart-namespace",
}

#: The probes must actually probe the redis port; a probe on another port (or a
#: non-tcpSocket probe) does not guard the endpoint the Service selects.
PROBE_PORT_FINDING = "probe_port_mismatch"

_EXPRESSION = re.compile(r"\{\{\s*(.*?)\s*\}\}")


class ChartParseError(RuntimeError):
    """The chart template could not be reduced to a container spec."""


def _scalar(text):
    """Parse a YAML scalar the chart subset uses."""
    text = text.strip()
    if len(text) >= 2 and text[0] == text[-1] and text[0] in "\"'":
        return text[1:-1]
    if text in ("true", "false"):
        return text == "true"
    if text in ("null", "~"):
        return None
    try:
        return int(text)
    except ValueError:
        return text


def _expression_value(expression, values):
    """Resolve ``.Values.x`` plus the ``| default "y"`` filter chain."""
    parts = [part.strip() for part in expression.split("|")]
    key = parts[0]
    if key.startswith(".Values."):
        value = values.get(key[len(".Values."):])
    else:
        value = values.get(key[1:]) if key.startswith(".") else None
    for part in parts[1:]:
        if part.startswith("default") and (value is None or value == "" or value is False):
            value = _scalar(part[len("default"):].strip())
    return value


def _truthy(value):
    return value is not None and value is not False and value != ""


def render_chart_template(text, values=None):
    """Render the Go-template subset the chart's redis template uses.

    Control lines (``{{- if ... }}`` / ``{{- else }}`` / ``{{- end }}``) are
    evaluated against ``values``; everything else is kept (and inline
    expressions substituted) only while every enclosing guard is active.
    """
    values = dict(CHART_VALUES if values is None else values)
    active = []  # one flag per open if/else block
    rendered = []

    def _inline(match):
        value = _expression_value(match.group(1), values)
        if value is None:
            raise ChartParseError("unresolved template expression %r" % match.group(1))
        return str(value)

    for raw in text.splitlines():
        stripped = raw.strip()
        match = _EXPRESSION.fullmatch(stripped)
        if match:
            expression = match.group(1).strip()
            if expression.startswith("-"):
                expression = expression[1:].strip()
            if expression.startswith("if "):
                active.append(all(active) and _truthy(_expression_value(expression[3:], values)))
                continue
            if expression == "end":
                if not active:
                    raise ChartParseError("unbalanced {{ end }} in the chart template")
                active.pop()
                continue
            if expression == "else":
                if not active:
                    raise ChartParseError("{{ else }} outside an if block")
                active[-1] = all(active[:-1]) and not active[-1]
                continue
        if not all(active):
            continue
        rendered.append(_EXPRESSION.sub(_inline, raw))

    if active:
        raise ChartParseError("unclosed {{ if }} in the chart template")
    return "\n".join(rendered) + "\n"


def _significant_lines(text):
    lines = []
    for raw in text.splitlines():
        line = raw.rstrip()
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        lines.append((len(line) - len(line.lstrip(" ")), stripped))
    return lines


def _parse_mapping(items, index, indent):
    node = {}
    while index < len(items):
        current_indent, text = items[index]
        if current_indent < indent or text.startswith("- "):
            break
        if current_indent > indent:
            raise ChartParseError("unexpected indent %d: %r" % (current_indent, text))
        key, separator, rest = text.partition(":")
        if not separator:
            raise ChartParseError("not a mapping entry: %r" % text)
        key = key.strip()
        rest = rest.strip()
        index += 1
        if rest:
            node[key] = _scalar(rest)
        elif index < len(items) and items[index][0] > current_indent:
            node[key], index = _parse_node(items, index, items[index][0])
        else:
            node[key] = None
    return node, index


def _parse_sequence(items, index, indent):
    sequence = []
    while index < len(items):
        current_indent, text = items[index]
        if current_indent != indent or not text.startswith("- "):
            break
        inline = text[2:].strip()
        index += 1
        if not inline:
            if index < len(items) and items[index][0] > indent:
                value, index = _parse_node(items, index, items[index][0])
            else:
                value = None
            sequence.append(value)
            continue
        if ":" in inline and not inline.startswith(("\"", "'")):
            key, _, rest = inline.partition(":")
            item = {}
            key = key.strip()
            rest = rest.strip()
            if rest:
                item[key] = _scalar(rest)
            elif index < len(items) and items[index][0] > indent:
                item[key], index = _parse_node(items, index, items[index][0])
            else:
                item[key] = None
            if index < len(items) and items[index][0] > indent and not items[index][1].startswith("- "):
                more, index = _parse_mapping(items, index, items[index][0])
                item.update(more)
            sequence.append(item)
        else:
            sequence.append(_scalar(inline))
    return sequence, index


def _parse_node(items, index, indent):
    if items[index][1].startswith("- "):
        return _parse_sequence(items, index, indent)
    return _parse_mapping(items, index, indent)


def _dig(data, *keys):
    for key in keys:
        if not isinstance(data, dict):
            return None
        data = data.get(key)
    return data


def parse_chart_template(text, values=None, port=REDIS_PORT):
    """Return the redis container dict the chart template renders."""
    rendered = render_chart_template(text, values)
    document = rendered.split("\n---")[0]
    items = _significant_lines(document)
    if not items:
        raise ChartParseError("empty chart template")
    data, _ = _parse_node(items, 0, items[0][0])
    containers = _dig(data, "spec", "template", "spec", "containers") or []
    for container in containers:
        if isinstance(container, dict) and container.get("name") == CONTAINER_NAME:
            return container
    raise ChartParseError("no %s container in the chart template" % CONTAINER_NAME)


def load_chart_container(path, values=None, port=REDIS_PORT):
    return parse_chart_template(
        pathlib.Path(path).read_text(encoding="utf-8"), values=values, port=port
    )


def check_container(container, port=REDIS_PORT):
    """The ci#722 findings for a rendered redis container, in a fixed order."""
    findings = list(contract.evaluate(container))
    for probe_key, *_ in contract.PROBE_CONTRACT:
        probe = container.get(probe_key)
        if isinstance(probe, dict) and probe:
            socket = probe.get("tcpSocket") or {}
            if socket.get("port") != port:
                findings.append(PROBE_PORT_FINDING)
    return findings


def check_chart_file(path, values=None, port=REDIS_PORT):
    return check_container(load_chart_container(path, values=values, port=port), port=port)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--chart-file", help="chart redis template (Go template) to check")
    parser.add_argument(
        "--chart-dir",
        help="chart checkout to check (%s is read from it)" % CHART_TEMPLATE,
    )
    parser.add_argument("--port", type=int, default=REDIS_PORT, help="redis service port")
    parser.add_argument("--json", dest="json_out", help="write the findings as JSON")
    parser.add_argument(
        "--print-rendered",
        action="store_true",
        help="print the rendered first document before checking it",
    )
    args = parser.parse_args(argv)

    chart_file = args.chart_file
    if not chart_file and args.chart_dir:
        chart_file = os.path.join(args.chart_dir, CHART_TEMPLATE)
    if not chart_file:
        print("redis_chart_readiness error=one of --chart-file/--chart-dir is required")
        return 2

    try:
        text = pathlib.Path(chart_file).read_text(encoding="utf-8")
        if args.print_rendered:
            sys.stdout.write(render_chart_template(text))
        container = parse_chart_template(text, port=args.port)
    except (OSError, ChartParseError) as exc:
        print("redis_chart_readiness error=%s" % exc)
        return 2

    findings = check_container(container, port=args.port)
    verdict = "ok" if not findings else "failed"
    print(
        "redis_chart_readiness file=%s findings=[%s] contract=%s"
        % (chart_file, ", ".join(findings), verdict)
    )
    if args.json_out:
        pathlib.Path(args.json_out).write_text(
            json.dumps({"findings": findings, "contract": verdict}, indent=2, sort_keys=True)
            + "\n",
            encoding="utf-8",
        )
    if findings:
        print(
            "::error::the Helm chart redis template still renders the ci#638/ci#722 "
            "findings; apply development/redis-readiness/ci-722-chart.patch at the chart root"
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
