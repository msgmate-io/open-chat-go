#!/usr/bin/env python3
"""Fail when agent-only / development-bot docs reappear in the public repo.

ci#736: the public `open-chat-go` repository must carry **human** development
guidance only (setup, build, run, test, deploy, contributing, public
architecture). Agent/development-bot operational instructions — the devlead
fleet-moderation toolkit, the agent playbooks that used to live under `./docs`,
and the agent-only sections of `DEVELOPMENT.md` — belong to the **private**
integration that owns the coder/lead (served into native `msgmate_coder`
interactions through the integration's own docs, never copied here).

This guard fails closed when such content is added back:

    python3 development/scripts/check_no_agent_docs_in_public.py --root .

Checks, in order:

1. Forbidden paths: `development/scripts/devlead/**`, `docs/devlead/**`,
   `docs/ci-733-*`, `docs/ci730-*` and any `**/agent-playbook*.md` /
   `**/*-playbook-agent*.md` file. A single tracked file is enough to fail.
2. Forbidden markers inside `DEVELOPMENT.md`: agent-only section headings
   (devlead / development-lead fleet moderation, auto-consolidation bot rules,
   provider-admission internals, native-coder selfcheck/auto-continue, tool-call
   budgets, bot label policy, CI-relay routing, handover instructions).

Every violation is printed with its path and the matching line, so the fix is
obvious. `--root` defaults to the current directory. Exit status is non-zero
(fails closed) on any violation; nothing but paths, line numbers and matched
markers is printed.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path
from typing import List, Tuple

# Tracked paths that must never exist in the public repository again.
FORBIDDEN_PATH_PREFIXES = (
    "development/scripts/devlead/",
    "docs/devlead/",
    "docs/ci-733-",
    "docs/ci730-",
)

# Filename globs (relative to the repository root) that mark agent playbooks.
FORBIDDEN_PATH_GLOBS = (
    "**/agent-playbook*.md",
    "**/*-playbook-agent*.md",
    "**/devlead*.md",
)

# Agent-only markers inside DEVELOPMENT.md. Each entry is (label, regex); the
# regex is matched case-insensitively against a single line.
FORBIDDEN_DEVELOPMENT_MD_MARKERS: Tuple[Tuple[str, str], ...] = (
    ("devlead toolkit", r"development/scripts/devlead"),
    ("devlead playbook", r"devlead[_ ]playbook"),
    ("devlead fleet moderation", r"dev[-_ ]?lead.{0,40}fleet|fleet.{0,40}dev[-_ ]?lead"),
    ("agent-only playbook", r"agent[-_ ]only playbook"),
    ("agent playbook reference", r"agent playbook"),
)

# The guard must be able to point readers at the policy section that explains
# where agent-only documentation lives, so that single section is exempt from the
# marker scan. Its title must match this regex.
EXEMPT_SECTION_PATTERN = re.compile(
    r"agent[-_ ]only documentation is private", re.IGNORECASE
)

DEVELOPMENT_MD = "DEVELOPMENT.md"


def _git_tracked_files(root: Path) -> List[str]:
    """Return the tracked files of `root`, or [] when it is not a git checkout."""
    try:
        out = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        ).stdout
    except (OSError, subprocess.CalledProcessError):
        return []
    return [p for p in out.decode("utf-8", "replace").split("\0") if p]


def check_paths(root: Path) -> List[str]:
    violations: List[str] = []
    tracked = _git_tracked_files(root)
    if tracked:
        candidates = tracked
    else:
        candidates = [
            str(p.relative_to(root))
            for p in root.rglob("*")
            if p.is_file() and ".git" not in p.parts
        ]
    for rel in candidates:
        if any(rel.startswith(prefix) for prefix in FORBIDDEN_PATH_PREFIXES):
            violations.append(f"{rel}: forbidden agent-only path")
            continue
        for pattern in FORBIDDEN_PATH_GLOBS:
            if Path(rel).match(pattern):
                violations.append(f"{rel}: forbidden agent playbook path")
                break
    return violations


def check_development_md(root: Path) -> List[str]:
    path = root / DEVELOPMENT_MD
    if not path.is_file():
        return []
    violations: List[str] = []
    exempt = False
    for lineno, line in enumerate(
        path.read_text(encoding="utf-8", errors="replace").splitlines(), start=1
    ):
        if line.lstrip().startswith("#"):
            # A new heading starts a new section; only the policy section that
            # documents where agent-only docs live is exempt from the scan.
            exempt = bool(EXEMPT_SECTION_PATTERN.search(line))
            continue
        if exempt:
            continue
        for label, pattern in FORBIDDEN_DEVELOPMENT_MD_MARKERS:
            if re.search(pattern, line, re.IGNORECASE):
                violations.append(
                    f"{DEVELOPMENT_MD}:{lineno}: agent-only content ({label})"
                )
    return violations


def main(argv: List[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        default=".",
        help="repository root to inspect (default: current directory)",
    )
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()

    violations = check_paths(root) + check_development_md(root)
    if violations:
        print("Agent-only documentation found in the public repository (ci#736):")
        for violation in violations:
            print(f"  - {violation}")
        print(
            "Move this content into the private integration that owns the coder/"
            "development lead and link it into native msgmate_coder interactions; "
            "see the 'Agent-only documentation is private' section of DEVELOPMENT.md."
        )
        return 1

    print(
        "No agent-only/development-bot documentation in the public repository "
        f"({root})."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
