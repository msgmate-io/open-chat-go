#!/usr/bin/env python3
"""Reconcile a superseded (cancelled) PR CI run with its replacement (ci#659).

The PR CI workflow cancels superseded runs for the same head branch, but its
always()-gated `PR Test Merge Requirement` job still executes inside the
cancelled run and reported a red gate for a commit whose tests never ran (run
39245536652). Blanket-passing that case would make an untested tree look
mergeable, so the cancellation is reconciled instead: accept only when the
commit no longer needs gating (PR closed, or a newer commit is the live PR
head) or when a replacement run for the same commit reached the gate job with a
real verdict, whose conclusion is adopted. Everything else fails closed.

Exit codes: 0 accept, 1 replacement gate not green, 2 missing, 3 timeout/budget
expiry, 4 usage/API error. The last stdout line is `accepted=true|false`.

Exit 3 is a *budget* expiry, not a replacement verdict: no replacement run ever
got to testify before the wait budget ran out. Its summary starts with the
`budget-expired:` token and `main` prints a `::warning::` annotation naming the
budget, so a stale red gate from an exhausted budget is distinguishable in logs
from a replacement run that really came back non-green (exit 1).

A replacement gate job that was itself `cancelled` cannot testify about the
commit and is skipped, but every other non-`success` verdict (including
`skipped`, `failure` and `timed_out`) is adopted and rejects the gate.
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

# ci#659 follow-up: the wait budget must outlast a *whole* replacement run, not
# just the gap to its gate job. Run 39370413734 (PR #879, commit 886d6e26) needed
# 2550s from creation to its gate verdict, so the previous 1500s default expired
# ~12 minutes early: run 39370411496 timed out ("replacement run 39370413734
# still pending after 1500s") and reported the required check as failed for a
# commit whose replacement gate job then concluded success. One hour covers the
# slowest observed PR CI run with margin.
#
# Known residual, by design: this is still a deadline, so it is a cliff, not a
# fix of the class. An expiry leaves the gate red for a commit whose replacement
# may pass minutes later; the deadline is kept because it is what stops the gate
# job hanging forever when a replacement is queued behind a starving runner.
# What the fix buys is legibility: exit 3 prints `budget-expired:` plus a
# `::warning::`, so "we gave up waiting" is distinguishable from "the replacement
# really concluded non-green" (exit 1).
#
# Operational backstop for bumping it: if `budget-expired:` ever shows up on a
# PR CI run whose replacement gate job concludes `success` afterwards, the budget
# is again shorter than a real run - raise it, or finally replace the deadline
# with a replacement-driven liveness/heartbeat wait. No test can assert this,
# because "budget > actual run duration" is environmental, not a property of the
# code: the tests only pin that the constant is named, singular and shared with
# the workflow fallback.
DEFAULT_WAIT_SECONDS = 3600

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

PAGE_SIZE = 100
MAX_PAGES = 20


def api(repo, path, token):
    return _FETCH(API + "/repos/" + repo + path, token)


def paged_path(path, page, per_page=PAGE_SIZE):
    """Append offset-pagination query parameters to an API path."""
    separator = "&" if "?" in path else "?"
    return "%s%sper_page=%d&page=%d" % (path, separator, per_page, page)


def api_pages(repo, path, token, per_page=PAGE_SIZE, limit=MAX_PAGES):
    """Yield every item of an offset-paginated `{"<key>": [...]}` API listing.

    Every endpoint used here returns a single list under one key, so the page
    body is unwrapped generically and pages are requested until a short (or
    empty) page proves the listing is exhausted. A listing that keeps returning
    full pages stops at `limit`, which is only a runaway guard.
    """
    for page in range(1, limit + 1):
        payload = api(repo, paged_path(path, page, per_page), token)
        items = []
        for value in payload.values():
            if isinstance(value, list):
                items = value
                break
        for item in items:
            yield item
        if len(items) < per_page:
            return


def gate_conclusion(repo, run_id, token):
    """Verdict of the run's gate job, or None when the run has no gate job.

    The jobs listing is paginated: a run with more jobs than fit on one page
    would otherwise hide the gate job and be skipped as if it never gated the
    commit, letting a newer run's verdict be read off an older run.
    """
    for job in api_pages(repo, "/actions/runs/%s/jobs" % run_id, token):
        if job.get("name") == GATE_JOB_NAME:
            return job.get("conclusion") or job.get("status")
    return None


def replacement_runs(repo, head_sha, current_run_id, token):
    """Runs of this workflow for `head_sha`, newest first, minus the current one."""
    runs = [
        r
        for r in api_pages(
            repo,
            "/actions/workflows/%s/runs?head_sha=%s" % (WORKFLOW_FILE, head_sha),
            token,
        )
        if r.get("id") != current_run_id
    ]
    runs.sort(key=lambda r: (r.get("id") or 0, r.get("run_attempt") or 1), reverse=True)
    return runs


def gate_verdict(runs, repo, token):
    """Newest completed candidate run with a real gate verdict, else (None, None).

    Runs that never contained the gate job (the `closed`-event deployment runs)
    and runs whose gate job was itself cancelled cannot testify about the
    commit, so they are skipped rather than mistaken for a green gate.

    "Newest" only orders the candidates: a run that never gated the commit is
    not evidence, so an older run of the *same* commit may testify in its place.
    That is intentional - same commit, same tree, same verdict - and it keeps a
    redundant deployment run from turning a real gate result into a silent skip.
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
                return 3, "budget-expired: no replacement run for %s within %ds." % (
                    args.head_sha[:12],
                    args.wait_seconds,
                )
            pending = [r for r in candidates if r.get("status") != "completed"]
            if pending:
                return 3, "budget-expired: replacement run %s still %s after %ds." % (
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
        default=int(os.environ.get("PR_CI_SUPERSEDED_WAIT_SECONDS") or DEFAULT_WAIT_SECONDS),
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

    if code == 3:
        # A budget expiry is not a replacement verdict, but `continue-on-error:
        # true` keeps the non-zero exit invisible in the UI, so leave a warning
        # annotation: a red gate from an exhausted budget must be greppable in
        # the logs, not indistinguishable from a genuine replacement failure.
        print(
            "::warning::pr_ci_superseded_reconcile: wait budget of %ds expired "
            "before a replacement run reached the '%s' job; the gate stays red "
            "on a budget expiry, not on a replacement verdict - raise "
            "PR_CI_SUPERSEDED_WAIT_SECONDS if a PR CI run now outlasts the budget."
            % (args.wait_seconds, GATE_JOB_NAME)
        )

    print(summary)
    print("accepted=%s" % ("true" if code == 0 else "false"))
    return code


if __name__ == "__main__":
    sys.exit(main())
