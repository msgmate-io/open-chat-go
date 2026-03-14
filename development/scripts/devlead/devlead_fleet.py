#!/usr/bin/env python3
"""Development-lead fleet-moderation toolkit (ci#733).

The native development lead drives the ci.msgmate.io agent fleet without an
operator watching every pass.  This toolkit implements the mechanical parts of
that pass as small, auditable, testable repo scripts:

  audit        list managed interactions with tool-call / edit counts and
               repeated-call detection, flagging **thrash** (>= 30 calls with
               0 edits, or >= 3 identical repeated calls).
  rescue       stop a flagged agent (only when its turn is live) and continue
               it with a narrow 10-15 call prompt.
  consolidate  build ``chore/consolidate-<date>-rNN`` from ``main``, fold the
               ready feature branches in, bump VERSION and open the
               consolidation PR with the full label set (dry-run by default).
  capacity     read ``/api/v1/admin/provider-admission`` and enforce the
               provider cap (never over-dispatch).
  label-audit  ci#733 Part G: strip the heavy labels from feature PRs and keep
               the basic ``test`` + ``build`` (+ optional ``test-core-only``)
               set, so a feature push cannot starve the consolidation CI in
               the GitHub Actions runner queue.

Safety: read-only unless ``--apply`` is given; every mutating command is
printed first.  No secret is ever read from a file or printed - the API token
comes from the environment (``OPEN_CHAT_API_TOKEN`` then ``CI_API_TOKEN``) and
only derived metrics / label names appear in the output.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import urllib.request
from datetime import datetime, timezone
from typing import Any, Callable, Dict, List, Optional, Sequence

DEFAULT_THRESHOLDS: Dict[str, Any] = {
    "tool_calls": 30,
    "duplicate_calls": 3,
    "rescues_per_chat": 2,
    "active_minutes": 10,
}

BASIC_LABELS = ("test", "build")
OPTIONAL_BASIC_LABELS = ("test-core-only",)
HEAVY_LABELS = ("e2e", "sandbox-test", "ai-test-mock", "ai-test-real", "deploy")
CONSOLIDATION_LABELS = (
    "test",
    "build",
    "e2e",
    "sandbox-test",
    "test-core-only",
    "ai-test-mock",
    "ai-test-real",
    "deploy",
)
REQUIRED_CONTEXT = "Run PR CI Tests and Deployments"
TERMINAL_NO_SUMMARY = ("completed", "no_usable_tool_call", "turn_timeout")
API_PREFIX = "/api/v1"
DEFAULT_BASE = "https://ci.msgmate.io"

_TOOL_CALL_RE = re.compile(r"(?:ran|used|made)\s+(\d+)\s+tool calls", re.IGNORECASE)
_RN_RE = re.compile(r"-r(\d+)$")


# --------------------------------------------------------------------------- #
# pure helpers (unit-tested in test_devlead_fleet.py)
# --------------------------------------------------------------------------- #
def _int(value: Any, default: int = 0) -> int:
    try:
        if value is None or value == "":
            return default
        return int(value)
    except (TypeError, ValueError):
        return default


def thresholds(**overrides: Any) -> Dict[str, Any]:
    merged = dict(DEFAULT_THRESHOLDS)
    merged.update({k: v for k, v in overrides.items() if v is not None})
    return merged


def interaction_ticket(view: Dict[str, Any]) -> Optional[str]:
    """Ticket reference of an interaction, from its ``issue:/ticket:`` tag."""
    for tag in view.get("tags") or []:
        text = str(tag).strip()
        if text.startswith(("issue:", "ticket:")):
            return text.split(":", 1)[1].strip() or None
        match = re.fullmatch(r"(?:ci)?#?(\d+)", text)
        if match and text.startswith(("ci#", "#")):
            return match.group(1)
    return None


def derive_metrics(view: Dict[str, Any]) -> Dict[str, int]:
    """Tool-call / edit / duplicate counts for one interaction view.

    Explicit counts on the view win (the server adds them when known).  The
    heuristic fallback parses the terminal text, so the audit still flags
    thrash on a deployment that only reports ``ran N tool calls``.
    """
    text = str(view.get("last_agent_text") or "")
    calls = _int(view.get("tool_call_count"))
    if not calls:
        match = _TOOL_CALL_RE.search(text)
        if match:
            calls = _int(match.group(1))
    edits = _int(view.get("edit_count"))
    if not edits:
        edits = len(re.findall(r"coder_(?:edit|write|patch)", text))
    return {
        "tool_calls": calls,
        "edits": edits,
        "duplicates": _int(view.get("duplicate_call_count")),
    }


def required_check_green(pr: Dict[str, Any]) -> bool:
    """True when the single required context is green (ci#683 ruleset read)."""
    for key in ("required_check_state", "required_checks"):
        value = pr.get(key)
        if isinstance(value, bool):
            return value
        if isinstance(value, str) and value:
            return value.strip().lower() in (
                "green",
                "success",
                "successful",
                "passing",
                "passed",
                "ok",
            )
    return False


def is_ready_pr(pr: Dict[str, Any], base: str = "main") -> bool:
    """A consolidation candidate: open, non-draft, mergeable, required green."""
    state = str(pr.get("state") or "OPEN").upper()
    mergeable = pr.get("mergeable")
    mergeable_ok = mergeable is True or (isinstance(mergeable, str) and mergeable.upper() == "MERGEABLE")
    pr_base = str(pr.get("base") or pr.get("baseRefName") or base)
    return (
        state == "OPEN"
        and not pr.get("draft")
        and not pr.get("isDraft")
        and mergeable_ok
        and pr_base == base
        and required_check_green(pr)
    )


def classify(
    view: Dict[str, Any],
    threshold: Optional[Dict[str, Any]] = None,
    rescues: int = 0,
) -> Dict[str, Any]:
    """Classify one managed interaction and recommend exactly one action.

    Thresholds (defaults): thrash = ``>= 30`` tool calls with ``0`` edits, or
    ``>= 3`` identical repeated calls; stalled = ``stalled`` /
    ``stall_no_first_turn``; finished-without-PR = terminal
    ``completed`` / ``no_usable_tool_call`` / ``turn_timeout`` with no open PR;
    ready-PR = open, non-draft, mergeable, required context green.
    """
    limit = thresholds(**(threshold or {}))
    metrics = derive_metrics(view)
    tags = [str(t) for t in (view.get("tags") or [])]
    state = str(view.get("state") or "").lower()
    active = bool(view.get("is_active"))
    terminal = str(view.get("turn_end_reason") or view.get("auto_continue_stop_reason") or "")
    pr = view.get("pull_request") or {}
    stalled = bool(view.get("stalled")) or str(view.get("stall_reason") or "") == "stall_no_first_turn"

    finding: Dict[str, Any] = {
        "chat_uuid": view.get("chat_uuid"),
        "bot": view.get("bot") or view.get("bot_uuid"),
        "ticket": interaction_ticket(view),
        "tags": tags,
        "state": state,
        "is_active": active,
        "metrics": metrics,
        "pull_request": pr,
        "terminal_reason": terminal,
        "last_agent_text": str(view.get("last_agent_text") or "")[:280],
        "rescues": rescues,
        "classification": "none",
        "action": "none",
        "needs_stop": False,
        "template": None,
        "reason": "",
    }

    thrash = (metrics["tool_calls"] >= limit["tool_calls"] and metrics["edits"] == 0) or (
        metrics["duplicates"] >= limit["duplicate_calls"]
    )
    if thrash:
        finding["classification"] = "thrash"
        finding["template"] = "thrash"
        detail = "%d calls / %d edits / %d repeated calls" % (
            metrics["tool_calls"],
            metrics["edits"],
            metrics["duplicates"],
        )
        if rescues >= limit["rescues_per_chat"]:
            finding["reason"] = "rescue budget exhausted (%s) - report the blocker instead" % detail
        else:
            finding["action"] = "stop_and_rescue"
            finding["needs_stop"] = active
            finding["reason"] = detail
    elif stalled:
        finding["classification"] = "stalled"
        finding["template"] = "stalled"
        if rescues == 0:
            finding["action"] = "continue"
            finding["reason"] = "never started (stall_no_first_turn) - re-continue once"
        else:
            finding["action"] = "redispatch"
            finding["reason"] = "stalled again after a continue - re-dispatch with a narrower task"
    elif pr and is_ready_pr(pr):
        finding["classification"] = "ready_pr"
        finding["action"] = "update_branch" if pr.get("behind") else "ready_for_consolidation"
        finding["reason"] = "required context green"
    elif terminal in TERMINAL_NO_SUMMARY and not active and not pr and metrics["tool_calls"] > 0:
        finding["classification"] = "finished_without_pr"
        finding["template"] = "no_summary"
        if rescues >= limit["rescues_per_chat"]:
            finding["reason"] = "rescue budget exhausted - report what is missing"
        else:
            finding["action"] = "continue"
            finding["reason"] = "terminal '%s' without a PR" % (terminal or "unknown")
    elif pr and (pr.get("draft") or pr.get("isDraft")) and terminal in TERMINAL_NO_SUMMARY:
        finding["classification"] = "pr_hygiene"
        finding["action"] = "undraft"
        finding["reason"] = "agent declared done while the PR is still a draft"
    elif pr and pr.get("behind"):
        finding["classification"] = "pr_hygiene"
        finding["action"] = "update_branch"
        finding["reason"] = "PR is BEHIND its base"
    return finding


def audit(views: Sequence[Dict[str, Any]], threshold: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
    """Audit a fleet snapshot: per-interaction findings + a pass summary."""
    rows = [classify(view, threshold) for view in views]
    summary: Dict[str, Any] = {
        "managed": len(rows),
        "active": sum(1 for r in rows if r["is_active"]),
        "thrash": sum(1 for r in rows if r["classification"] == "thrash"),
        "stalled": sum(1 for r in rows if r["classification"] == "stalled"),
        "finished_without_pr": sum(1 for r in rows if r["classification"] == "finished_without_pr"),
        "ready_pr": sum(1 for r in rows if r["classification"] == "ready_pr"),
        "rescue": sum(1 for r in rows if r["action"] == "stop_and_rescue"),
        "by_action": {},
    }
    for row in rows:
        summary["by_action"][row["action"]] = summary["by_action"].get(row["action"], 0) + 1
    return {"rows": rows, "summary": summary}


def render_rescue_prompt(template: str, finding: Dict[str, Any], steps: Optional[Sequence[str]] = None) -> str:
    """Render one narrow rescue prompt (never more than a 10-15 call ask)."""
    ticket = str(finding.get("ticket") or "the ticket")
    ref = "ci#" + ticket if ticket.isdigit() else ticket
    calls = _int((finding.get("metrics") or {}).get("tool_calls"))
    step_text = "; ".join(steps) if steps else "the minimal change the ticket asks for"
    if template == "thrash":
        return (
            "You are at %d calls with ZERO edits. Implement the minimal change for %s now: %s. "
            "Max 10-15 tool calls; commit/push a DRAFT PR; if blocked report the exact blocker. "
            "Do not re-read files." % (calls, ref, step_text)
        )
    if template == "no_summary":
        return (
            "Continue from the same workspace. Commit/push the work you already have as a DRAFT PR "
            "within 10 calls; if nothing is committed, report exactly what is missing."
        )
    if template == "stalled":
        return (
            "Your previous turn never started. Start %s now in the same workspace: %s. "
            "Max 10-15 tool calls; commit/push a DRAFT PR; if blocked report the exact blocker."
            % (ref, step_text)
        )
    raise ValueError("unknown rescue template %r" % template)


def rescue_actions(
    finding: Dict[str, Any],
    *,
    base: str,
    token: str = "",
    steps: Optional[Sequence[str]] = None,
    resume_auto_continue: bool = False,
    dry_run: bool = True,
    post: Optional[Callable[[str, Dict[str, Any]], Any]] = None,
) -> Dict[str, Any]:
    """Stop (only when the turn is live) then continue with a narrow prompt.

    Ordering is the guardrail: a continue is never sent to a live turn without
    a preceding stop.  ``resume_auto_continue`` defaults to *off* for a rescue
    turn so a repeated thrash cannot loop unattended.
    """
    chat = str(finding.get("chat_uuid") or "")
    if not chat:
        raise ValueError("finding has no chat_uuid")
    template = finding.get("template") or "thrash"
    prompt = render_rescue_prompt(template, finding, steps)
    sender = post or (lambda path, payload: api_request(path, base, token, method="POST", payload=payload))
    actions: List[Dict[str, Any]] = []
    if finding.get("needs_stop"):
        path = "%s/agents/interactions/%s/stop" % (API_PREFIX, chat)
        actions.append({"kind": "stop", "path": path})
        if not dry_run:
            sender(path, {})
    path = "%s/agents/interactions/%s/continue" % (API_PREFIX, chat)
    payload = {"message": prompt, "resume_auto_continue": bool(resume_auto_continue)}
    actions.append({"kind": "continue", "path": path, "payload": payload})
    if not dry_run:
        sender(path, payload)
    return {"chat_uuid": chat, "template": template, "prompt": prompt, "actions": actions, "dry_run": dry_run}


def capacity_decision(admission: Dict[str, Any], required: int = 1) -> Dict[str, Any]:
    """Enforce the provider cap from ``/api/v1/admin/provider-admission``."""
    cap = _int(
        admission.get("effective_cap")
        or admission.get("effective_provider_cap")
        or admission.get("provider_cap")
        or admission.get("cap")
    )
    in_flight = _int(admission.get("in_flight") or admission.get("inflight") or admission.get("active"))
    queue = _int(admission.get("queue") or admission.get("queued"))
    free = max(cap - in_flight, 0)
    allowed = cap > 0 and required <= free
    if cap <= 0:
        reason = "provider cap unknown (0) - do not dispatch"
    elif allowed and queue > 0:
        reason = "free slot available but the queue is non-empty - dispatch at most %d" % free
    elif allowed:
        reason = "%d free slot(s) of cap %d" % (free, cap)
    else:
        reason = "cap full (%d/%d in flight, queue %d) - wait for slots" % (in_flight, cap, queue)
    return {
        "allowed": allowed,
        "cap": cap,
        "in_flight": in_flight,
        "queue": queue,
        "free": free,
        "required": required,
        "reason": reason,
    }


def label_actions(labels: Sequence[str], kind: str = "feature") -> Dict[str, Any]:
    """ci#733 Part G label budget for one PR.

    A feature/bugfix PR carries ``test`` + ``build`` (optionally
    ``test-core-only``) only; the heavy labels belong to the single
    consolidation PR.
    """
    present = [str(label) for label in labels]
    if kind == "consolidation":
        return {
            "kind": kind,
            "add": [label for label in CONSOLIDATION_LABELS if label not in present],
            "remove": [],
            "offending": [],
        }
    return {
        "kind": "feature",
        "add": [label for label in BASIC_LABELS if label not in present],
        "remove": [label for label in present if label in HEAVY_LABELS],
        "offending": [label for label in present if label in HEAVY_LABELS],
    }


def plan_consolidation(
    prs: Sequence[Dict[str, Any]],
    *,
    base: str = "main",
    today: Optional[str] = None,
    one_open_at_a_time: bool = True,
    repo: str = "msgmate-io/open-chat-go",
) -> Dict[str, Any]:
    """Plan exactly one ``chore/consolidate-<date>-rNN`` batch from ``main``."""
    today = today or datetime.now(timezone.utc).strftime("%Y-%m-%d")
    consolidations = [p for p in prs if str(p.get("branch") or "").startswith("chore/consolidate-")]
    opened = [p for p in consolidations if str(p.get("state") or "OPEN").upper() == "OPEN"]
    used = [int(_RN_RE.search(str(p.get("branch") or "")).group(1)) for p in consolidations if _RN_RE.search(str(p.get("branch") or ""))]
    batch = (max(used) if used else 0) + 1
    ready = [p for p in prs if is_ready_pr(p, base) and str(p.get("branch") or "") not in consolidation_branches(prs)]
    branch = "chore/consolidate-%s-r%d" % (today, batch)
    plan: Dict[str, Any] = {
        "base": base,
        "repo": repo,
        "branch": branch,
        "batch": batch,
        "labels": list(CONSOLIDATION_LABELS),
        "pull_requests": [p.get("number") for p in ready],
        "branches": [p.get("branch") for p in ready],
        "ready": [p for p in prs if is_ready_pr(p, base)],
        "open_consolidation": opened[0].get("branch") if opened else None,
        "merge": {
            "requires": REQUIRED_CONTEXT + " green",
            "no_force_push": True,
            "rest_fallback": "PUT /repos/%s/pulls/{n}/merge (ci#683: a non-required red status never blocks)" % repo,
        },
    }
    label_args = " ".join("-l " + label for label in CONSOLIDATION_LABELS)
    plan["commands"] = [
        "git fetch origin %s" % base,
        "git checkout -B %s origin/%s" % (branch, base),
    ] + ["git merge --no-ff --no-edit origin/%s" % b for b in plan["branches"]] + [
        "bash development/scripts/bump_backend_version.sh",
        "git push -u origin %s" % branch,
        'gh pr create --repo %s --base %s --head %s --title "chore: consolidate %s (r%d)" %s'
        % (repo, base, branch, today, batch, label_args),
    ]
    if one_open_at_a_time and opened:
        plan["status"] = "wait_for_open_consolidation"
        plan["deferred_branches"] = plan["branches"]
        plan["reason"] = "%s is still open - ready PRs roll into the NEXT consolidation" % plan["open_consolidation"]
    elif not plan["branches"]:
        plan["status"] = "nothing_ready"
        plan["reason"] = "no ready PR (non-draft, mergeable, required green)"
    else:
        plan["status"] = "ready"
        plan["reason"] = "%d ready PR(s) for %s" % (len(plan["branches"]), branch)
    return plan


CONSOLIDATION_BRANCHES_EXCLUDED = ()


def consolidation_branches(prs: Sequence[Dict[str, Any]]) -> set:
    """Branch names of every existing consolidation PR (never re-merged)."""
    return {str(p.get("branch") or "") for p in prs if str(p.get("branch") or "").startswith("chore/consolidate-")}


# --------------------------------------------------------------------------- #
# API / CLI
# --------------------------------------------------------------------------- #
def api_request(
    path: str,
    base: str,
    token: str = "",
    method: str = "GET",
    payload: Optional[Dict[str, Any]] = None,
) -> Any:
    url = base.rstrip("/") + (path if path.startswith("/") else "/" + path)
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(url, data=data, method=method)
    request.add_header("Accept", "application/json")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(request, timeout=30) as response:
        body = response.read().decode("utf-8", "replace")
    return json.loads(body) if body.strip() else {}


def _token() -> str:
    return os.environ.get("OPEN_CHAT_API_TOKEN") or os.environ.get("CI_API_TOKEN") or ""


def _load_rows(path: Optional[str], base: str, token: str) -> List[Dict[str, Any]]:
    if path:
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        return data.get("rows", data) if isinstance(data, dict) else list(data)
    data = api_request(API_PREFIX + "/agents/interactions?limit=200", base, token)
    if isinstance(data, dict) and "rows" in data:
        return list(data["rows"])
    return list(data)


def _cmd_audit(args: argparse.Namespace) -> int:
    token = _token() if not args.from_file else ""
    rows = _load_rows(args.from_file, args.base, token)
    threshold = thresholds(tool_calls=args.max_calls, duplicate_calls=args.max_repeats)
    result = audit(rows, threshold)
    for row in result["rows"]:
        metrics = row["metrics"]
        print(
            "%s  %-22s %-8s %-10s calls=%-4d edits=%-3d dupes=%-3d -> %s (%s)"
            % (
                (row["chat_uuid"] or "?")[:8],
                (row["bot"] or "?")[:22],
                row["ticket"] or "-",
                row["state"],
                metrics["tool_calls"],
                metrics["edits"],
                metrics["duplicates"],
                row["action"],
                row["reason"],
            )
        )
    print(json.dumps(result["summary"], sort_keys=True))
    if args.json_out:
        _write_json(args.json_out, result)
    return 0


def _cmd_rescue(args: argparse.Namespace) -> int:
    token = _token()
    if args.from_file or args.audit_file:
        if args.audit_file:
            with open(args.audit_file, "r", encoding="utf-8") as handle:
                rows = json.load(handle)["rows"]
        else:
            rows = _load_rows(args.from_file, args.base, token)
        if args.chat:
            findings = [r for r in rows if r.get("chat_uuid") == args.chat]
        else:
            findings = [r for r in rows if r.get("action") in ("stop_and_rescue", "continue", "redispatch")]
    else:
        raise SystemExit("rescue needs --audit-file/--from-file (or an explicitly selected --chat)")
    if not findings:
        print("nothing to rescue")
        return 0
    steps = args.step or None
    for finding in findings:
        if finding.get("action") == "redispatch":
            print("chat %s: stalled twice - re-dispatch with a narrower task (no continue)" % finding["chat_uuid"])
            continue
        result = rescue_actions(
            finding,
            base=args.base,
            token=token,
            steps=steps,
            resume_auto_continue=args.resume_auto_continue,
            dry_run=not args.apply,
        )
        print("[%s] chat %s: %s" % ("apply" if args.apply else "dry-run", result["chat_uuid"], ", ".join(a["kind"] for a in result["actions"])))
        print("  prompt: " + result["prompt"])
    return 0


def _cmd_capacity(args: argparse.Namespace) -> int:
    if args.from_file:
        with open(args.from_file, "r", encoding="utf-8") as handle:
            admission = json.load(handle)
    else:
        admission = api_request(API_PREFIX + "/admin/provider-admission", args.base, _token())
    decision = capacity_decision(admission, required=args.required)
    print(json.dumps(decision, sort_keys=True))
    return 0 if (decision["allowed"] or args.report_only) else 1


def _cmd_consolidate(args: argparse.Namespace) -> int:
    if args.from_file:
        with open(args.from_file, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        prs = data.get("pull_requests", data) if isinstance(data, dict) else list(data)
    else:
        prs = _gh_json(args.repo, args.limit)
    plan = plan_consolidation(prs, base=args.base, today=args.today, repo=args.repo)
    print(json.dumps({k: v for k, v in plan.items() if k not in ("commands", "ready")}, sort_keys=True, indent=2))
    for command in plan["commands"]:
        print(command)
    if args.apply:
        if plan["status"] != "ready":
            raise SystemExit("refusing --apply: %s" % plan["reason"])
        for command in plan["commands"]:
            subprocess.run(command, shell=True, check=True)
    return 0


def _cmd_label_audit(args: argparse.Namespace) -> int:
    if args.from_file:
        with open(args.from_file, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        prs = data.get("pull_requests", data) if isinstance(data, dict) else list(data)
    else:
        prs = _gh_json(args.repo, args.limit)
    for pr in prs:
        labels = pr.get("labels") or []
        if labels and isinstance(labels[0], dict):
            labels = [entry.get("name") for entry in labels]
        kind = "consolidation" if str(pr.get("branch") or "").startswith("chore/consolidate-") else "feature"
        actions = label_actions([str(x) for x in labels], kind)
        print("PR #%s %s: add=%s remove=%s" % (pr.get("number"), kind, actions["add"], actions["remove"]))
        if args.apply and (actions["add"] or actions["remove"]):
            for label in actions["remove"]:
                subprocess.run(["gh", "pr", "edit", str(pr["number"]), "--repo", args.repo, "--remove-label", label], check=False)
            for label in actions["add"]:
                subprocess.run(["gh", "pr", "edit", str(pr["number"]), "--repo", args.repo, "--add-label", label], check=False)
    return 0


def _gh_json(repo: str, limit: int) -> List[Dict[str, Any]]:
    fields = "number,branch:headRefName,base:baseRefName,state,isDraft,mergeable,labels,url"
    out = subprocess.run(
        ["gh", "pr", "list", "--repo", repo, "--state", "open", "--limit", str(limit), "--json", fields],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(out.stdout or "[]")


def _write_json(path: str, payload: Any) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, sort_keys=True, indent=2)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="ci#733 development-lead fleet-moderation toolkit")
    parser.add_argument("--base", default=os.environ.get("OPEN_CHAT_HOST", DEFAULT_BASE))
    sub = parser.add_subparsers(dest="command", required=True)

    p = sub.add_parser("audit", help="read-only fleet audit (thrash/repeat detection)")
    p.add_argument("--from-file")
    p.add_argument("--json-out")
    p.add_argument("--max-calls", type=int, default=DEFAULT_THRESHOLDS["tool_calls"])
    p.add_argument("--max-repeats", type=int, default=DEFAULT_THRESHOLDS["duplicate_calls"])
    p.set_defaults(func=_cmd_audit)

    p = sub.add_parser("rescue", help="stop a flagged agent and continue it narrowly")
    p.add_argument("--audit-file")
    p.add_argument("--from-file")
    p.add_argument("--chat")
    p.add_argument("--step", action="append")
    p.add_argument("--resume-auto-continue", action="store_true")
    p.add_argument("--apply", action="store_true")
    p.set_defaults(func=_cmd_rescue)

    p = sub.add_parser("capacity", help="read provider-admission and enforce the cap")
    p.add_argument("--from-file")
    p.add_argument("--required", type=int, default=1)
    p.add_argument("--report-only", action="store_true")
    p.set_defaults(func=_cmd_capacity)

    p = sub.add_parser("consolidate", help="one chore/consolidate-<date>-rNN from main")
    p.add_argument("--from-file")
    p.add_argument("--today")
    p.add_argument("--repo", default="msgmate-io/open-chat-go")
    p.add_argument("--limit", type=int, default=100)
    p.add_argument("--apply", action="store_true")
    p.set_defaults(func=_cmd_consolidate)

    p = sub.add_parser("label-audit", help="ci#733 Part G label budget")
    p.add_argument("--from-file")
    p.add_argument("--repo", default="msgmate-io/open-chat-go")
    p.add_argument("--limit", type=int, default=100)
    p.add_argument("--apply", action="store_true")
    p.set_defaults(func=_cmd_label_audit)
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    return int(args.func(args) or 0)


if __name__ == "__main__":
    sys.exit(main())
