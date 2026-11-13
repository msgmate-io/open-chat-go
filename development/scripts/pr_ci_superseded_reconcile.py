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
expiry, 4 usage/API error, 5 yielded (this run released the concurrency group
 for a starved replacement and no acceptable replacement run exists yet). The
last stdout line is `accepted=true|false`; the line before it is
`replacement_run=<id>` (empty when no run owns the commit's verdict), so the
gate step's notice can name the run that carries the verdict.

ci#1069: the `yielded` arm used to always reject, which is what dyed the gate
red for the exact 3-4-run burst this module exists for (a `labeled`/`unlabeled`
burst cancels the in-flight run for the identical commit, the starved
replacement behind our concurrency group is then yielded to, and *nobody*
accepted). A yield is now accepted when a newer same-SHA, non-cancelled run owns
the commit's verdict: that run posts the commit's newest `PR Test Merge
Requirement` check, so this run's red gate is a stale duplicate. Ownership is
judged on what the replacement has *not given up* rather than on the gate job
being visible, because GitHub materialises a `needs`-gated job lazily - see the
ci#1069 note on `yielded_gate_carrier`. Hand-cancel, re-run and shutdown still
fail closed (they leave no such run behind), and a missing replacement keeps
exit 5.

Exit 3 is a *budget* expiry, not a replacement verdict: no replacement run ever
got to testify before the wait budget ran out. Its summary starts with the
`budget-expired:` token and `main` prints a `::warning::` annotation naming the
budget, so a stale red gate from an exhausted budget is distinguishable in logs
from a replacement run that really came back non-green (exit 1).

A replacement gate job that was itself `cancelled` cannot testify about the
commit and is skipped, but every other non-`success` verdict (including
`skipped`, `failure` and `timed_out`) is adopted and rejects the gate.

A replacement that has STARTED (`in_progress`) is also authoritative on its own:
it opens its own `PR Test Merge Requirement` check, which is newer than this
run's, and branch protection evaluates the newest check for the commit. Waiting
for it to *finish* therefore added nothing but held this run's concurrency group
for up to an hour (the observed hang, ci#931), so the reconciler accepts and
releases the group as soon as a replacement is running, deferring the verdict to
it. Only a replacement that has NOT begun executing is waited for / yielded to:
that is the ci#835 shape, where the cancelled run's red gate was the commit's
newest check precisely because no replacement ever started.

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

Self-cancellation is the shape that used to hang this reconciler (ci#1033). The
"Cancel superseded workflow runs for this PR" entry step of a NEWER run cancels
every in-progress/queued run of the same head branch, including runs for a
different commit, and those runs can be cancelled before any suite executed. For
such a run there is no replacement at all for its own SHA - the successor has a
different SHA and its own gate is the one that will testify - so waiting could
only run into the budget while holding the `pr-ci-<pr>-<sha>` concurrency group,
which is what kept the successor's gate from ever concluding (PR #9, run
42955395696 cancelled at `Profile setup`). With no sibling attempt for the SHA
after `EMPTY_LISTING_GRACE_SECONDS` and a newer run of the same workflow on the
head branch (the fingerprint of our own supersession logic), the reconciler now
accepts and defers to that run's gate. A cancellation by hand, a re-run or a
shutdown leaves no newer run behind and still fails closed.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import sys
import time
import urllib.error
import urllib.parse
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
# ci#1033: how long a cancelled run with NO sibling attempt for its SHA waits
# before concluding that nothing is ever going to testify for the commit. It
# only exists to let a sibling run that is still being created show up in the
# listing; it must stay far below the wait budget, because the whole point is
# not to hold the `pr-ci-<pr>-<sha>` concurrency group for that budget.
EMPTY_LISTING_GRACE_SECONDS = 30

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


def newer_branch_run(repo, head_branch, current_run_id, token):
    """Newest run of this workflow for `head_branch` that is newer than ours.

    This is the fingerprint of *self*-cancellation (ci#1033): the "Cancel
    superseded workflow runs for this PR" entry step of a newer run cancels every
    in-progress/queued run of the same head branch, including ones for a
    different SHA. A newer run of this branch therefore proves the cancellation
    came from our own supersession logic, and that run's `PR Test Merge
    Requirement` check is the commit's verdict.

    Returns None when nothing newer exists, so any other cancellation (a
    maintainer cancelling by hand, a re-run, a shutdown) keeps the gate closed.
    """
    if not head_branch:
        return None
    runs = [
        r
        for r in api_pages(
            repo,
            "/actions/workflows/%s/runs?branch=%s"
            % (WORKFLOW_FILE, urllib.parse.quote(head_branch, safe="")),
            token,
        )
        if (r.get("id") or 0) > current_run_id
    ]
    if not runs:
        return None
    runs.sort(key=lambda r: r.get("id") or 0, reverse=True)
    return runs[0]


# ci#1033: attempt outcomes that are *not* a verdict about the tree. A run that
# was cancelled (by the `pr-ci-<pr>-<sha>` concurrency group, by the
# cancel-superseded-runs job, or by its own yield) tested nothing, so its gate
# job must never be read as red or green: it was superseded.
SUPERSEDED_CONCLUSIONS = ("cancelled", "stale")


def is_superseded(conclusion):
    return isinstance(conclusion, str) and conclusion in SUPERSEDED_CONCLUSIONS


def gate_verdict(runs, repo, token):
    """Newest completed, non-cancelled attempt with a real gate verdict.

    ci#1033: the verdict for a commit is the gate conclusion of the *newest
    completed attempt that was not cancelled*. Measured on head e846fb8d, five
    `Run PR CI Tests and Deployments` runs existed (four cancelled, one live)
    and the required check stayed red from attempt 1 while attempt 2 ran, so a
    green tree read red. Cancelled runs/attempts are therefore never a verdict
    (neither red nor green) and are walked past; the winning run id is part of
    the returned summary so the adopted attempt is identifiable in the logs.

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
        # A cancelled *run* was superseded as a whole: its gate job cannot
        # testify even if it happened to finish first (ci#1033).
        if is_superseded(run.get("conclusion")):
            continue
        conclusion = gate_conclusion(repo, run.get("id"), token)
        if conclusion is None or is_superseded(conclusion):
            continue
        return run, conclusion
    return None, None


def yielded_gate_carrier(repo, runs, current_run_id, token):
    """Newest newer, non-superseded run that demonstrably owns the verdict.

    This is the ci#1069 acceptance test for a *yielded* run: the run this one
    handed the concurrency group to must be a real gating attempt for the same
    commit, so that its own `PR Test Merge Requirement` check - newer than ours -
    is the commit's verdict.

    A replacement that has not FINISHED can only be judged on what it has not
    given up: a gate job already concluded *superseded* (cancelled/stale) can
    never testify, and neither can the `closed`-event deployment runs of the same
    SHA - which need no special case, because they only exist once the PR is
    closed or the head moved, and both are accepted at the top of `reconcile`
    before a yield is ever considered. Job *presence* is explicitly not the test:
    GitHub materialises a `needs`-gated job lazily, so a freshly handed-over run
    lists no gate job yet (see the ci#1069 note on PR #52 below).

    A run that has FINISHED is judged on its gate conclusion, which must be a
    real verdict.

    Anything else fails closed.
    """
    newer = [r for r in runs if (r.get("id") or 0) > current_run_id]
    for run in sorted(newer, key=lambda r: r.get("id") or 0, reverse=True):
        if is_superseded(run.get("conclusion")):
            continue
        # ci#1069 regression, measured live on PR #52: run 43354888370 was already
        # `in_progress` with 27 jobs listed and NO `PR Test Merge Requirement` job
        # at all (GitHub creates a `needs`-gated job lazily; it appeared minutes
        # later). Requiring the gate job to be *visible* therefore made this
        # function return None, exit 5 dyed the gate red, and the exact 3-4-run
        # burst this module exists for still failed - for a commit whose real
        # verdict was already being produced by that very run.
        if run.get("status") != "completed":
            jobs = list(api_pages(repo, "/actions/runs/%s/jobs" % run.get("id"), token))
            gate_outcomes = [
                job.get("conclusion")
                for job in jobs
                if (job.get("name") or "") == GATE_JOB_NAME
            ]
            if gate_outcomes:
                # A gate job that already concluded superseded can never testify;
                # one that has not concluded yet will produce the verdict.
                if all(is_superseded(c) for c in gate_outcomes):
                    continue
                return run
            # No gate job listed at all. Two very different situations share that
            # shape, and they must not be treated alike:
            #   * `in_progress` with a populated job list - the gate job simply
            #     has not been materialised yet (see the ci#1069 note above).
            #     This is the live shape and it must be adopted.
            #   * anything else, in particular a run that has not started: it has
            #     produced no evidence that it gates this commit (the `closed`
            #     -event deployment runs look like this), so it fails closed.
            if run.get("status") == "in_progress" and jobs:
                return run
            continue
        conclusion = gate_conclusion(repo, run.get("id"), token)
        if conclusion is None or is_superseded(conclusion):
            continue
        return run
    return None


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


def reconcile(args, token, out=None):
    """Return (exit_code, summary) for the cancelled run described by `args`.

    `out`, when given, is a dict filled with `replacement_run` - the id of the
    run whose own `PR Test Merge Requirement` check owns the commit's verdict
    (None when no run does). `main` prints it so the gate step's notice can name
    the run it defers to instead of a vague "the replacement run" (ci#1069).
    """

    def record(run):
        if out is not None:
            out["replacement_run"] = (run or {}).get("id")

    pull = api(args.repo, "/pulls/%d" % args.pr, token)
    if pull.get("state") != "open":
        return 0, "accepted: PR #%d is %s, the cancelled run no longer gates anything." % (
            args.pr,
            pull.get("state"),
        )
    live_head = (pull.get("head") or {}).get("sha")
    live_branch = (pull.get("head") or {}).get("ref")
    if live_head and live_head != args.head_sha:
        return 0, (
            "accepted: commit %s is superseded by the live PR head %s; the "
            "cancelled run gated an obsolete commit." % (args.head_sha[:12], live_head[:12])
        )

    started = _MONOTONIC()
    deadline = started + args.wait_seconds
    while True:
        candidates = replacement_runs(args.repo, args.head_sha, args.run_id, token)
        run, conclusion = gate_verdict(candidates, args.repo, token)
        if run is not None:
            record(run)
            if conclusion == "success":
                return 0, "accepted: replacement run %s gate job concluded success." % run.get("id")
            return 1, "rejected: replacement run %s gate job concluded %s." % (
                run.get("id"),
                conclusion,
            )
        # A replacement that has STARTED is authoritative on its own: it posts its
        # own `PR Test Merge Requirement` check, which is newer than this run's and
        # therefore the one branch protection evaluates for the commit. Waiting for
        # it to *finish* only holds this run's concurrency group for up to an hour
        # (the observed hang) while adding nothing - once it runs, its gate already
        # supersedes ours, so we release the group now and defer to it. Runs that
        # have not begun executing are handled below (a starved one yields; other-
        # wise we keep waiting, because nothing is gating the commit yet).
        #
        # ci#1033: this is also why an in_progress attempt must never inherit the
        # older attempt's verdict. The commit has no verdict yet - it is *pending*,
        # not red - and head e846fb8d read red for exactly this reason. A queued
        # attempt is waited for below and, if it starves behind this run's
        # concurrency group, the group is handed over instead of stamping a red
        # verdict on a tree nobody is gating.
        running = next((r for r in candidates if r.get("status") == "in_progress"), None)
        if running is not None:
            record(running)
            return 0, (
                "accepted: newer attempt run %s for %s is in_progress (pending "
                "verdict, not a failure); its own '%s' check is newer and "
                "authoritative for this commit, so superseded run %s defers to it."
                % (running.get("id"), args.head_sha[:12], GATE_JOB_NAME, args.run_id)
            )
        starved = starved_replacement(candidates, _NOW(), args.yield_seconds)
        if starved is not None:
            code, summary = yield_group(args, starved, token)
            if code != 5:
                return code, summary
            # ci#1069: yielding is not a verdict, but it is also not a reason to
            # dye the gate red for a commit whose real verdict is being produced
            # by the run we just handed the group to. Accept iff that run is a
            # newer, non-cancelled, gate-carrying attempt for the same SHA: its
            # `PR Test Merge Requirement` check is newer than ours and therefore
            # the one branch protection evaluates, while our red check would be a
            # stale duplicate for a tree with zero executed steps. A hand-cancel,
            # a re-run or a shutdown leaves no such run behind (each keeps exit 5
            # and stays red), and so does a yield with no replacement at all.
            carrier = yielded_gate_carrier(args.repo, candidates, args.run_id, token)
            if carrier is None:
                return code, summary
            record(carrier)
            return 0, (
                "accepted: %s Replacement run %s (status %s) for %s owns the "
                "authoritative '%s' verdict for this commit, so this yielded run "
                "defers to it instead of dyeing the gate red."
                % (summary, carrier.get("id"), carrier.get("status"), args.head_sha[:12], GATE_JOB_NAME)
            )
        # ci#1033: waiting is only meaningful when something can still produce a
        # verdict. Self-cancellation makes that impossible and is the one shape
        # that used to hang the gate: this PR's own "Cancel superseded workflow
        # runs for this PR" step cancels every in-progress/queued run of the same
        # head branch, so a run can be cancelled by its own successor's entry job
        # before it gated anything, and then no replacement for the SHA ever
        # exists (the successor carries a different SHA; its own gate is the one
        # that will testify). Without this guard the gate job of the cancelled run
        # waits out the whole hour-long budget holding the
        # `pr-ci-<pr>-<sha>` concurrency group - observed on PR #9: run
        # 42955395696 was cancelled at `Profile setup`, and `PR Test Merge
        # Requirement` then sat in_progress on this step, which is exactly what
        # keeps the successor from ever concluding.
        #
        # Defer only when (a) nothing at all is scheduled for this SHA, after a
        # short grace so a sibling that is still being created is not missed, and
        # (b) a NEWER run of this same workflow exists for the PR's head branch,
        # which is the fingerprint of our own supersession logic. A cancellation
        # by hand, a re-run or a shutdown leaves no newer run behind, so it keeps
        # failing closed below.
        if not candidates:
            grace = min(args.empty_grace_seconds, args.wait_seconds)
            if _MONOTONIC() - started >= grace:
                superseder = newer_branch_run(args.repo, live_branch, args.run_id, token)
                if superseder is not None:
                    return 0, (
                        "accepted: this run was cancelled by the superseded-runs job of "
                        "newer run %s for branch %s, and no other run for %s exists "
                        "(nothing is scheduled to testify for this commit); its own "
                        "'%s' check is the verdict, so the gate defers to it instead of "
                        "waiting out the %ds budget."
                        % (
                            superseder.get("id"),
                            live_branch,
                            args.head_sha[:12],
                            GATE_JOB_NAME,
                            args.wait_seconds,
                        )
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
    parser.add_argument(
        "--yield-seconds",
        type=int,
        default=int(os.environ.get("PR_CI_SUPERSEDED_YIELD_SECONDS") or 60),
    )
    parser.add_argument(
        "--empty-grace-seconds",
        type=int,
        default=int(
            os.environ.get("PR_CI_SUPERSEDED_EMPTY_GRACE_SECONDS")
            or EMPTY_LISTING_GRACE_SECONDS
        ),
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
        out = {}
        code, summary = reconcile(args, token, out)
    except urllib.error.HTTPError as error:
        out = {}
        code, summary = 4, "error: GitHub API returned HTTP %s for %s." % (error.code, error.url)
    except (urllib.error.URLError, ValueError, KeyError, TypeError) as error:
        out = {}
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
    # ci#1069: name the run whose own gate check owns the commit's verdict, so
    # the workflow's notice points at a real run id (empty when nothing does).
    replacement = out.get("replacement_run") if isinstance(out, dict) else None
    print("replacement_run=%s" % (replacement or ""))
    print("accepted=%s" % ("true" if code == 0 else "false"))
    return code


if __name__ == "__main__":
    sys.exit(main())
