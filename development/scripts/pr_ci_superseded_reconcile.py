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

Exit codes: 0 accept, 1 replacement gate not green, 2 missing, 3 timeout,
4 usage/API error, 5 yielded (this run released the concurrency group for a
starved replacement). The last stdout line is `accepted=true|false`.

A replacement gate job that was itself `cancelled` cannot testify about the
commit and is skipped, but every other non-`success` verdict (including
`skipped`, `failure` and `timed_out`) is adopted and rejects the gate.

Waiting for a replacement is futile when the replacement cannot start. The
workflow concurrency group (`pr-ci-<number>-<head sha>`) is held by THIS run
until it completes, and the `always()`-gated gate job keeps a cancelled run
alive for the whole wait budget; a replacement that is still `pending` after
`PR_CI_SUPERSEDED_YIELD_SECONDS` is therefore starving behind us, and waiting
only extends the hold (observed as run 39407027496 keeping 39407028825 in
`pending` for 12+ minutes with an empty repository queue). Such a run cancels
itself to hand the group over (`yield_group`) and reports `accepted=false`, so
the replacement run's gate produces the verdict - like a superseded deployment
run - while a cancel that does not take effect keeps the gate red (a `409` just
means the run is already cancelling; ending this last, `always()`-gated job is
itself what lets the run complete and release the group).
"""

from __future__ import annotations

import argparse
import datetime
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
_NOW = time.time


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


def post_json(url, token, timeout=30):
    """POST to the API with an empty body (used only to cancel this run)."""
    request = urllib.request.Request(
        url,
        method="POST",
        data=b"",
        headers={
            "Authorization": "Bearer " + token,
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        body = response.read()
        return json.loads(body) if body else {}


_POST = post_json

PAGE_SIZE = 100
MAX_PAGES = 20

# Run states in which a replacement has not begun executing, so it is either
# about to start or - the case this module acts on - blocked behind this run's
# own concurrency group.
YIELD_STATUSES = ("pending", "queued", "requested", "waiting")


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


def parse_epoch(value):
    """Epoch seconds of an ISO-8601 `created_at`, or None when unparseable."""
    if not isinstance(value, str) or not value:
        return None
    try:
        parsed = datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        return None
    return parsed.replace(tzinfo=datetime.timezone.utc).timestamp()


def starved_replacement(candidates, now, min_age, statuses=YIELD_STATUSES):
    """The newer same-SHA run that cannot start while this run holds the group.

    Only a run that has not begun executing and has been in that state for
    `min_age` seconds qualifies: a replacement is allowed a moment to start, and
    a run that is already `in_progress` is making progress on its own. Runs
    without a parseable `created_at` are ignored (never a reason to yield).
    """
    if min_age <= 0:
        return None
    for run in candidates:
        if run.get("status") not in statuses:
            continue
        created = parse_epoch(run.get("created_at"))
        if created is not None and now - created >= min_age:
            return run
    return None


def cancel_run(repo, run_id, token):
    return _POST(API + "/repos/" + repo + "/actions/runs/%s/cancel" % run_id, token)


def yield_group(args, run, token):
    """Cancel this run so a starved replacement can start, and stop waiting.

    The replacement's gate is the authority from here on, and the caller reports
    `accepted=false`: if the cancel does not take effect the gate stays red
    rather than letting an untested commit look mergeable.
    """
    try:
        cancel_run(args.repo, args.run_id, token)
    except urllib.error.HTTPError as error:
        if error.code != 409:  # 409: already cancelling / already cancelled
            raise
    return 5, (
        "yielded: replacement run %s for %s is still %s behind this run's "
        "concurrency group; cancelling this run so the replacement can start."
        % (run.get("id"), args.head_sha[:12], run.get("status"))
    )


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
        starved = starved_replacement(candidates, _NOW(), args.yield_seconds)
        if starved is not None:
            return yield_group(args, starved, token)
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
    parser.add_argument(
        "--yield-seconds",
        type=int,
        default=int(os.environ.get("PR_CI_SUPERSEDED_YIELD_SECONDS") or 60),
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
