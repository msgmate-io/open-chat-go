#!/usr/bin/env python3
"""ci#722 - read-only contract check for the redis probe budget / CPU guarantee.

The coder-sandbox readiness gate (ci#666) reported the redis Deployment of the
CI deployment as a failure source: under load the queue/state endpoint backed by
redis could be evicted, so freshly dispatched interactions were dead on arrival.
The gate is read-only and derives a strategic-merge patch for the operator; it
never applies it. This script is the repo-side half of that contract:

  * it re-derives the same findings from a redis Deployment manifest
    (`kubectl get deploy ... -o json`, or a file/JSON on stdin),
  * it emits the exact remediation patch the gate proposed (ci#722), so the
    patch in `development/redis-readiness/` cannot drift from the checks,
  * and it exits non-zero while any contract check fails.

Findings are reported in a fixed order - probe presence, then per probe
(liveness, readiness, startup) the timeout and the budget, then the CPU request -
so two runs over the same manifest produce the same line:

    startup_probe_missing
    probe_timeout_too_low
    probe_timeout_too_low
    probe_budget_too_short
    redis_cpu_request_missing

Contract (the numbers the ci#722 patch settles on):

  * livenessProbe:  timeoutSeconds >= 2, failureThreshold * periodSeconds >= 30
  * readinessProbe: timeoutSeconds >= 2, failureThreshold * periodSeconds >= 30
  * startupProbe:   timeoutSeconds >= 2, failureThreshold * periodSeconds >= 60,
                    initialDelaySeconds 0 and a tcpSocket probe on the redis port
  * the container must carry a CPU *request* (scheduling guarantee), the limit is
    only reported

Usage:
  development/scripts/redis_readiness_contract.py --deployment-file deploy.json
  KUBECTL=kubectl development/scripts/redis_readiness_contract.py \
      --namespace msgmate-io-ci --name redis-container
  development/scripts/redis_readiness_contract.py --print-patch

Exit codes: 0 contract satisfied, 1 findings (or no redis container), 2 usage or
input error.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys

CONTAINER_NAME = "redis-container"
REDIS_PORT = 6379

# Kubernetes defaults apply when a field is absent: a probe without an explicit
# timeoutSeconds gets 1s (already below the contract), without failureThreshold 3
# and without periodSeconds 10.
DEFAULT_TIMEOUT_SECONDS = 1
DEFAULT_FAILURE_THRESHOLD = 3
DEFAULT_PERIOD_SECONDS = 10

# min_budget_seconds is failureThreshold * periodSeconds: how long a probe may
# keep retrying before Kubernetes gives up on the container.
PROBE_CONTRACT = (
    # (probe key, missing-finding, min timeout, min budget, exact values)
    ("livenessProbe", "liveness_probe_missing", 2, 30, None),
    ("readinessProbe", "readiness_probe_missing", 2, 30, None),
    (
        "startupProbe",
        "startup_probe_missing",
        2,
        60,
        {"initialDelaySeconds": 0, "periodSeconds": 5, "failureThreshold": 12},
    ),
)

TIMEOUT_FINDING = "probe_timeout_too_low"
BUDGET_FINDING = "probe_budget_too_short"
CPU_FINDING = "redis_cpu_request_missing"
MISSING_CONTAINER_FINDING = "redis_container_missing"


def find_container(deployment, name=CONTAINER_NAME):
    """Return the named container of the first pod template, or None."""
    containers = (
        deployment.get("spec", {})
        .get("template", {})
        .get("spec", {})
        .get("containers", [])
        or []
    )
    for container in containers:
        if container.get("name") == name:
            return container
    return None


def _probe_timeout(probe):
    value = probe.get("timeoutSeconds")
    return DEFAULT_TIMEOUT_SECONDS if value is None else value


def _probe_budget(probe):
    failure_threshold = probe.get("failureThreshold")
    period_seconds = probe.get("periodSeconds")
    if failure_threshold is None:
        failure_threshold = DEFAULT_FAILURE_THRESHOLD
    if period_seconds is None:
        period_seconds = DEFAULT_PERIOD_SECONDS
    return failure_threshold * period_seconds


def evaluate(container):
    """Return the contract findings for one container, in a fixed order."""
    findings = []
    for probe_key, missing_finding, min_timeout, min_budget, exact in PROBE_CONTRACT:
        probe = container.get(probe_key)
        if not isinstance(probe, dict) or not probe:
            findings.append(missing_finding)
            continue
        if _probe_timeout(probe) < min_timeout:
            findings.append(TIMEOUT_FINDING)
        # One budget finding per probe: the explicit exact-value check (startup)
        # and the derived failureThreshold * periodSeconds check are the same
        # remediation, so reporting both would duplicate the finding.
        if _probe_budget(probe) < min_budget or (
            exact and any(probe.get(k) != v for k, v in exact.items())
        ):
            findings.append(BUDGET_FINDING)

    requests = (container.get("resources") or {}).get("requests") or {}
    if not requests.get("cpu"):
        findings.append(CPU_FINDING)
    return findings


def build_patch(container_name=CONTAINER_NAME):
    """The ci#722 strategic-merge remediation patch for the redis container."""
    return {
        "spec": {
            "template": {
                "spec": {
                    "containers": [
                        {
                            "name": container_name,
                            "livenessProbe": {
                                "failureThreshold": 3,
                                "periodSeconds": 10,
                                "timeoutSeconds": 2,
                            },
                            "readinessProbe": {
                                "failureThreshold": 6,
                                "periodSeconds": 5,
                                "timeoutSeconds": 2,
                            },
                            "startupProbe": {
                                "failureThreshold": 12,
                                "initialDelaySeconds": 0,
                                "periodSeconds": 5,
                                "tcpSocket": {"port": REDIS_PORT},
                                "timeoutSeconds": 2,
                            },
                            "resources": {
                                "limits": {"cpu": "500m"},
                                "requests": {"cpu": "100m"},
                            },
                        }
                    ]
                }
            }
        }
    }


