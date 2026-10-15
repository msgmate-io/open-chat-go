#!/usr/bin/env python3
"""Fail when a tracked component is not pinned to the latest upstream main.

Tracked components:

  * public git submodules  - the gitlink SHA recorded in the commit tree;
  * public integrations    - ``integrations.lock.json``;
  * private integrations   - ``.integrations/private/integrations.private.lock.json``
                             (mirrored from the private ``ci`` checkout);
  * private repos          - the ref-following repositories declared in
                             ``profile_setup.yaml`` (checked against the remote
                             tip, and against the materialized checkout when it
                             is present in the workspace).

Each pinned component is compared against the tip of its declared remote ref
(default ``main``) with ``git ls-remote``. The gate fails closed: a pin that
cannot be determined, a remote that cannot be reached or a missing private lock
all fail the run with a clear, itemised report instead of being skipped
silently.

Two explicit, reviewable escape hatches exist for deliberate lag:

  * ``pin-policy.yaml`` (tracked, public) allow-lists component ids (or
    ``fnmatch`` globs) together with a reason; and
  * the ``pin-drift-allowed`` pull-request label (``PIN_DRIFT_ALLOWED=true``).

When either is used the drift is still printed, clearly marked as allowed, so
the opt-out is always visible in the check output.

No secrets are ever printed: only repository URLs and commit SHAs appear in the
report.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Tuple

DEFAULT_REF = "main"
GIT_TIMEOUT = int(os.environ.get("PIN_CHECK_GIT_TIMEOUT", "120"))
MANIFEST_REPO_URL = "https://github.com/msgmate-io/open-chat-go.git"

PUBLIC_LOCK = "integrations.lock.json"
PRIVATE_LOCK_CANDIDATES = (
    ".integrations/private/integrations.private.lock.json",
    "development/ci/openchat/integrations.private.lock.json",
)
SETUP_SPEC = "profile_setup.yaml"
POLICY_FILE = "pin-policy.yaml"


class CheckError(RuntimeError):
    pass


@dataclass
class Component:
    ident: str
    group: str
    name: str
    remote: Optional[str]
    ref: str
    pin: Optional[str]
    pin_source: str
    pinned: bool = True
    checkout: Optional[str] = None


@dataclass
class Result:
    component: Component
    status: str  # FRESH | STALE | ALLOWED | TRACKS-MAIN | ERROR
    upstream: str = "-"
    detail: str = ""


# ---------------------------------------------------------------------------
# git helpers
# ---------------------------------------------------------------------------


def run_git(args: List[str], cwd: Optional[Path] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["git", *args],
        cwd=str(cwd) if cwd else None,
        capture_output=True,
        text=True,
        timeout=GIT_TIMEOUT,
    )


def ls_remote(remote: str, ref: str) -> Tuple[Optional[str], Optional[str]]:
    """Resolve ``refs/heads/<ref>`` on ``remote``. Returns (sha, error)."""
    try:
        proc = run_git(["ls-remote", remote, f"refs/heads/{ref}"])
    except subprocess.TimeoutExpired:
        return None, f"timed out after {GIT_TIMEOUT}s"
    except OSError as exc:
        return None, f"could not run git: {exc}"
    if proc.returncode != 0:
        return None, (proc.stderr.strip() or f"git ls-remote exited {proc.returncode}")
    output = proc.stdout.strip()
    if not output:
        return None, f"ref refs/heads/{ref} not found on remote"
    return output.split("\n")[0].split()[0], None


def gitlink_sha(root: Path, path: str) -> Optional[str]:
    proc = run_git(["ls-tree", "HEAD", path], cwd=root)
    if proc.returncode != 0 or not proc.stdout.strip():
        return None
    meta = proc.stdout.split("\t")[0].split()
    if len(meta) >= 3 and meta[0] == "160000":
        return meta[2]
    return None


def checkout_head(path: Path) -> Optional[str]:
    if not (path / ".git").exists() and not (path / ".git").is_file():
        return None
    proc = run_git(["rev-parse", "HEAD"], cwd=path)
    if proc.returncode != 0:
        return None
    return proc.stdout.strip() or None


# ---------------------------------------------------------------------------
# component discovery
# ---------------------------------------------------------------------------


def collect_submodules(root: Path) -> List[Component]:
    components: List[Component] = []
    proc = run_git(
        ["config", "-f", ".gitmodules", "--get-regexp", r"^submodule\..*\.(path|url)$"],
        cwd=root,
    )
    entries: Dict[str, Dict[str, str]] = {}
    if proc.returncode == 0:
        for line in proc.stdout.splitlines():
            key, _, value = line.partition(" ")
            match = re.match(r"^submodule\.(.+)\.(path|url)$", key)
            if not match:
                continue
            entries.setdefault(match.group(1), {})[match.group(2)] = value.strip()
    for name, fields in sorted(entries.items()):
        path = fields.get("path") or name
        components.append(
            Component(
                ident=f"submodule:{path}",
                group="public submodule",
                name=path,
                remote=fields.get("url"),
                ref=DEFAULT_REF,
                pin=gitlink_sha(root, path),
                pin_source=f"gitlink in HEAD tree ({path})",
            )
        )
    return components


def collect_integrations(
    lock_path: Path, ident_prefix: str, group: str, pin_source: str
) -> List[Component]:
    if not lock_path.exists():
        raise CheckError(f"lockfile not found: {lock_path}")
    try:
        data = json.loads(lock_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError) as exc:
        raise CheckError(f"could not read {lock_path}: {exc}") from exc
    integrations = data.get("integrations")
    if not isinstance(integrations, dict):
        raise CheckError(f"{lock_path} has no 'integrations' mapping")
    components = []
    for integ_id, entry in sorted(integrations.items()):
        if not isinstance(entry, dict):
            raise CheckError(f"{lock_path}: entry {integ_id!r} is not a mapping")
        components.append(
            Component(
                ident=f"{ident_prefix}:{integ_id}",
                group=group,
                name=integ_id,
                remote=entry.get("repo"),
                ref=str(entry.get("ref") or DEFAULT_REF),
                pin=entry.get("commit"),
                pin_source=pin_source,
            )
        )
    return components


def load_yaml(path: Path) -> dict:
    try:
        import yaml  # type: ignore
    except ImportError:
        return _parse_simple_yaml(path)
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle) or {}


def _parse_simple_yaml(path: Path) -> dict:
    """Minimal fallback parser for the flat repositories block we need."""
    repos: Dict[str, Dict[str, str]] = {}
    current: Optional[str] = None
    in_repos = False
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.split("#", 1)[0].rstrip()
        if not line.strip():
            continue
        if re.match(r"^repos:\s*$", line):
            in_repos = True
            continue
        if in_repos and re.match(r"^[A-Za-z]", line):
            break
        repo_match = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", line)
        if in_repos and repo_match:
            current = repo_match.group(1)
            repos[current] = {}
            continue
        field_match = re.match(r"^    (repo|path|ref):\s*(.+?)\s*$", line)
        if in_repos and current and field_match:
            repos[current][field_match.group(1)] = field_match.group(2).strip("'\"")
    return {"repos": repos}


def collect_private_repos(root: Path) -> List[Component]:
    spec_path = root / SETUP_SPEC
    if not spec_path.exists():
        return []
    spec = load_yaml(spec_path)
    repos = spec.get("repos") or {}
    if not isinstance(repos, dict):
        raise CheckError(f"{SETUP_SPEC} 'repos' must be a mapping")
    components = []
    seen = set()
    for repo_id, entry in sorted(repos.items()):
        if not isinstance(entry, dict):
            continue
        remote = entry.get("repo")
        path = entry.get("path")
        # `ci` and `ci_fragment` (and any future aliases) can point at the same
        # checkout/remote; report each (remote, checkout) pair only once.
        dedupe_key = (remote, str(path) if path else None)
        if dedupe_key in seen:
            continue
        seen.add(dedupe_key)
        components.append(
            Component(
                ident=f"private-repo:{repo_id}",
                group="private repo",
                name=repo_id,
                remote=remote,
                ref=str(entry.get("ref") or DEFAULT_REF),
                pin=None,
                pin_source=f"{SETUP_SPEC} (ref-following)",
                pinned=False,
                checkout=str(path) if path else None,
            )
        )
    return components


# ---------------------------------------------------------------------------
# policy
# ---------------------------------------------------------------------------


@dataclass
class Policy:
    rules: List[Dict[str, str]] = field(default_factory=list)

    @classmethod
    def load(cls, path: Path) -> "Policy":
        if not path.exists():
            return cls()
        data = load_yaml(path)
        rules = []
        for entry in data.get("allow") or []:
            if isinstance(entry, dict) and entry.get("id"):
                rules.append(
                    {"id": str(entry["id"]), "reason": str(entry.get("reason", ""))}
                )
        return cls(rules=rules)

    def reason(self, ident: str) -> Optional[str]:
        for rule in self.rules:
            pattern = rule["id"]
            if ident == pattern or fnmatch.fnmatch(ident, pattern):
                return rule.get("reason") or "listed in pin-policy.yaml"
        return None


# ---------------------------------------------------------------------------
# evaluation
# ---------------------------------------------------------------------------


def _maybe_allow(result: Result, label_allowed: bool, policy: Policy) -> Result:
    """Turn a STALE result into ALLOWED when an escape hatch covers it."""
    if result.status != "STALE":
        return result
    reason = policy.reason(result.component.ident)
    if label_allowed:
        reason = (reason + "; " if reason else "") + "pin-drift-allowed label"
    if reason:
        return Result(
            result.component, "ALLOWED", upstream=result.upstream, detail=reason
        )
    return result


def evaluate(component: Component, root: Path, label_allowed: bool, policy: Policy) -> Result:
    if component.pinned and not component.pin:
        return Result(
            component,
            "ERROR",
            detail=(
                "pin could not be determined "
                f"({component.pin_source}); refusing to skip silently"
            ),
        )
    if not component.remote:
        return Result(component, "ERROR", detail="repository URL is missing")

    upstream, error = ls_remote(component.remote, component.ref)
    if error or not upstream:
        return Result(component, "ERROR", detail=f"could not resolve {component.ref}: {error}")

    if not component.pinned:
        detail = f"ref-following, tracks origin/{component.ref}"
        checkout = component.checkout
        if checkout:
            head = checkout_head(root / checkout)
            if head and head != upstream:
                return _maybe_allow(
                    Result(
                        component,
                        "STALE",
                        upstream=upstream,
                        detail=f"materialized checkout {checkout} at {head[:12]} != upstream",
                    ),
                    label_allowed,
                    policy,
                )
            if head:
                detail = f"materialized checkout {checkout} matches upstream"
        return Result(component, "TRACKS-MAIN", upstream=upstream, detail=detail)

    if component.pin == upstream:
        return Result(component, "FRESH", upstream=upstream)

    return _maybe_allow(
        Result(
            component,
            "STALE",
            upstream=upstream,
            detail="pin is behind upstream main; update the pin or opt out explicitly",
        ),
        label_allowed,
        policy,
    )


# ---------------------------------------------------------------------------
# reporting
# ---------------------------------------------------------------------------


def render_table(results: List[Result]) -> str:
    headers = ("component", "pin", "upstream", "status", "detail")
    rows = []
    for result in results:
        component = result.component
        pin = component.pin[:12] if component.pin else "-"
        if result.status == "ERROR" and not component.pin:
            pin = "<unresolved>"
        rows.append(
            (
                component.ident,
                pin,
                result.upstream[:12],
                result.status,
                result.detail,
            )
        )
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(cell))
    lines = []
    header_line = "  ".join(h.ljust(widths[i]) for i, h in enumerate(headers))
    lines.append(header_line)
    lines.append("-" * len(header_line))
    for row in rows:
        lines.append("  ".join(cell.ljust(widths[i]) for i, cell in enumerate(row)))
    return "\n".join(lines)


def render_summary(results: List[Result], label_allowed: bool, policy: Policy) -> str:
    stale = [r for r in results if r.status == "STALE"]
    allowed = [r for r in results if r.status == "ALLOWED"]
    errors = [r for r in results if r.status == "ERROR"]
    lines = ["## Pin Freshness Gate", ""]
    lines.append(f"Tracked components: {len(results)}")
    lines.append(f"Fresh/matching: {sum(1 for r in results if r.status in ('FRESH', 'TRACKS-MAIN'))}")
    lines.append(f"Allowed drift: {len(allowed)}")
    lines.append(f"Stale: {len(stale)}")
    lines.append(f"Errors: {len(errors)}")
    if label_allowed:
        lines.append("")
        lines.append("> `pin-drift-allowed` label present: all drift is printed and tolerated for this run.")
    if policy.rules:
        lines.append("")
        lines.append(f"> `pin-policy.yaml` allow-list active ({len(policy.rules)} rule(s)).")
    if stale or errors:
        lines.append("")
        lines.append("Blocking components:")
        for result in stale + errors:
            lines.append(f"- `{result.component.ident}`: {result.detail}")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# entrypoint
# ---------------------------------------------------------------------------


def build_components(root: Path) -> List[Component]:
    components: List[Component] = []
    components.extend(collect_submodules(root))
    components.extend(
        collect_integrations(
            root / PUBLIC_LOCK,
            "public-integration",
            "public integration",
            PUBLIC_LOCK,
        )
    )

    private_lock = next(
        (root / rel for rel in PRIVATE_LOCK_CANDIDATES if (root / rel).exists()), None
    )
    if private_lock is None:
        raise CheckError(
            "private lock not found (looked for {}); run "
            "`openchat-integrations setup --profile full-ci` first"
            .format(", ".join(PRIVATE_LOCK_CANDIDATES))
        )
    components.extend(
        collect_integrations(
            private_lock,
            "private-integration",
            "private integration",
            str(private_lock.relative_to(root)),
        )
    )
    components.extend(collect_private_repos(root))
    return components


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="repository root (default: cwd)")
    parser.add_argument(
        "--policy", default=None, help=f"allow-list file (default: <root>/{POLICY_FILE})"
    )
    parser.add_argument(
        "--allow-drift",
        action="store_true",
        default=os.environ.get("PIN_DRIFT_ALLOWED", "").lower() in ("1", "true", "yes"),
        help="tolerate all drift (the pin-drift-allowed label)",
    )
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    policy = Policy.load(Path(args.policy) if args.policy else root / POLICY_FILE)

    components = build_components(root)

    with ThreadPoolExecutor(max_workers=min(8, max(1, len(components)))) as pool:
        results = list(
            pool.map(lambda c: evaluate(c, root, args.allow_drift, policy), components)
        )

    print("Pin freshness gate: compare every tracked pin against its upstream ref")
    print(f"Repository: {os.environ.get('GITHUB_REPOSITORY', MANIFEST_REPO_URL)}")
    print(f"Escape hatch: pin-drift-allowed label = {args.allow_drift}")
    print(render_table(results))
    print()

    stale = [r for r in results if r.status == "STALE"]
    errors = [r for r in results if r.status == "ERROR"]
    allowed = [r for r in results if r.status == "ALLOWED"]

    if allowed:
        print("Allowed drift (visible opt-out):")
        for result in allowed:
            print(f"  - {result.component.ident}: {result.detail}")
        print()

    summary = render_summary(results, args.allow_drift, policy)
    step_summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if step_summary:
        try:
            with open(step_summary, "a", encoding="utf-8") as handle:
                handle.write(summary + "\n")
        except OSError:
            pass

    if errors:
        print("::error::Pin freshness gate could not determine some pins (fail closed).")
        return 1
    if stale:
        print(
            "::error::Pin freshness gate: {} tracked component(s) are not at the "
            "latest upstream main.".format(len(stale))
        )
        return 1
    if allowed:
        print(
            "Pin freshness gate satisfied with {} explicitly allowed drift "
            "item(s); see the report above.".format(len(allowed))
        )
        return 0
    print("Pin freshness gate satisfied: every tracked pin is at the latest upstream main.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except CheckError as exc:
        print(f"::error::{exc}")
        sys.exit(1)
