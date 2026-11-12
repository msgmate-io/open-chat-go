#!/usr/bin/env python3
"""ci#1055 - close the `main <- dev` promotion hop, then verify the deployment.

The development loop used to end in a maintainer click: the merge of the
automatically maintained `[Dev promotion]` PR is what deploys ci.msgmate.io, so
"implemented" never became "live" without a human. The lead may merge that PR
itself once all required checks on it are green, and must then verify the
deployment. This module holds both decisions as pure predicates over one JSON
observation document, so they are unit-testable offline (no token, no cluster,
no network) and an agent branches on a rendered verdict instead of re-deriving
the rules from raw API payloads.

`merge` - may the promotion PR be merged right now?

  A label event (`labeled test`) spawns a second `pull_request` dispatch for the
  same SHA; the duplicate shares the `pr-ci-<pr>-<sha>` concurrency group, is
  cancelled, and its `always()`-gated `PR Test Merge Requirement` job still
  leaves a FAILURE check-run on the commit while the live run's identical check
  goes green. `mergeStateStatus` then reads BLOCKED on a green tree, which is
  the single highest-leverage autonomy defect (blocker 2). The predicate does
  what the auto-approve gate does to the raw rollup (ci#847): collapse the
  check list onto the NEWEST instance per (name, app) and decide from the
  survivors. Superseded instances are reported as `ignored`, never silently.

  Observations: {"pull_request": {"title", "head_ref", "base_ref", "draft",
  "mergeable", "merge_state_status", "head_sha"}, "required_checks": [names],
  "check_runs": [{"name", "app", "status", "conclusion", "started_at"}]}
  (GitHub's checks API spelling; `context` is accepted for `name`, `app` may be
  a string or an object with a `slug`.)

`deploy` - did the merge actually land on ci.msgmate.io?

  A merge is not acceptance: the rollout must complete, every pod must be
  ready, and the public liveness endpoint must answer 200 with the version the
  promotion shipped. A host serving an older (or unmerged-branch) VERSION is the
  ci#890 incident, so a version mismatch is a failure, not a note.

  Observations: {"rollout_complete": bool, "rollout_status": str,
  "pods_ready": n, "pods_desired": n, "expected_version": "x.y.z", "health":
  {"url", "status_code", "body"}} where `body` is the raw response text.

Exit codes - `merge`: 0 merge, 1 wait (green not reached yet), 2 refuse (the hop
is structurally wrong or a real check failed). `deploy`: 0 healthy, 1 broken.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any, Dict, List, Optional, Tuple

MERGE = "merge"
WAIT = "wait"
REFUSE = "refuse"
HEALTHY = "healthy"
BROKEN = "broken"

PROMOTION_HEAD = "dev"
PROMOTION_BASE = "main"
PROMOTION_TITLE_MARKER = "[Dev promotion]"

# A check-run in one of these states has not concluded; the commit is pending,
# not red. `cancelled` is deliberately NOT here: it is handled separately.
UNFINISHED = {"queued", "in_progress", "pending", "requested", "waiting", "expected"}
# Conclusions that do not veto a merge. `skipped`/`neutral` are what an
# informational or label-gated job reports when it did not run.
PASSING = {"success", "skipped", "neutral"}


class Verdict:
    """One decision: what to do, why, and what it deliberately disregarded."""

    __slots__ = ("decision", "reasons", "ignored")

    def __init__(self, decision: str, reasons: List[str], ignored: Optional[List[str]] = None):
        self.decision = decision
        self.reasons = reasons
        self.ignored = ignored or []

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Verdict):
            return NotImplemented
        return (self.decision, self.reasons, self.ignored) == (
            other.decision,
            other.reasons,
            other.ignored,
        )

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "Verdict(%r, %r, %r)" % (self.decision, self.reasons, self.ignored)


def _clean(value: Any) -> str:
    return str(value or "").strip()


def _app_key(check: Dict[str, Any]) -> str:
    app = check.get("app")
    if isinstance(app, dict):
        app = app.get("slug") or app.get("name") or ""
    return _clean(app)


def _name(check: Dict[str, Any]) -> str:
    return _clean(check.get("name") or check.get("context"))


def _started(check: Dict[str, Any]) -> str:
    """Sort key: ISO-8601 timestamps compare correctly as strings."""
    return _clean(check.get("started_at") or check.get("completed_at"))


def _conclusion(check: Dict[str, Any]) -> str:
    """Conclusion, tolerating the statuses API spelling (`state`)."""
    return _clean(check.get("conclusion") or check.get("state")).lower()


def newest_instances(check_runs: Any) -> Tuple[Dict[Tuple[str, str], Dict[str, Any]], List[str]]:
    """Collapse duplicate check-runs onto the newest instance per (name, app).

    Returns the surviving instances plus a line per superseded instance, so a
    stale red duplicate is visible in the verdict instead of being guessed at.
    """
    newest: Dict[Tuple[str, str], Dict[str, Any]] = {}
    superseded: List[str] = []
    for check in check_runs or []:
        if not isinstance(check, dict) or not _name(check):
            continue
        key = (_name(check), _app_key(check))
        current = newest.get(key)
        if current is None:
            newest[key] = check
            continue
        if _started(check) >= _started(current):
            loser, winner = current, check
            newest[key] = check
        else:
            loser, winner = check, current
        superseded.append(
            "%s (%s) concluded %r at %s is superseded by %r at %s"
            % (
                key[0],
                key[1] or "no app",
                _clean(loser.get("conclusion")) or _clean(loser.get("state")) or _clean(loser.get("status")) or "unknown",
                _started(loser) or "unknown time",
                _clean(winner.get("conclusion")) or _clean(winner.get("state")) or _clean(winner.get("status")) or "unknown",
                _started(winner) or "unknown time",
            )
        )
    return newest, superseded


def merge_verdict(document: Dict[str, Any]) -> Verdict:
    """May the `main <- dev` promotion PR be merged now? (see module docstring)"""
    pull_request = document.get("pull_request") or {}
    head = _clean(pull_request.get("head_ref") or pull_request.get("head"))
    base = _clean(pull_request.get("base_ref") or pull_request.get("base"))
    title = _clean(pull_request.get("title"))
    state = _clean(pull_request.get("merge_state_status") or document.get("merge_state_status")).upper()

    refusals: List[str] = []
    waits: List[str] = []

    if (head, base) != (PROMOTION_HEAD, PROMOTION_BASE):
        refusals.append(
            "refusing to merge %r -> %r: only %s -> %s is the promotion hop"
            % (head, base, PROMOTION_HEAD, PROMOTION_BASE)
        )
    if pull_request.get("draft"):
        refusals.append("the promotion PR is a draft")
    if pull_request.get("mergeable") is False or state == "DIRTY":
        refusals.append("the promotion PR has conflicts with main (mergeStateStatus=%s)" % (state or "DIRTY"))

    instances, ignored = newest_instances(document.get("check_runs"))
    by_name: Dict[str, Dict[str, Any]] = {}
    for (name, app), check in instances.items():
        # One surviving instance per name is enough for a gate; prefer the newest.
        if name not in by_name or _started(check) >= _started(by_name[name]):
            by_name[name] = check

    required = [_clean(name) for name in (document.get("required_checks") or []) if _clean(name)]
    if not required:
        required = sorted(by_name)
    if not required:
        waits.append("no required checks are known yet: nothing can be green")

    for name in required:
        check = by_name.get(name)
        if check is None:
            waits.append("required check %r has no instance on the commit yet" % name)
            continue
        conclusion = _conclusion(check)
        status = _clean(check.get("status")).lower()
        if conclusion in PASSING:
            continue
        if conclusion == "cancelled" or (not conclusion and status in UNFINISHED):
            # A cancelled attempt tested nothing (ci#1033); it is not a red gate,
            # but with no green replacement the commit is not mergeable either.
            waits.append(
                "required check %r is %s; a replacement run for the same commit must conclude"
                % (name, conclusion or status or "unconcluded")
            )
            continue
        if conclusion in UNFINISHED or status in UNFINISHED:
            waits.append("required check %r is still running (status=%s)" % (name, status or conclusion))
            continue
        refusals.append("required check %r concluded %r" % (name, conclusion or status or "unknown"))

    if state == "BEHIND":
        waits.append("the promotion branch is behind main (strict required checks); refresh it")
    elif state in {"BLOCKED", "UNSTABLE"} and not ignored:
        waits.append(
            "mergeStateStatus=%s but no duplicate check-run explains it; a green promotion needs a review approval or a check"
            % state
        )

    notes: List[str] = []
    if title and PROMOTION_TITLE_MARKER not in title:
        notes.append("the PR title lacks %r; it may not be the automated promotion PR" % PROMOTION_TITLE_MARKER)
    head_sha = _clean(pull_request.get("head_sha"))
    if head_sha:
        notes.append("merging %s/%s at %s" % (head, base, head_sha))

    if refusals:
        return Verdict(REFUSE, refusals + notes, ignored)
    if waits:
        return Verdict(WAIT, waits + notes, ignored)
    return Verdict(MERGE, ["all %d required check(s) are green" % len(required)] + notes, ignored)


def deployment_verdict(document: Dict[str, Any]) -> Verdict:
    """Did the promotion reach ci.msgmate.io as specified? (see module docstring)"""
    problems: List[str] = []
    notes: List[str] = []

    if document.get("rollout_complete") is not True:
        problems.append(
            "the rollout has not completed: %s"
            % (_clean(document.get("rollout_status")) or "no rollout status reported")
        )
    else:
        notes.append(_clean(document.get("rollout_status")) or "rollout complete")

    ready, desired = document.get("pods_ready"), document.get("pods_desired")
    if isinstance(ready, int) and isinstance(desired, int):
        if desired < 1:
            problems.append("the deployment reports %d desired pods; nothing is running" % desired)
        elif ready < desired:
            problems.append("%d of %d pods are ready" % (ready, desired))
        else:
            notes.append("%d/%d pods ready" % (ready, desired))
    else:
        problems.append("pod readiness was not reported (pods_ready/pods_desired)")

    health = document.get("health") or {}
    url = _clean(health.get("url")) or "https://ci.msgmate.io/api/v1/metrics/health"
    code = health.get("status_code")
    if code != 200:
        problems.append("%s answered %r, expected 200" % (url, code))
    body: Any = health.get("body")
    if isinstance(body, str):
        try:
            body = json.loads(body)
        except ValueError:
            body = None
    if not isinstance(body, dict):
        problems.append("%s did not return a JSON liveness document" % url)
    else:
        if _clean(body.get("status")).lower() != "ok":
            problems.append("%s reported status %r, expected 'ok'" % (url, body.get("status")))
        expected = _clean(document.get("expected_version"))
        served = _clean(body.get("version"))
        if expected and served != expected:
            problems.append("%s serves version %r, expected %r (ci#890)" % (url, served, expected))
        elif served:
            notes.append("%s serves version %s" % (url, served))

    if problems:
        return Verdict(BROKEN, problems + notes)
    return Verdict(HEALTHY, notes)


RENDER_HEADERS = {
    MERGE: "ci#1055 dev -> main promotion merge verdict",
    WAIT: "ci#1055 dev -> main promotion merge verdict",
    REFUSE: "ci#1055 dev -> main promotion merge verdict",
    HEALTHY: "ci#1055 ci.msgmate.io deployment verdict",
    BROKEN: "ci#1055 ci.msgmate.io deployment verdict",
}


def render(verdict: Verdict) -> str:
    out = [RENDER_HEADERS.get(verdict.decision, "ci#1055 promotion hop verdict"), ""]
    out.append("decision: %s" % verdict.decision.upper())
    for reason in verdict.reasons:
        out.append("  - %s" % reason)
    if verdict.ignored:
        out.append("")
        out.append("ignored superseded check-run instances (ci#847/ci#1055):")
        for line in verdict.ignored:
            out.append("  - %s" % line)
    return "\n".join(out)


EXIT_CODES = {MERGE: 0, WAIT: 1, REFUSE: 2, HEALTHY: 0, BROKEN: 1}


def _load(value: Optional[str]) -> Dict[str, Any]:
    if not value:
        return {}
    if value.startswith("@"):
        with open(value[1:], "r", encoding="utf-8") as handle:
            return json.load(handle)
    try:
        return json.loads(value)
    except ValueError:
        with open(value, "r", encoding="utf-8") as handle:
            return json.load(handle)


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("decision", choices=[MERGE, "deploy"])
    parser.add_argument(
        "--observations-json",
        help="observation document, inline JSON, `@path` or a plain path",
    )
    parser.add_argument("--log-file", help="write the rendered verdict here as well")
    args = parser.parse_args(argv)

    document = _load(args.observations_json)
    verdict = merge_verdict(document) if args.decision == MERGE else deployment_verdict(document)
    text = render(verdict)
    print(text)
    if args.log_file:
        with open(args.log_file, "a", encoding="utf-8") as handle:
            handle.write(text + "\n")
    return EXIT_CODES[verdict.decision]


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
