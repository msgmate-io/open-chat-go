#!/usr/bin/env python3
"""ci#890 - fail closed when a deployment dispatch could claim the CI host.

`ci.msgmate.io` (the `msgmate-io-ci` deployment) was observed serving a VERSION
that existed only on unmerged branches, while the run history showed a
*PR-preview* deployment (`msgmate-io-pr-deployment`) for an already **closed**
pull request running *after* the `main`-merge deploys. A pull-request preview
must never be able to claim, or leave behind, the shared CI deployment - neither
by naming its namespace/ingress host, nor by dispatching without the PR-scoped
overrides (which makes the CI-side deploy fall back to deployment-wide defaults),
nor by running for a PR that is already closed.

This module is a pure predicate over one dispatch payload so it can be unit
tested without a cluster: `evaluate()` returns the list of problems, and the CLI
wrapper turns them into `::error::` lines plus a non-zero exit code.

Rules (all fail closed):

1. A PR-preview dispatch must carry a PR number and PR-scoped overrides
   (`open-chat-pr-<n>`, `backend-pr-<n>`, `<slug>-pr-<n>.pr-env.msgmate.io`).
   Missing/blank overrides are refused: the CI-side deploy only honours a
   non-empty override, so an empty one would deploy onto the deployment-wide
   identity of `values.msgmate-io-pr-deployment.yaml`.
2. No dispatch - PR preview or not - may name the CI deployment identity
   (`msgmate-io-ci` namespace or the `ci.msgmate.io` host).
3. No PR-preview dispatch may run for a closed/merged PR.
4. A non-PR deploy (ci/staging/production) must not carry PR-scoped overrides,
   so a preview host/namespace can never leak into a merged deployment.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from typing import Any, Dict, List, Optional

CI_DEPLOYMENT = "msgmate-io-ci"
PR_DEPLOYMENT = "msgmate-io-pr-deployment"
CI_NAMESPACE = "msgmate-io-ci"
CI_HOST = "ci.msgmate.io"
PR_HOST_SUFFIX = ".pr-env.msgmate.io"
CLOSED_STATES = {"closed", "merged"}


def _clean(value: Optional[str]) -> str:
    return (value or "").strip()


def _is_ci_identity(value: str) -> bool:
    """True when a namespace/host resolves to the CI deployment itself."""
    v = value.lower()
    return v in {CI_NAMESPACE.lower(), CI_HOST.lower(), CI_DEPLOYMENT.lower()}


def evaluate(payload: Dict[str, Any]) -> List[str]:
    """Return the list of problems for one dispatch payload (empty == allowed)."""
    deployment_name = _clean(payload.get("deployment_name"))
    namespace = _clean(payload.get("namespace_override"))
    host = _clean(payload.get("deploy_host_override"))
    release = _clean(payload.get("helm_release_override"))
    pr_number = _clean(str(payload.get("pr_number", "")))
    pr_state = _clean(str(payload.get("pr_state", ""))).lower()

    problems: List[str] = []
    is_pr_preview = deployment_name == PR_DEPLOYMENT

    # Rule 2 applies to every dispatch, not only previews.
    if _is_ci_identity(namespace):
        problems.append(
            "namespace '%s' is the CI deployment namespace (%s); refusing to dispatch."
            % (namespace, CI_NAMESPACE)
        )
    if host and _is_ci_identity(host):
        problems.append(
            "deploy host '%s' is the CI deployment host (%s); refusing to dispatch."
            % (host, CI_HOST)
        )
    if deployment_name == CI_DEPLOYMENT and (namespace or host or release):
        problems.append(
            "deployment '%s' carries PR-scoped overrides (namespace='%s', host='%s', "
            "release='%s') that would redirect the CI deployment; refusing to dispatch."
            % (CI_DEPLOYMENT, namespace, host, release)
        )

    if is_pr_preview:
        if pr_state in CLOSED_STATES:
            problems.append(
                "PR #%s is %s; a PR-preview deploy must not run for a closed/merged PR."
                % (pr_number or "?", pr_state)
            )
        if not pr_number:
            problems.append(
                "deployment '%s' has no PR number, so its overrides cannot be proven "
                "PR-scoped; refusing to dispatch." % PR_DEPLOYMENT
            )
        expected_namespace = "open-chat-pr-%s" % pr_number if pr_number else ""
        expected_release = "backend-pr-%s" % pr_number if pr_number else ""
        if not namespace:
            problems.append(
                "PR-preview dispatch is missing NAMESPACE_OVERRIDE; the CI deploy would "
                "fall back to the deployment-wide namespace instead of open-chat-pr-%s."
                % (pr_number or "<n>")
            )
        elif expected_namespace and namespace != expected_namespace:
            problems.append(
                "PR-preview namespace '%s' is not PR-scoped (expected '%s')."
                % (namespace, expected_namespace)
            )
        if not host:
            problems.append(
                "PR-preview dispatch is missing DEPLOY_HOST_OVERRIDE; the CI deploy would "
                "fall back to the deployment-wide ingress host instead of a pr-env host."
            )
        elif not host.lower().endswith(PR_HOST_SUFFIX):
            problems.append(
                "PR-preview host '%s' is not a %s host." % (host, PR_HOST_SUFFIX)
            )
        elif pr_number and "-pr-%s%s" % (pr_number, PR_HOST_SUFFIX) not in host:
            problems.append(
                "PR-preview host '%s' does not belong to PR #%s." % (host, pr_number)
            )
        if not release:
            problems.append(
                "PR-preview dispatch is missing HELM_RELEASE_OVERRIDE; the CI deploy would "
                "reuse the deployment-wide release instead of backend-pr-%s."
                % (pr_number or "<n>")
            )
        elif expected_release and release != expected_release:
            problems.append(
                "PR-preview helm release '%s' is not PR-scoped (expected '%s')."
                % (release, expected_release)
            )
    elif namespace or host or release:
        problems.append(
            "deployment '%s' is not a PR preview but carries PR-scoped overrides "
            "(namespace='%s', host='%s', release='%s'); refusing to dispatch."
            % (deployment_name or "?", namespace, host, release)
        )

    return problems


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--payload-json", default=os.environ.get("DISPATCH_PAYLOAD", ""),
                        help="dispatch payload as JSON (default: $DISPATCH_PAYLOAD)")
    parser.add_argument("--deployment-name", default=os.environ.get("DEPLOYMENT_NAME", ""))
    parser.add_argument("--namespace-override", default=os.environ.get("NAMESPACE_OVERRIDE", ""))
    parser.add_argument("--deploy-host-override", default=os.environ.get("DEPLOY_HOST_OVERRIDE", ""))
    parser.add_argument("--helm-release-override", default=os.environ.get("HELM_RELEASE_OVERRIDE", ""))
    parser.add_argument("--pr-number", default=os.environ.get("PR_NUMBER", ""))
    parser.add_argument("--pr-state", default=os.environ.get("PR_STATE", ""))
    args = parser.parse_args(argv)

    if args.payload_json.strip():
        try:
            payload = json.loads(args.payload_json)
        except json.JSONDecodeError as exc:
            print("::error::Could not parse the dispatch payload as JSON: %s" % exc)
            return 1
    else:
        payload = {
            "deployment_name": args.deployment_name,
            "namespace_override": args.namespace_override,
            "deploy_host_override": args.deploy_host_override,
            "helm_release_override": args.helm_release_override,
            "pr_number": args.pr_number,
            "pr_state": args.pr_state,
        }

    problems = evaluate(payload)
    if problems:
        for problem in problems:
            print("::error::ci#890: %s" % problem)
        print("::error::ci#890: refusing to dispatch this deployment "
              "(a PR preview must never be able to claim the CI host %s/%s)."
              % (CI_NAMESPACE, CI_HOST))
        return 1

    print("ci#890: dispatch target verified - deployment='%s' namespace='%s' host='%s' "
          "release='%s' PR='%s' state='%s'."
          % (payload.get("deployment_name", ""), payload.get("namespace_override", ""),
             payload.get("deploy_host_override", ""), payload.get("helm_release_override", ""),
             payload.get("pr_number", ""), payload.get("pr_state", "")))
    return 0


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
