#!/usr/bin/env python3
"""ci#722 - read-only contract check for the *compose* redis probe budget.

The coder-sandbox readiness gate (ci#666) reported the redis Deployment of the
CI deployment with `probe_timeout_too_low`, `probe_budget_too_short`,
`startup_probe_missing` and `redis_cpu_request_missing`: an unbudgeted,
unschedulable redis takes the queue/state endpoint down, so freshly dispatched
interactions are dead on arrival.

`development/scripts/redis_readiness_contract.py` is the Kubernetes half of that
contract (the live `redis-container` Deployment and the strategic-merge patch the
read-only gate proposes). The same redis serves the CI and local deployments
through the compose profiles, where the identical equivocation applies: a
healthcheck that gives up after a few seconds of contention marks the container
unhealthy, and a redis without a CPU reservation is starved behind the agent
workloads on a shared runner.

This module is the compose half, stdlib-only (the Pin Freshness Gate runs it with
the system `python3`, without the build-tools venv / PyYAML). It extracts the
`redis` service from a compose file with a small block-mapping parser and
re-derives the ci#722 findings with the same ids:

  * `probe_missing`                  - no healthcheck / no `test` command,
  * `probe_timeout_too_low`          - `timeout` below 2s (the value that flapped),
  * `probe_budget_too_short`         - `interval * retries` below 60s of sustained
                                       failure before the container is unhealthy,
  * `startup_probe_missing`          - no explicit `start_period` grace,
  * `redis_cpu_request_missing`      - no CPU reservation (scheduling guarantee),
  * `redis_cpu_request_too_low`, `redis_cpu_limit_too_low`,
    `redis_cpu_limit_below_request` - the reservation/limit pair does not match
                                       the ci#722 numbers,
  * `redis_service_missing`          - no redis service in the file at all.

Usage:
  development/scripts/redis_compose_readiness_contract.py
  development/scripts/redis_compose_readiness_contract.py --file docker-compose.ci.yaml

Exit codes: 0 contract satisfied, 1 findings, 2 usage or unreadable input.
"""

from __future__ import annotations

import argparse
import pathlib
import re

#: The compose profiles that carry the redis service of a real deployment.
DEFAULT_FILES = (
    "docker-compose.yaml",
    "docker-compose.ci.yaml",
    "docker-compose.pro.yaml",
)
SERVICE_NAME = "redis"

#: ci#722 numbers, kept identical to the checked-in Kubernetes patch artifact.
MIN_PROBE_TIMEOUT_SECONDS = 2
MIN_SUSTAINED_FAILURE_SECONDS = 60
MIN_STARTUP_SECONDS = 5
MIN_REDIS_CPU_RESERVATION_MILLI = 100
MIN_REDIS_CPU_LIMIT_MILLI = 500

#: Compose defaults when a healthcheck field is absent (docker/compose spec).
DEFAULT_INTERVAL_SECONDS = 30
DEFAULT_TIMEOUT_SECONDS = 30
DEFAULT_RETRIES = 3
DEFAULT_STARTUP_SECONDS = 0

#: Stable finding ids (the gate's vocabulary, reused for the compose profiles).
PROBE_MISSING_FINDING = "probe_missing"
TIMEOUT_FINDING = "probe_timeout_too_low"
BUDGET_FINDING = "probe_budget_too_short"
STARTUP_FINDING = "startup_probe_missing"
CPU_FINDING = "redis_cpu_request_missing"
CPU_REQUEST_TOO_LOW_FINDING = "redis_cpu_request_too_low"
CPU_LIMIT_TOO_LOW_FINDING = "redis_cpu_limit_too_low"
CPU_LIMIT_BELOW_REQUEST_FINDING = "redis_cpu_limit_below_request"
SERVICE_MISSING_FINDING = "redis_service_missing"

_KEY_RE = re.compile(r"^(\s*)([A-Za-z0-9_.-]+):(?:\s*(.*))?$")
_DURATION_PART_RE = re.compile(r"(\d+(?:\.\d+)?)(ms|s|m|h)")
_DURATION_UNITS = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}


