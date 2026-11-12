#!/usr/bin/env python3
"""ci#1044 - prove a deployment run used the *vault* BOT_TOKEN, not the fallback.

`check_bot_token_capabilities.py` verifies a *candidate* credential before it is
written to the deployment profile's vault. This is the other half: after a
`[Dev promotion]` deployment run, it reads that run's evidence and reports the
three acceptance criteria of ci#1044:

1. the token-source line names the **deployment vault** token (not the
   CI-secret fallback of ci#1042/ci#1043), and no `::warning::` names the vault
   item as rejected;
2. the `Resolve and verify GitHub bot token` precondition passed, i.e. it did
   not fail with `bot token cannot read <owner/repo>@<ref>`;
3. `Deploy Helm chart` and `Assert deployed VERSION matches the merged VERSION`
   are green.

Inputs are plain files, so nothing here needs the cluster, Helm or a
credential:

    gh run view <run-id> --repo msgmate-io/open-chat-go --log > /tmp/run.log
    gh run view <run-id> --repo msgmate-io/open-chat-go \\
        --json jobs > /tmp/run-jobs.json
    python3 development/scripts/verify_deployment_bot_token_rotation.py \\
        --log-file /tmp/run.log --jobs-json /tmp/run-jobs.json

The report carries verdicts and step names only - no token, no vault content -
and the script never makes a network request.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any, Dict, Iterable, List, Optional, Tuple

OK = "OK"
FAIL = "FAIL"
UNKNOWN = "UNKNOWN"

VAULT = "vault"
CI_FALLBACK = "ci-secret fallback"
NO_TOKEN_SOURCE = "not logged"

PRECONDITION_STEP = "Resolve and verify GitHub bot token"
DEPLOY_STEP = "Deploy Helm chart"
VERSION_STEP = "Assert deployed VERSION matches the merged VERSION"

VAULT_SELECTED = "using the bot token from deployment vault"
CI_SELECTED = "using the bot token from ci repository secret"
VAULT_REJECTED = "the deployment vault bot_token"
CACHE_MISS = "bot token cannot read"
ANNOTATION = ("::error::", "::warning::", "##[error]", "##[warning]")

# `msgmate-io/open-chat-go` is the deploy target the vault item must be able to
# read (see development/deployment-bot-token-capabilities.md).
DEPLOY_TARGET = "msgmate-io/open-chat-go"

# What a named rejection of the vault item actually costs, keyed by the HTTP
# status the resolver reports. 401 means the credential is dead (re-issue);
# 404/403 mean it is alive but not scoped for this repository (a different fix,
# and the one that reproduced the git exit 128 of run 42959618854).
REJECTION_GUIDANCE = (
    ("401", "the credential is invalid, expired or revoked - re-issue the vault item"),
    ("403", "the credential lacks contents:read, or the API rate limit is exhausted"),
    ("404", "the credential cannot see %s - a fine-grained token's selected "
            "repositories must include it" % DEPLOY_TARGET),
)


class Criterion:
    """One acceptance criterion. Deliberately tiny: no credential ever enters it."""

    __slots__ = ("name", "verdict", "detail")

    def __init__(self, name: str, verdict: str, detail: str) -> None:
        self.name = name
        self.verdict = verdict
        self.detail = detail

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "Criterion(%r, %r, %r)" % (self.name, self.verdict, self.detail)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Criterion):
            return NotImplemented
        return (self.name, self.verdict, self.detail) == (
            other.name,
            other.verdict,
            other.detail,
        )


def _lines(raw: Iterable[str]) -> List[str]:
    return [line.rstrip("\n") for line in raw]


def _annotated(line: str, phrase: str) -> bool:
    """True only for a real workflow annotation carrying `phrase`.

    A run log also contains the *script text* of every `run:` block, so the
    resolver's own explanatory comments repeat its failure vocabulary (e.g.
    "`bot token cannot read <owner/repo>@<ref>`"). Matching the bare phrase
    therefore reported a failing precondition for every successful run - the
    comment was read as the error. Only `::error::`/`::warning::` annotations
    are authoritative.
    """
    lowered = line.lower()
    return any(marker in lowered for marker in ANNOTATION) and phrase in lowered


def token_source(lines: Iterable[str]) -> Tuple[str, str]:
    """Which credential the resolver selected, and whether the vault one was rejected.

    Only the resolver's own summary lines are read; the credential value never
    appears in them (see scripts/resolve-github-bot-token.sh).
    """
    source = NO_TOKEN_SOURCE
    rejected = False
    guidance = ""
    for line in lines:
        lowered = line.lower()
        if VAULT_SELECTED in lowered:
            source = VAULT
        elif CI_SELECTED in lowered:
            source = CI_FALLBACK
        if _annotated(line, VAULT_REJECTED):
            rejected = True
            for status, advice in REJECTION_GUIDANCE:
                if "http %s" % status in lowered:
                    guidance = advice
                    break
    if rejected:
        return source, "the vault BOT_TOKEN was rejected and named in a warning%s" % (
            " - " + guidance if guidance else ""
        )
    if source == NO_TOKEN_SOURCE:
        return source, "the run log has no 'Using the bot token from ...' line"
    if source == CI_FALLBACK:
        return source, "the deploy ran on the CI repository secret BOT_TOKEN"
    return source, "the deploy ran on the deployment profile's vault BOT_TOKEN"


def step_conclusions(jobs: Any) -> Dict[str, str]:
    """Flatten a `gh run view --json jobs` document into step -> conclusion."""
    steps: Dict[str, str] = {}
    if isinstance(jobs, dict):
        jobs = jobs.get("jobs", [])
    for job in jobs or []:
        if not isinstance(job, dict):
            continue
        for step in job.get("steps") or []:
            if not isinstance(step, dict):
                continue
            name = str(step.get("name") or "").strip()
            if name:
                steps[name] = str(step.get("conclusion") or "").strip()
    return steps


def evaluate(log_lines: Iterable[str], steps: Dict[str, str]) -> List[Criterion]:
    lines = _lines(log_lines)
    source, detail = token_source(lines)
    if source == VAULT:
        source_verdict = OK
    elif source == NO_TOKEN_SOURCE:
        source_verdict = UNKNOWN
    else:
        source_verdict = FAIL

    precondition = steps.get(PRECONDITION_STEP, "")
    if any(_annotated(line, CACHE_MISS) for line in lines):
        precondition_verdict, precondition_detail = FAIL, (
            "the precondition failed with 'bot token cannot read <owner/repo>@<ref>'"
        )
    elif precondition == "success":
        precondition_verdict, precondition_detail = OK, "the precondition step is green"
    elif precondition:
        precondition_verdict, precondition_detail = FAIL, (
            "the precondition step concluded %r" % precondition
        )
    else:
        precondition_verdict, precondition_detail = UNKNOWN, "step not present in the run"

    missing = [name for name in (DEPLOY_STEP, VERSION_STEP) if steps.get(name) != "success"]
    if not missing:
        steps_verdict, steps_detail = OK, "both deploy assertions are green"
    elif all(steps.get(name) for name in missing):
        steps_verdict = FAIL
        steps_detail = "not green: " + ", ".join(
            "%s (%s)" % (name, steps[name]) for name in missing
        )
    else:
        steps_verdict = UNKNOWN
        steps_detail = "step not present in the run: " + ", ".join(
            name for name in missing if not steps.get(name)
        )

    return [
        Criterion("1 token source is the vault BOT_TOKEN", source_verdict, detail),
        Criterion("2 precondition assertion passed", precondition_verdict, precondition_detail),
        Criterion("3 deploy + VERSION assertions green", steps_verdict, steps_detail),
    ]


def render(criteria: List[Criterion]) -> str:
    width = max(len(c.name) for c in criteria)
    out = ["ci#1044 deployment BOT_TOKEN rotation verdict", ""]
    for criterion in criteria:
        out.append("%-*s  %-7s  %s" % (width, criterion.name, criterion.verdict, criterion.detail))
    failed = [c.name for c in criteria if c.verdict == FAIL]
    out.append("")
    if failed:
        out.append("NOT ROTATED: %d of %d criteria failed - %s" % (
            len(failed), len(criteria), "; ".join(failed)))
    elif any(c.verdict == UNKNOWN for c in criteria):
        out.append("INCONCLUSIVE: no criterion failed, but at least one could not be settled.")
    else:
        out.append("ROTATED: the deployment ran on the profile's vault BOT_TOKEN.")
    return "\n".join(out)


def _load(path: Optional[str]) -> str:
    if not path:
        return ""
    with open(path, "r", encoding="utf-8", errors="replace") as handle:
        return handle.read()


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--log-file", help="`gh run view <id> --log` output (text)")
    parser.add_argument("--jobs-json", help="`gh run view <id> --json jobs` output (JSON)")
    parser.add_argument(
        "--observations-json",
        help="offline JSON: {\"log_lines\": [...], \"steps\": {...}} or {\"jobs\": [...]}",
    )
    args = parser.parse_args(argv)

    steps: Dict[str, str] = {}
    if args.observations_json:
        document = json.loads(args.observations_json)
        log_lines = document.get("log_lines") or []
        steps = document.get("steps") or step_conclusions(document.get("jobs"))
    else:
        log_lines = _load(args.log_file).splitlines()
        raw = _load(args.jobs_json)
        steps = step_conclusions(json.loads(raw)) if raw.strip() else {}

    criteria = evaluate(log_lines, steps)
    print(render(criteria))
    return 0 if all(c.verdict == OK for c in criteria) else 1


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
