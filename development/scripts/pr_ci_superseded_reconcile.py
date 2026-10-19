#!/usr/bin/env python3
"""Reconcile a superseded (cancelled) PR CI run with its replacement.

The PR CI workflow cancels superseded runs for the same head branch, but its
always()-gated `PR Test Merge Requirement` job still executes inside the
cancelled run and reported a red gate for a commit whose tests never ran (run
39245536652). Blanket-passing that case would make an untested tree look
mergeable, so the cancellation is reconciled instead: accept only when the
commit no longer needs gating (PR closed, or a newer commit is the live PR
head) or when a replacement run for the same commit reached the gate job with a
real verdict, whose conclusion is adopted. Everything else fails closed.

Exit codes: 0 accept, 1 replacement gate not green, 2 missing, 3 timeout,
4 usage/API error. The last stdout line is `accepted=true|false`.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request

WORKFLOW_FILE = "run-pr-ci-tests-and-deployments.yaml"
GATE_JOB_NAME = "PR Test Merge Requirement"
API = "https://api.github.com"

_SLEEP = time.sleep
_MONOTONIC = time.monotonic


def fetch_json(url, token, timeout=30):
    request = urllib.request.Request(
        url,
        headers={
            "Authorization": "Bearer " + token,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


_FETCH = fetch_json


def api(repo, path, token):
    return _FETCH(API + "/repos/" + repo + path, token)


def gate_conclusion(repo, run_id, token):
    """Verdict of the run's gate job, or None when the run has no gate job."""
    jobs = api(repo, "/actions/runs/%s/jobs?per_page=100" % run_id, token).get("jobs") or []
    for job in jobs:
        if job.get("name") == GATE_JOB_NAME:
            return job.get("conclusion") or job.get("status")
    return None


def replacement_runs(repo, head_sha, current_run_id, token):
    """Runs of this workflow for `head_sha`, newest first, minus the current one."""
    data = api(
        repo,
        "/actions/workflows/%s/runs?head_sha=%s&per_page=100" % (WORKFLOW_FILE, head_sha),
        token,
    )
    runs = [r for r in (data.get("workflow_runs") or []) if r.get("id") != current_run_id]
    runs.sort(key=lambda r: (r.get("id") or 0, r.get("run_attempt") or 1), reverse=True)
    return runs


def gate_verdict(runs, repo, token):
    """Newest completed candidate run with a real gate verdict, else (None, None).

    Runs that never contained the gate job (the `closed`-event deployment runs)
    and runs whose gate job was itself cancelled cannot testify about the
    commit, so they are skipped rather than mistaken for a green gate.
    """
    for run in runs:
        if run.get("status") != "completed":
            continue
        conclusion = gate_conclusion(repo, run.get("id"), token)
        if conclusion in (None, "cancelled"):
            continue
        return run, conclusion
    return None, None


def reconcile(args, token):
    """Return (exit_code, summary) for the cancelled run described by `args`."""
    pull = api(args.repo, "/pulls/%d" % args.pr, token)
    if pull.get("state") != "open":
        return 0, "accepted: PR #%d is %s, the cancelled run no longer gates anything." % (
            args.pr,
            pull.get("state"),
        )
    live_head = (pull.get("head") or {}).get("sha")
    if live_head and live_head != args.head_sha:
        return 0, (
            "accepted: commit %s is superseded by the live PR head %s; the "
            "cancelled run gated an obsolete commit." % (args.head_sha[:12], live_head[:12])
        )

    deadline = _MONOTONIC() + args.wait_seconds
    while True:
        candidates = replacement_runs(args.repo, args.head_sha, args.run_id, token)
        run, conclusion = gate_verdict(candidates, args.repo, token)
        if run is not None:
            if conclusion == "success":
                return 0, "accepted: replacement run %s gate job concluded success." % run.get("id")
            return 1, "rejected: replacement run %s gate job concluded %s." % (
                run.get("id"),
                conclusion,
            )
        if _MONOTONIC() >= deadline:
            if not candidates:
                return 3, "timeout: no replacement run for %s within %ds." % (
                    args.head_sha[:12],
                    args.wait_seconds,
                )
            pending = [r for r in candidates if r.get("status") != "completed"]
            if pending:
                return 3, "timeout: replacement run %s still %s after %ds." % (
                    pending[0].get("id"),
                    pending[0].get("status"),
                    args.wait_seconds,
                )
            return 2, (
                "missing: no replacement run for %s reached the '%s' job, so the "
                "commit was never gated." % (args.head_sha[:12], GATE_JOB_NAME)
            )
        _SLEEP(min(30, max(1, args.wait_seconds)))


def main(argv=None):
    parser = argparse.ArgumentParser(description="Reconcile a superseded (cancelled) PR CI run.")
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    parser.add_argument("--pr", type=int, default=0)
    parser.add_argument("--head-sha", default="")
    parser.add_argument("--run-id", type=int, default=0)
    parser.add_argument(
        "--wait-seconds",
        type=int,
        default=int(os.environ.get("PR_CI_SUPERSEDED_WAIT_SECONDS") or 1500),
    )
    args = parser.parse_args(argv)

    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("LABEL_TOKEN") or ""
    missing = [
        name
        for name, value in (
            ("--repo", args.repo),
            ("--pr", args.pr),
            ("--head-sha", args.head_sha),
            ("--run-id", args.run_id),
        )
        if not value
    ]
    if missing or not token:
        print(
            "::error::pr_ci_superseded_reconcile: missing %s."
            % ("/".join(missing) or "API token (GITHUB_TOKEN/LABEL_TOKEN)")
        )
        return 4

    try:
        code, summary = reconcile(args, token)
    except urllib.error.HTTPError as error:
        code, summary = 4, "error: GitHub API returned HTTP %s for %s." % (error.code, error.url)
    except (urllib.error.URLError, ValueError, KeyError, TypeError) as error:
        code, summary = 4, "error: %s: %s" % (type(error).__name__, error)

    print(summary)
    print("accepted=%s" % ("true" if code == 0 else "false"))
    return code


if __name__ == "__main__":
    sys.exit(main())