def _scalar(value):
    """Strip surrounding quotes from an inline scalar."""
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
        return value[1:-1]
    return value


#: ``${VAR}`` / ``${VAR:-default}`` / ``${VAR-default}`` compose interpolation.
_INTERP_RE = re.compile(r"^\$\{[A-Za-z_][A-Za-z0-9_]*:?-([^}]*)\}$")


def unquote_scalar(value):
    """Resolve a compose scalar to the value `docker compose` would use here.

    Values are quoted in the compose profiles so the numeric budgets stay
    scalar strings; env interpolation (`${REDIS_CPUS:-0.5}`) resolves to its
    default because the contract check runs without the deployment's
    environment.
    """
    if value is None:
        return None
    text = _scalar(str(value))
    match = _INTERP_RE.match(text)
    if match:
        return match.group(1) or None
    return text


def _indent_of(raw):
    return len(raw) - len(raw.lstrip())


def _is_ignorable(raw):
    stripped = raw.strip()
    return not stripped or stripped.startswith("#")


def _next_child(lines, index, current_indent):
    """Index/indent of the next line deeper than ``current_indent``, if any."""
    while index < len(lines):
        raw = lines[index]
        if _is_ignorable(raw):
            index += 1
            continue
        indent = _indent_of(raw)
        if indent <= current_indent:
            return None, index
        return indent, index
    return None, index


def _parse_mapping(lines, index, indent):
    """Parse a block mapping at ``indent``; return (mapping, next index)."""
    result = {}
    while index < len(lines):
        raw = lines[index]
        if _is_ignorable(raw):
            index += 1
            continue
        line_indent = _indent_of(raw)
        if line_indent < indent:
            break
        match = _KEY_RE.match(raw)
        if line_indent > indent or not match:
            # Nested block or sequence entry: only reachable through recursion.
            index += 1
            continue
        key = match.group(2)
        value = (match.group(3) or "").strip()
        index += 1
        child_indent, child_index = _next_child(lines, index, indent)
        if not value and child_indent is not None:
            child, index = _parse_mapping(lines, child_index, child_indent)
            result[key] = child
        else:
            result[key] = _scalar(value)
    return result, index


def parse_top_level(text):
    """Parse a compose file's top-level block mapping (stdlib-only)."""
    lines = text.splitlines()
    index = 0
    while index < len(lines) and _is_ignorable(lines[index]):
        index += 1
    if index >= len(lines):
        return {}
    mapping, _ = _parse_mapping(lines, index, _indent_of(lines[index]))
    return mapping


def parse_service(text, name=SERVICE_NAME):
    """Return the ``services.<name>`` mapping of a compose file, or None.

    The `services` block is resolved structurally, so a `depends_on: redis:`
    entry of another service cannot shadow the real redis service definition.
    """
    services = parse_top_level(text).get("services")
    if not isinstance(services, dict):
        return None
    service = services.get(name)
    if service is None:
        return None
    return service if isinstance(service, dict) else {}


def duration_seconds(value, default=None):
    """Seconds for a compose duration ("5s", "1m30s", 2), else ``default``."""
    if value is None:
        return default
    if isinstance(value, (int, float)):
        return float(value)
    text = unquote_scalar(value) or ""
    if not text:
        return default
    total = 0.0
    position = 0
    for match in _DURATION_PART_RE.finditer(text):
        if match.start() != position:
            return default
        total += float(match.group(1)) * _DURATION_UNITS[match.group(2)]
        position = match.end()
    if position != len(text):
        return default
    return total


def retry_count(value, default=DEFAULT_RETRIES):
    """Integer retry count ("12", 12, "12.0") or ``default``."""
    if value is None:
        return default
    if isinstance(value, bool):
        return default
    if isinstance(value, int):
        return value
    text = unquote_scalar(value) or ""
    if not text:
        return default
    try:
        return int(float(text))
    except ValueError:
        return default


