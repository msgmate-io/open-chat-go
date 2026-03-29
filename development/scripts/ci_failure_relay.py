#!/usr/bin/env python3
"""ci#734 - quiet, lead-driven CI-failure relay.

A failed PR CI run is relayed to the open-chat-go development lead **only** for
consolidation PRs (`chore/consolidate-*` targeting `main`). Feature/bugfix PRs
get no comment and no interaction, and no `issue-plan-bot` /
`plan_auto_implement` session is ever created on this path: the lead decides
whether to fix, re-dispatch or escalate.

The payload/marker/target-selection logic is pure and unit-tested in
`development/scripts/test_ci_failure_relay.py`; the CLI wires it to HTTP.

Usage (from the PR CI workflow):

    python3 development/scripts/ci_failure_relay.py relay --event <event.json>

Environment: OPEN_CHAT_HOST, OPEN_CHAT_API_TOKEN, OPEN_CHAT_LEAD_BOT,
OPEN_CHAT_LEAD_CHAT_UUID (optional), GITHUB_TOKEN (comment/labels/attempt
counting), CI_FIX_MAX_ATTEMPTS, DRY_RUN. No secret value is ever printed.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
import re
import urllib.error
import urllib.request

CONSOLIDATION_PREFIX = "chore/consolidate-"
DEFAULT_LEAD_BOT = "msgmate-coder-development-lead"
DEFAULT_HOST = "https://ci.msgmate.io"
RELAY_MARKER_NAME = "open-chat-ci-failure-relay"
LEGACY_MARKER_NAME = "open-chat-ci-failure-plan"
RELAY_MARKER_RE = re.compile(r"<!--\s*" + RELAY_MARKER_NAME + r"\s*(?P<meta>[^>]*?)-->")
LEGACY_MARKER_RE = re.compile(r"<!--\s*" + LEGACY_MARKER_NAME + r"\s*(?P<meta>[^>]*?)-->")
DRY_RUN_TRUE = ("1", "true", "yes", "on")


def should_relay(base_ref: str, head_ref: str) -> bool:
    """Mirror the workflow trigger: consolidation PRs targeting main only."""
    return ((base_ref or "").strip() == "main"
            and (head_ref or "").strip().startswith(CONSOLIDATION_PREFIX))


def _utcnow():
    return datetime.now(timezone.utc)


def build_ci_failure_payload(event: dict, attempt: int, max_attempts: int,
                             now=None) -> dict:
    """The ci#607 `ci_failure` contract (`backend/nativecoder/devlead.go`).

    Only the documented fields are emitted (plus the relay's own attempt
    counter), so the lead's `CIFailureEvent` parses it without surprises.
    """
    return {
        "schema_version": 1,
        "kind": "ci_failure",
        "provider": "github",
        "repo": event.get("repo", ""),
        "pr_number": int(event.get("pr_number") or 0),
        "branch": event.get("pr_branch") or "",
        "sha": event.get("pr_sha") or "",
        "run_url": event.get("run_url") or "",
        "failing_jobs": [str(job.get("name", "")) for job in (event.get("failed_jobs") or [])],
        "severity": event.get("severity") or "error",
        "occurred_at": (now or _utcnow()).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "attempt": int(attempt),
        "max_attempts": int(max_attempts),
    }


def build_relay_message(event: dict, payload: dict) -> str:
    """The concise lead prompt: context + decision, and the no-polling rule."""
    jobs = "\n".join("- " + name for name in payload.get("failing_jobs") or []) or "- (unknown)"
    return (
        "CI failure relay (kind=ci_failure, schema_version=1) for a consolidation PR.\n"
        "Decide whether to fix it yourself, re-dispatch a coding agent, or escalate "
        "to a maintainer; no fix-plan session was created for this failure.\n"
        "\n"
        "- repo: {repo}\n"
        "- pr_number: {pr}\n"
        "- pr_branch: {branch}\n"
        "- pr_sha: {sha}\n"
        "- run_url: {run_url}\n"
        "- attempt: {attempt}/{max_attempts}\n"
        "Failing jobs:\n{jobs}\n"
        "\n"
        "Payload:\n{payload}\n"
        "\n"
        "Do not poll or wait on CI: push the fix and stop; the next failure is relayed here."
    ).format(
        repo=event.get("repo", ""),
        pr=event.get("pr_number"),
        branch=event.get("pr_branch", ""),
        sha=event.get("pr_sha", ""),
        run_url=event.get("run_url", ""),
        attempt=payload.get("attempt"),
        max_attempts=payload.get("max_attempts"),
        jobs=jobs,
        payload=json.dumps(payload, sort_keys=True),
    )


def build_dedupe_key(event: dict) -> str:
    """Stable key so a re-run of the same (PR, head SHA) never double-dispatches."""
    sha = (event.get("pr_sha") or "")[:12]
    return "ci-failure:{repo}#{pr}:{sha}".format(
        repo=event.get("repo", ""), pr=event.get("pr_number") or 0, sha=sha)


def build_relay_marker(event: dict, attempt: int) -> str:
    """Durable, machine-readable attempt/dedupe state carried in the PR comment."""
    return "<!-- {name} pr={pr} sha={sha} attempt={attempt} -->".format(
        name=RELAY_MARKER_NAME, pr=event.get("pr_number") or 0,
        sha=event.get("pr_sha") or "", attempt=int(attempt))


def _parse_meta(meta: str) -> dict:
    parsed = {}
    for token in (meta or "").split():
        if "=" in token:
            key, value = token.split("=", 1)
            parsed[key.strip()] = value.strip()
    return parsed


def parse_relay_markers(comment_bodies) -> list:
    marked = []
    for body in comment_bodies or []:
        for match in RELAY_MARKER_RE.finditer(body or ""):
            meta = _parse_meta(match.group("meta"))
            meta["kind"] = "relay"
            marked.append(meta)
    return marked


def count_attempts(comment_bodies) -> int:
    """Relay attempts so far (legacy fix-plan markers count as prior attempts)."""
    relayed = parse_relay_markers(comment_bodies)
    highest = max([int(m.get("attempt") or 1) for m in relayed] or [0])
    legacy = len([body for body in (comment_bodies or [])
                  if LEGACY_MARKER_RE.search(body or "")
                  and not RELAY_MARKER_RE.search(body or "")])
    return highest + legacy


def next_attempt(comment_bodies) -> int:
    return count_attempts(comment_bodies) + 1


def already_relayed(comment_bodies, sha: str) -> bool:
    return any((m.get("sha") or "") == (sha or "") for m in parse_relay_markers(comment_bodies))


def pick_lead_interaction(rows, lead_bot: str = DEFAULT_LEAD_BOT, chat_uuid: str = "") -> dict | None:
    """Configured UUID wins; else the live lead interaction; else the most recent."""
    if (chat_uuid or "").strip():
        return {"chat_uuid": chat_uuid.strip(), "is_active": None, "resolved_by": "configured"}
    matches = [row for row in (rows or []) if (row.get("bot") or "") == lead_bot]
    if not matches:
        return None
    active = [row for row in matches if row.get("is_active")]
    chosen = (active or matches)[0]  # the list endpoint is ordered updated_at desc
    return {"chat_uuid": chosen.get("chat_uuid", ""),
            "is_active": bool(chosen.get("is_active")),
            "resolved_by": "active" if active else "recent"}


def _run_line(event: dict) -> str:
    failed = event.get("failed_jobs") or []
    jobs = ", ".join("`{}`".format(job.get("name", "")) for job in failed) or "(unknown)"
    return "- Run: {run}\n- Failed jobs: {jobs}".format(run=event.get("run_url", ""), jobs=jobs)


def build_comment_body(event: dict, attempt: int, max_attempts: int, lead_bot: str,
                       target: dict | None, dispatched: bool, delivery_error: str = "") -> str:
    """Exactly one comment: what failed, the run, and where it was relayed."""
    if delivery_error:
        status = "could **not** be delivered to `{}` ({}) - maintainer action required".format(lead_bot, delivery_error)
    elif dispatched:
        status = "relayed to the development lead `{}` (new interaction dispatched)".format(lead_bot)
    else:
        # A dry-run makes no API call, so without OPEN_CHAT_LEAD_CHAT_UUID there
        # is no interaction to name yet; say so instead of rendering a bare "?".
        ref = (target or {}).get("chat_uuid") or "resolved at runtime"
        status = "relayed to the live development lead `{}` interaction `{}`".format(lead_bot, ref)
    return (
        "{marker}\n"
        ":x: **PR CI run failed** on this consolidation PR (relay attempt {attempt}/{max_attempts}).\n"
        "\n"
        "{run_line}\n"
        "\n"
        "The failure {status}; the lead decides whether to fix, re-dispatch or escalate. "
        "No fix-plan session was created.\n"
    ).format(marker=build_relay_marker(event, attempt), attempt=attempt,
             max_attempts=max_attempts, run_line=_run_line(event), status=status)


def build_exhausted_comment_body(event: dict, attempt: int, max_attempts: int) -> str:
    return (
        "{marker}\n"
        ":warning: **CI fix-cycle limit reached ({attempt}/{max_attempts}).**\n"
        "\n"
        "No further relaying happens for this PR - maintainer intervention is required.\n"
    ).format(marker=build_relay_marker(event, attempt), attempt=attempt, max_attempts=max_attempts)


class OpenChatTransport:
    """Thin HTTP client for the agents/interactions endpoints (ci#540/#607)."""

    def __init__(self, host: str, token: str):
        self.base = (host or DEFAULT_HOST).rstrip("/")
        self._token = token or ""

    def _call(self, method: str, path: str, body=None):
        data = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Accept", "application/json")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        if self._token:
            request.add_header("Authorization", "Bearer " + self._token)
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return response.status, json.loads(response.read().decode("utf-8") or "{}")
        except urllib.error.HTTPError as err:
            return err.code, {"error": err.read().decode("utf-8", "replace")[:400]}

    def list_interactions(self):
        _, data = self._call("GET", "/api/v1/agents/interactions?limit=50")
        return data.get("rows") or []

    def continue_interaction(self, chat_uuid: str, message: str):
        return self._call("POST", "/api/v1/agents/interactions/{}/continue".format(chat_uuid),
                          {"message": message, "resume_auto_continue": True})

    def dispatch(self, body: dict):
        return self._call("POST", "/api/v1/agents/dispatch", body)


class GitHubTransport:
    """The GitHub side: attempt state, one comment, the blocked label."""

    def __init__(self, repo: str, token: str):
        self.repo = repo
        self._token = token or ""

    def _call(self, method: str, path: str, body=None):
        if not self._token:
            raise RuntimeError("no GitHub token configured")
        data = None if body is None else json.dumps(body).encode("utf-8")
        request = urllib.request.Request("https://api.github.com" + path, data=data, method=method)
        request.add_header("Accept", "application/vnd.github+json")
        request.add_header("X-GitHub-Api-Version", "2022-11-28")
        request.add_header("Authorization", "Bearer " + self._token)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return response.status, json.loads(response.read().decode("utf-8") or "{}")
        except urllib.error.HTTPError as err:
            return err.code, {"error": err.read().decode("utf-8", "replace")[:400]}

    def comment_bodies(self, pr_number: int):
        _, data = self._call("GET", "/repos/{}/issues/{}/comments?per_page=100".format(self.repo, pr_number))
        rows = data if isinstance(data, list) else []
        return [row.get("body") or "" for row in rows]

    def add_comment(self, pr_number: int, body: str):
        return self._call("POST", "/repos/{}/issues/{}/comments".format(self.repo, pr_number), {"body": body})

    def add_label(self, pr_number: int, label: str):
        return self._call("POST", "/repos/{}/issues/{}/labels".format(self.repo, pr_number),
                          {"labels": [label]})


def run_relay(args, env=None, gh_transport=None, chat_transport=None) -> int:
    env = dict(os.environ if env is None else env)
    with open(args.event, "r", encoding="utf-8") as handle:
        event = json.load(handle)
    if not should_relay(event.get("base_ref", ""), event.get("pr_branch", "")):
        print("skip: not a consolidation PR -> no relay, no comment")
        return 0

    lead_bot = env.get("OPEN_CHAT_LEAD_BOT") or DEFAULT_LEAD_BOT
    lead_uuid = env.get("OPEN_CHAT_LEAD_CHAT_UUID", "")
    max_attempts = int(env.get("CI_FIX_MAX_ATTEMPTS") or 3)
    dry_run = (args.dry_run or env.get("DRY_RUN", "").strip().lower() in DRY_RUN_TRUE)
    gh = gh_transport or GitHubTransport(env.get("GITHUB_REPOSITORY") or event["repo"],
                                         env.get("GITHUB_TOKEN", ""))
    chat = chat_transport or OpenChatTransport(env.get("OPEN_CHAT_HOST", DEFAULT_HOST),
                                               env.get("OPEN_CHAT_API_TOKEN", ""))

    comments = []
    if not dry_run or env.get("GITHUB_TOKEN"):
        comments = gh.comment_bodies(int(event.get("pr_number") or 0))
    if already_relayed(comments, event.get("pr_sha", "")):
        print("skip: failure already relayed for head SHA", (event.get("pr_sha") or "")[:12])
        return 0

    attempt = next_attempt(comments)
    if attempt > max_attempts:
        print("exhausted: {}/{} relay attempts".format(attempt, max_attempts))
        if not dry_run:
            gh.add_comment(int(event["pr_number"]), build_exhausted_comment_body(event, attempt, max_attempts))
            gh.add_label(int(event["pr_number"]), "ci-fix-blocked")
        return 0

    payload = build_ci_failure_payload(event, attempt, max_attempts)
    message = build_relay_message(event, payload)
    target = pick_lead_interaction([], lead_bot, lead_uuid)
    dispatched = False
    delivery_error = ""
    if dry_run:
        print("[dry-run] would relay to lead:", json.dumps(payload, sort_keys=True))
        target = pick_lead_interaction([], lead_bot, lead_uuid)
    else:
        try:
            if target is None:
                target = pick_lead_interaction(chat.list_interactions(), lead_bot, lead_uuid)
            if target:
                status, body = chat.continue_interaction(target["chat_uuid"], message)
                if status >= 300:
                    raise RuntimeError("continue -> HTTP {} {}".format(status, body))
                print("relayed to lead interaction", target["chat_uuid"], "/", target["resolved_by"])
            else:
                status, body = chat.dispatch({
                    "bot": lead_bot,
                    "message": message,
                    "tags": ["ci-failure", "repo:{}".format(event.get("repo", ""))],
                    "dedupe_key": build_dedupe_key(event),
                    "auto_share": True,
                    "require_confirmation": False,
                })
                if status >= 300:
                    raise RuntimeError("dispatch -> HTTP {} {}".format(status, body))
                dispatched = True
                target = {"chat_uuid": body.get("chat_uuid") or body.get("uuid") or "",
                          "resolved_by": "dispatch"}
                print("dispatched a new lead interaction", target["chat_uuid"])
        except Exception as err:  # noqa: BLE001 - reported, not fatal to the comment
            delivery_error = str(err)[:200]
            print("relay failed:", delivery_error, file=__import__("sys").stderr)

    body = build_comment_body(event, attempt, max_attempts, lead_bot, target, dispatched, delivery_error)
    if dry_run:
        print("[dry-run] comment body:\n" + body)
        return 0
    gh.add_comment(int(event["pr_number"]), body)
    print("posted exactly one relay comment on PR", event.get("pr_number"))
    return 1 if delivery_error else 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Relay a consolidation-PR CI failure to the development lead (ci#734).")
    parser.add_argument("command", nargs="?", default="relay", choices=["relay"])
    parser.add_argument("--event", required=True, help="CI failure event JSON (repo/pr/branch/sha/run_url/failed_jobs)")
    parser.add_argument("--dry-run", action="store_true", help="do everything except calling the APIs")
    args = parser.parse_args(argv)
    return run_relay(args)


if __name__ == "__main__":
    raise SystemExit(main())