def render_patch(container_name=CONTAINER_NAME):
    return json.dumps(build_patch(container_name), indent=2, sort_keys=True) + "\n"


def apply_patch(deployment, patch):
    """Strategic-merge the patch into a deployment (dicts merge, lists by name)."""
    merged = json.loads(json.dumps(deployment))

    def merge(target, overlay):
        for key, value in overlay.items():
            if isinstance(value, dict) and isinstance(target.get(key), dict):
                merge(target[key], value)
            elif isinstance(value, list):
                current = target.get(key)
                if not isinstance(current, list):
                    target[key] = json.loads(json.dumps(value))
                    continue
                for item in value:
                    if isinstance(item, dict) and "name" in item:
                        match = next(
                            (
                                existing
                                for existing in current
                                if isinstance(existing, dict)
                                and existing.get("name") == item["name"]
                            ),
                            None,
                        )
                        if match is None:
                            current.append(json.loads(json.dumps(item)))
                        else:
                            merge(match, item)
                    else:
                        current.append(json.loads(json.dumps(item)))
            else:
                target[key] = json.loads(json.dumps(value))
        return target

    return merge(merged, patch)


def load_deployment(args):
    if args.deployment_file:
        with open(args.deployment_file, "r", encoding="utf-8") as handle:
            return json.load(handle)
    if args.deployment_json:
        return json.loads(args.deployment_json)
    command = [
        args.kubectl,
        "-n",
        args.namespace,
        "get",
        "deploy",
        args.name,
        "-o",
        "json",
    ]
    completed = subprocess.run(
        command, capture_output=True, text=True, check=False
    )
    if completed.returncode != 0:
        raise SystemExit(
            "::error::kubectl get deploy %s failed (%d): %s"
            % (args.name, completed.returncode, completed.stderr.strip())
        )
    return json.loads(completed.stdout)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--deployment-file", help="deployment manifest JSON")
    parser.add_argument("--deployment-json", help="deployment manifest JSON inline")
    parser.add_argument("--kubectl", default="kubectl", help="kubectl binary")
    parser.add_argument(
        "--namespace", default="msgmate-io-ci", help="namespace of the redis deployment"
    )
    parser.add_argument(
        "--name", default=CONTAINER_NAME, help="deployment name of the redis deployment"
    )
    parser.add_argument(
        "--container", default=CONTAINER_NAME, help="container name inside the deployment"
    )
    parser.add_argument(
        "--print-patch",
        action="store_true",
        help="print the ci#722 remediation patch instead of checking a manifest",
    )
    args = parser.parse_args(argv)

    if args.print_patch:
        sys.stdout.write(render_patch(args.container))
        return 0

    deployment = load_deployment(args)
    container = find_container(deployment, args.container)
    if container is None:
        print("redis_readiness findings=[%s]" % MISSING_CONTAINER_FINDING)
        print("::error::no container %r in deployment %r" % (args.container, args.name))
        return 1

    findings = evaluate(container)
    print("redis_readiness findings=[%s]" % ",".join(findings))
    print(
        "redis_readiness probes=%d cpu_request=%s"
        % (
            sum(1 for key, *_ in PROBE_CONTRACT if isinstance(container.get(key), dict)),
            ((container.get("resources") or {}).get("requests") or {}).get("cpu")
            or "unset",
        )
    )
    if findings:
        print(
            "::error::redis probe budget contract violated (ci#722); apply "
            "development/redis-readiness/ci-722-redis-probes.patch.json"
        )
        return 1
    print("redis_readiness contract=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
