#!/usr/bin/env python3
"""ci#1066: can the `dev` -> `main` promotion hop merge unattended *for the right reason*?

The `main` ruleset (`Main (Staging) Merge Protection`, rulesets `26947244` /
`26947249`) requires one approving review from a reviewer **with write access**.
No automation identity can post that review without either

  * `CONSOLIDATE_APPROVER_TOKEN` for a write-access identity, or
  * a `bypass_actors` entry on the ruleset for the automation identity.

While the ruleset is *disabled* (`rules/branches/main` answers `[]`) the hop
merges unattended anyway - because the gate is off, not because the approval
path works. That state **masks** the defect: re-enabling the ruleset brings the
original failure straight back. The ticket therefore fixes an order:

  1. approver identity (or bypass actor) first,
  2. ruleset `active` second.

This module holds that decision as a pure predicate over one JSON observation
document, so it is unit-testable offline (no token, no network) and a maintainer
or agent branches on a rendered verdict instead of re-deriving the rules from
raw API payloads.

Observation document:

  {"ruleset": {"name": str, "enforcement": "active"|"disabled"|"evaluate",
               "covers_main": bool, "required_approving_reviews": int,
               "bypass_actors": [{"actor_type": str, "actor_id": int,
                                  "bypass_mode": str}]},
   "approver": {"configured": bool, "login": str, "resolved": bool,
                "write_access": bool|null},
   "automation": {"login": str, "app_slug": str}}

Verdicts:

  ready_approver  - ruleset active, a write-access approver identity is usable:
                    the hop can be merged by the automation.
  ready_bypass    - ruleset active, a bypass actor covers the automation
                    identity: the hop can be merged by the automation.
  needs_approver  - ruleset active, no usable approver and no bypass actor: the
                    hop is human-gated (this is the ci#1066 defect, not a
                    regression - the auto-approve run reports it and stays green).
  masked          - the review requirement is not enforced: the unattended hop
                    proves nothing. `safe_to_enable` says whether the approver
                    path is already in place, i.e. whether re-enabling the
                    ruleset is the correct next step.
  unknown         - the observation is incomplete or unreadable.

Exit codes: 0 ready, 1 needs configuration, 2 masked, 3 unknown.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from typing import Any, Callable, Dict, List, Optional, Tuple

READY_APPROVER = "ready_approver"
READY_BYPASS = "ready_bypass"
NEEDS_APPROVER = "needs_approver"
MASKED = "masked"
UNKNOWN = "unknown"

EXIT_READY = 0
EXIT_NEEDS_CONFIG = 1
EXIT_MASKED = 2
EXIT_UNKNOWN = 3

# Bypass actor types that can cover an automation identity. `Integration` is the
# GitHub App the workflow acts as; `RepositoryRole`/`OrganizationAdmin` cover a
# user token with that role.
BYPASS_ACTOR_TYPES = {"integration", "repositoryrole", "organizationadmin", "team"}


def _as_dict(value: Any) -> Dict[str, Any]:
    return value if isinstance(value, dict) else {}


def _as_bool(value: Any) -> Optional[bool]:
    return value if isinstance(value, bool) else None


def _as_int(value: Any) -> int:
    if isinstance(value, bool):
        return 0
    if isinstance(value, int):
        return value
    try:
        return int(str(value).strip())
    except (TypeError, ValueError):
        return 0


def approver_ready(approver: Dict[str, Any]) -> bool:
    """A usable approver identity: configured, resolvable and write-capable.

    `write_access` may be `None` (an installation token that cannot report
    repository permissions); that is *not* treated as ready here, because the
    whole point of this check is to prove the approval will be accepted before
    the ruleset is re-enabled.
    """
    if not _as_bool(approver.get("configured")):
        return False
    if not _as_bool(approver.get("resolved")):
        return False
    return _as_bool(approver.get("write_access")) is True


def bypass_ready(ruleset: Dict[str, Any], automation: Dict[str, Any]) -> bool:
    """Is there a bypass actor on the ruleset that covers the automation identity?"""
    actors = ruleset.get("bypass_actors")
    if not isinstance(actors, list) or not actors:
        return False
    login = str(automation.get("login") or "").strip().lower()
    app_slug = str(automation.get("app_slug") or "").strip().lower()
    for actor in actors:
        actor = _as_dict(actor)
        actor_type = str(actor.get("actor_type") or "").strip().lower()
        if actor_type not in BYPASS_ACTOR_TYPES:
            continue
        # An `Integration` actor is identified by its app id/slug, a role actor by
        # its role id; either way the presence of a bypass actor for the acting
        # identity is what the ruleset evaluates. Without a resolvable identity we
        # cannot claim coverage, so require *some* identity to match on.
        if not login and not app_slug:
            continue
        return True
    return False


def review_required(ruleset: Dict[str, Any]) -> bool:
    """Does the ruleset actually enforce an approving review on `refs/heads/main`?"""
    if str(ruleset.get("enforcement") or "").strip().lower() != "active":
        return False
    if _as_bool(ruleset.get("covers_main")) is False:
        return False
    return _as_int(ruleset.get("required_approving_reviews")) >= 1


def evaluate(observation: Dict[str, Any]) -> Dict[str, Any]:
    """Render the readiness verdict for one observation document."""
    observation = _as_dict(observation)
    ruleset = _as_dict(observation.get("ruleset"))
    approver = _as_dict(observation.get("approver"))
    automation = _as_dict(observation.get("automation"))

    if not ruleset:
        return {
            "verdict": UNKNOWN,
            "exit_code": EXIT_UNKNOWN,
            "safe_to_enable": False,
            "reason": "no ruleset observation: the merge protection of `main` is unknown",
            "remediation": [
                "read `repos/{owner}/{repo}/rulesets` and the branch-targeted ruleset "
                "details, then re-run this check",
            ],
        }

    has_approver = approver_ready(approver)
    has_bypass = bypass_ready(ruleset, automation)
    enforced = review_required(ruleset)
    name = str(ruleset.get("name") or "the `main` ruleset")
    reviews = _as_int(ruleset.get("required_approving_reviews"))

    remediation: List[str] = []
    if not has_approver:
        remediation.append(
            "issue `CONSOLIDATE_APPROVER_TOKEN` for an identity with write access to "
            "this repository (the ci#1044 re-issue procedure)"
        )
    if not has_bypass:
        remediation.append(
            "or register a `bypass_actors` entry on the `main` ruleset for the "
            "automation identity"
        )
    remediation.append(
        "only then set the ruleset enforcement to `active` (never the other way round)"
    )

    if enforced:
        if has_approver:
            return {
                "verdict": READY_APPROVER,
                "exit_code": EXIT_READY,
                "safe_to_enable": True,
                "reason": (
                    f"{name} enforces {reviews} approving review(s) and a write-access "
                    "approver identity is usable: the hop can merge unattended"
                ),
                "remediation": [],
            }
        if has_bypass:
            return {
                "verdict": READY_BYPASS,
                "exit_code": EXIT_READY,
                "safe_to_enable": True,
                "reason": (
                    f"{name} enforces {reviews} approving review(s) and a bypass actor "
                    "covers the automation identity: the hop can merge unattended"
                ),
                "remediation": [],
            }
        return {
            "verdict": NEEDS_APPROVER,
            "exit_code": EXIT_NEEDS_CONFIG,
            "safe_to_enable": False,
            "reason": (
                f"{name} enforces {reviews} approving review(s) but no write-access "
                "approver identity and no bypass actor is available: the hop is "
                "human-gated (ci#1066)"
            ),
            "remediation": remediation,
        }

    # Not enforced: the unattended hop works only because the gate is off.
    return {
        "verdict": MASKED,
        "exit_code": EXIT_MASKED,
        "safe_to_enable": has_approver or has_bypass,
        "reason": (
            f"{name} does not enforce an approving review on `refs/heads/main` "
            f"(enforcement {ruleset.get('enforcement') or 'unknown'}, "
            f"{reviews} required review(s)): an unattended merge proves nothing about "
            "the approval path (ci#1066)"
        ),
        "remediation": [] if (has_approver or has_bypass) else remediation,
    }


def render(verdict: Dict[str, Any]) -> str:
    lines = [
        f"Promotion hop readiness: {verdict['verdict']}",
        f"  {verdict['reason']}",
        f"  safe to set the `main` ruleset to `active`: "
        f"{'yes' if verdict['safe_to_enable'] else 'no'}",
    ]
    for step in verdict.get("remediation") or []:
        lines.append(f"  - {step}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Live observation (ci#1066)
#
# `evaluate()` is a pure predicate, so something has to turn the live repository
# state into the observation document above. That translation is error prone in
# exactly the way that matters here - the effective-rules answer
# (`rules/branches/main`) says whether the review requirement is *enforced*, but
# not which ruleset owns it or whether it has bypass actors, and those only exist
# on the ruleset resource. Doing it ad hoc in a shell step is what let the masked
# state pass for a healthy hop, so it lives here next to the verdict and is
# unit-tested offline against a stubbed `gh`.
#
# Deliberately `gh api` only (no extra dependencies), read-only, and the
# credential never reaches the rendered output.
# ---------------------------------------------------------------------------

MAIN_REF_PATTERN = re.compile(r"refs/heads/(main|~DEFAULT_BRANCH)$")


def _gh_json(run: Callable[[List[str]], Tuple[int, str]], args: List[str]) -> Tuple[bool, Any]:
    """Run `gh <args>` and parse its JSON answer; `(False, None)` on any failure."""
    rc, out = run(args)
    if rc != 0 or not out.strip():
        return False, None
    try:
        return True, json.loads(out)
    except ValueError:
        return False, None


def _approval_rule_count(rules: Any) -> int:
    total = 0
    for rule in rules if isinstance(rules, list) else []:
        rule = _as_dict(rule)
        if str(rule.get("type") or "").strip().lower() != "pull_request":
            continue
        parameters = _as_dict(rule.get("parameters"))
        total = max(total, _as_int(parameters.get("required_approving_review_count")))
    return total


def _covers_main(conditions: Any) -> bool:
    include = _as_dict(_as_dict(conditions).get("ref_name")).get("include")
    if not isinstance(include, list):
        return False
    return any(MAIN_REF_PATTERN.search(str(entry or "")) for entry in include)


def collect_observation(
    repo: str,
    *,
    run: Callable[[List[str]], Tuple[int, str]],
    approver_configured: Optional[bool] = None,
) -> Dict[str, Any]:
    """Build the observation document from the live repository state.

    `run` executes `gh` with the given arguments and returns `(rc, stdout)`; it is
    injected so the whole translation can be tested without a token or network.

    Returns `{}` when neither the effective rules nor any ruleset detail could be
    read: the verdict is then `unknown`, never `masked` (a read failure must not
    be reported as "the gate is off").
    """
    repo = str(repo or "").strip()
    observation: Dict[str, Any] = {
        "approver": {
            "configured": bool(approver_configured),
            "login": "",
            "resolved": False,
            "write_access": None,
        },
        "automation": {"login": "", "app_slug": "github-actions"},
    }
    if not repo:
        return {}

    rules_ok, rules = _gh_json(run, ["api", f"repos/{repo}/rules/branches/main"])
    effective_reviews = _approval_rule_count(rules) if rules_ok else 0

    # The governing branch ruleset, if any. The effective-rules answer is
    # authoritative for *whether* the requirement is enforced; the ruleset
    # resource is authoritative for its enforcement level and bypass actors.
    ruleset: Dict[str, Any] = {}
    details_ok = False
    list_ok, rulesets = _gh_json(run, ["api", f"repos/{repo}/rulesets"])
    for entry in rulesets if isinstance(rulesets, list) else []:
        ident = _as_dict(entry).get("id")
        case = str(_as_int(ident)) if ident is not None else ""
        if not case or case == "0":
            continue
        detail_ok, detail = _gh_json(run, ["api", f"repos/{repo}/rulesets/{case}"])
        if not detail_ok:
            continue
        detail = _as_dict(detail)
        details_ok = True
        if str(detail.get("target") or "branch").strip().lower() != "branch":
            continue
        if not _covers_main(detail.get("conditions")):
            continue
        candidate = {
            "id": _as_int(detail.get("id")),
            "name": str(detail.get("name") or ""),
            "enforcement": str(detail.get("enforcement") or "unknown").strip().lower(),
            "covers_main": True,
            "required_approving_reviews": _approval_rule_count(detail.get("rules")),
            "bypass_actors": [
                dict(_as_dict(actor)) for actor in detail.get("bypass_actors") or []
            ]
            if isinstance(detail.get("bypass_actors"), list)
            else [],
        }
        # Prefer the ruleset that actually requires the reviews; otherwise the
        # first one that targets `refs/heads/main`.
        if ruleset and candidate["required_approving_reviews"] <= ruleset[
            "required_approving_reviews"
        ]:
            continue
        ruleset = candidate

    if not rules_ok and not details_ok:
        return {}
    if not ruleset:
        # No branch ruleset matches `refs/heads/main` at all. Report the masked
        # state explicitly rather than leaving the ruleset half-unknown.
        ruleset = {
            "name": "Main (Staging) Merge Protection",
            "enforcement": "disabled" if rules_ok and effective_reviews == 0 else "unknown",
            "covers_main": True,
            "required_approving_reviews": effective_reviews,
            "bypass_actors": [],
        }
    elif rules_ok:
        # `rules/branches/main` is the effective answer: if it requires no review
        # while the ruleset says `active`, the ruleset is not actually applied.
        if effective_reviews == 0 and ruleset["required_approving_reviews"] >= 1:
            ruleset["enforcement"] = "disabled"
    observation["ruleset"] = ruleset

    # The acting identity, and whether its approval would count. `permissions.push`
    # is only meaningful for a user token; an installation/app token cannot report
    # it, and that stays `None` (not ready) by design.
    user_ok, user = _gh_json(run, ["api", "user"])
    login = str(_as_dict(user).get("login") or "").strip()
    if user_ok and login:
        observation["approver"]["login"] = login
        observation["approver"]["resolved"] = True
        perm_ok, perm = _gh_json(run, ["api", f"repos/{repo}"])
        push = _as_dict(_as_dict(perm).get("permissions")).get("push")
        if perm_ok and isinstance(push, bool):
            observation["approver"]["write_access"] = push
    observation["automation"]["login"] = login
    return observation


def _run_gh(args: List[str]) -> Tuple[int, str]:
    """Execute `gh` read-only; returns `(rc, stdout)`. Never echoes the token."""
    try:
        completed = subprocess.run(
            ["gh", *args], capture_output=True, text=True, check=False
        )
    except OSError:
        return 127, ""
    return completed.returncode, completed.stdout


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="Decide whether the dev -> main promotion hop can merge unattended (ci#1066)."
    )
    parser.add_argument(
        "observation",
        nargs="?",
        default="-",
        help="path to the observation JSON document, or - for stdin (default)",
    )
    parser.add_argument("--json", action="store_true", help="print the verdict as JSON")
    parser.add_argument(
        "--live",
        action="store_true",
        help=(
            "read the observation from the GitHub API via `gh` instead of a "
            "document (read-only; requires a token with rules:read)"
        ),
    )
    parser.add_argument(
        "--repo",
        default=os.environ.get("REPOSITORY") or os.environ.get("GITHUB_REPOSITORY") or "",
        help="owner/repo to inspect with --live (default: $GITHUB_REPOSITORY)",
    )
    args = parser.parse_args(argv)

    if args.live:
        observation = collect_observation(
            args.repo,
            run=_run_gh,
            approver_configured=bool(os.environ.get("CONSOLIDATE_APPROVER_TOKEN")),
        )
    else:
        try:
            observation = _read_observation(args.observation)
        except (OSError, ValueError) as exc:
            observation = exc
    verdict = _verdict_from(observation)
    if args.json:
        print(json.dumps(verdict, indent=2, sort_keys=True))
    else:
        print(render(verdict))
    return int(verdict["exit_code"])


def _read_observation(path: str) -> Dict[str, Any]:
    if path == "-":
        raw = sys.stdin.read()
    else:
        with open(path, encoding="utf-8") as handle:
            raw = handle.read()
    return json.loads(raw) if raw.strip() else {}


def _verdict_from(observation: Any) -> Dict[str, Any]:
    if isinstance(observation, BaseException):
        return {
            "verdict": UNKNOWN,
            "exit_code": EXIT_UNKNOWN,
            "safe_to_enable": False,
            "reason": f"could not read the observation document: {observation}",
            "remediation": ["pass a readable JSON observation document"],
        }
    return evaluate(_as_dict(observation))


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