def milli_cpus(value):
    """Milli-CPU for "0.5" / "500m" / 1, or None when unset/unparseable."""
    if value is None:
        return None
    text = unquote_scalar(value) or ""
    if not text:
        return None
    try:
        if text.endswith("m"):
            return float(text[:-1])
        return float(text) * 1000.0
    except ValueError:
        return None


def _nested(mapping, *keys):
    current = mapping
    for key in keys:
        if not isinstance(current, dict):
            return {}
        current = current.get(key)
    return current if isinstance(current, dict) else {}


def evaluate(service):
    """Return the contract findings for one compose service, in a fixed order."""
    findings = []
    healthcheck = service.get("healthcheck") if isinstance(service, dict) else None
    if not isinstance(healthcheck, dict) or not healthcheck.get("test"):
        findings.append(PROBE_MISSING_FINDING)
    else:
        timeout = duration_seconds(
            healthcheck.get("timeout"), DEFAULT_TIMEOUT_SECONDS
        )
        if timeout is None or timeout < MIN_PROBE_TIMEOUT_SECONDS:
            findings.append(TIMEOUT_FINDING)
        interval = duration_seconds(healthcheck.get("interval"), DEFAULT_INTERVAL_SECONDS)
        retries = retry_count(healthcheck.get("retries"))
        budget = (interval or 0) * (retries or 0)
        if budget < MIN_SUSTAINED_FAILURE_SECONDS:
            findings.append(BUDGET_FINDING)
        startup = duration_seconds(
            healthcheck.get("start_period"), DEFAULT_STARTUP_SECONDS
        )
        if startup is None or startup < MIN_STARTUP_SECONDS:
            findings.append(STARTUP_FINDING)

    resources = _nested(service, "deploy", "resources")
    request = milli_cpus(_nested(resources, "reservations").get("cpus"))
    # The CPU *limit* is read from either dialect: the swarm
    # `deploy.resources.limits.cpus` or the non-swarm service-level `cpus`
    # that `docker compose up` honours (and that merges cleanly with the
    # sandbox overlay, ci#785).
    limit = milli_cpus(
        _nested(resources, "limits").get("cpus") or service.get("cpus")
    )
    if not request:
        findings.append(CPU_FINDING)
    elif request < MIN_REDIS_CPU_RESERVATION_MILLI:
        findings.append(CPU_REQUEST_TOO_LOW_FINDING)
    if limit is not None and limit < MIN_REDIS_CPU_LIMIT_MILLI:
        findings.append(CPU_LIMIT_TOO_LOW_FINDING)
    if limit is not None and request and limit < request:
        findings.append(CPU_LIMIT_BELOW_REQUEST_FINDING)
    return findings


def check_text(text, name=SERVICE_NAME):
    """Findings for a compose file's text (``redis_service_missing`` if absent)."""
    service = parse_service(text, name)
    if service is None:
        return [SERVICE_MISSING_FINDING]
    return evaluate(service)


def check_file(path, name=SERVICE_NAME):
    return check_text(pathlib.Path(path).read_text(encoding="utf-8"), name)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--file",
        action="append",
        dest="files",
        default=None,
        help="compose file to check (repeatable; defaults to the shipped profiles)",
    )
    parser.add_argument("--service", default=SERVICE_NAME, help="service name to check")
    args = parser.parse_args(argv)
    files = args.files or list(DEFAULT_FILES)

    failed = False
    for path in files:
        target = pathlib.Path(path)
        if not target.is_file():
            print("::error::redis_compose_readiness cannot read %s" % path)
            return 2
        findings = check_file(target, args.service)
        state = "ok" if not findings else "FAILED"
        print(
            "redis_compose_readiness %s service=%s findings=[%s] %s"
            % (path, args.service, ",".join(findings), state)
        )
        if findings:
            failed = True
    if failed:
        print(
            "::error::redis compose probe budget contract violated (ci#722); "
            "mirror development/redis-readiness/ci-722-redis-probes.patch.json"
        )
        return 1
    print("redis_compose_readiness contract=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())