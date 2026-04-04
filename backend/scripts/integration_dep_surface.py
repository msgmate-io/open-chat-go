#!/usr/bin/env python3
"""Measure the integration dependency surface (ci#423).

The stable-Host migration (ci#423) is only
verifiable by measurement: an integration is decoupled when it no longer pulls
any ``backend/...`` package into its *compiled artifact*. This script reports
exactly that, per integration, so a migration increment can be checked and the
remaining backlog re-derived at any time.

For every integration module it prints:

* ``packages`` — transitive dependency count (``go list -deps``), i.e. how much
  of the module graph a per-integration prebuild would have to compile;
* ``backend`` — ``backend/...`` packages in that set. This is the number that
  must reach ``0``;
* ``test_packages`` / ``test_backend`` — the same two numbers with ``-test``,
  which additionally include each package's in-package and external test
  dependencies. These are informational: test-only ``backend/...`` imports are
  explicitly allowed by the recipe (they do not enter the shipped graph), and
  they are what the Go-test CI job pays for.

Usage
-----

Run it from a Go workspace that ``use``s ``backend/`` plus every integration
module, with the checkouts at the revisions pinned in ``integrations.lock.json``
(the ``full`` profile):

.. code-block:: console

    $ cd <workspace root>
    $ GOWORK="$PWD/go.work" python3 backend/scripts/integration_dep_surface.py

The module pattern handed to ``go list`` is resolved through ``go.work``, so
``GOWORK`` is what actually selects the revisions being measured. ``--repo-root``
only changes where the script discovers integration module paths (useful when the
workspace mirrors the checkouts somewhere else).

Note: the experimental ``msgmate_coder_integration_wt*`` / ``*_ask_wt`` /
``*_task_hardening`` / ``*_trust`` directories all declare the *same* module path
(``github.com/msgmate-io/msgmate-coder-integration``) and must not be added to
the workspace alongside the canonical checkout, or ``go`` refuses with
"module ... appears multiple times in workspace". The script skips them.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

# Integration directories that duplicate a module path declared by another
# checkout (worktree copies). They are skipped so the workspace stays valid.
SKIP_DIRS = (
    "msgmate_coder_integration_wt",
    "msgmate_coder_ask_wt",
    "msgmate_coder_task_hardening",
    "msgmate_coder_trust",
)


def module_names(integrations_dir: Path) -> list[tuple[str, str]]:
    """Return (directory name, module path) for every integration checkout."""
    out: list[tuple[str, str]] = []
    seen: set[str] = set()
    for entry in sorted(integrations_dir.iterdir()):
        if not entry.is_dir():
            continue
        if any(entry.name.startswith(prefix) for prefix in SKIP_DIRS):
            continue
        gomod = entry / "go.mod"
        if not gomod.is_file():
            continue
        match = re.search(r"^module\s+(\S+)", gomod.read_text(encoding="utf-8"), re.M)
        if not match:
            continue
        module = match.group(1)
        if module in seen:  # a duplicate path would break the workspace
            print(f"  skipping duplicate module path {module} ({entry.name})", file=sys.stderr)
            continue
        seen.add(module)
        out.append((entry.name, module))
    return out


def deps(module: str, include_tests: bool) -> list[str] | None:
    cmd = ["go", "list", "-deps"]
    if include_tests:
        cmd.append("-test")
    cmd.append(f"{module}/...")
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"  go list failed for {module}: {result.stderr.strip()}", file=sys.stderr)
        return None
    return [line for line in result.stdout.split() if line]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="repository root holding clients/integrations (default: this checkout)",
    )
    args = parser.parse_args()
    repo_root = args.repo_root.resolve()
    integrations_dir = repo_root / "clients" / "integrations"
    if not integrations_dir.is_dir():
        print(f"integrations directory not found at {integrations_dir}", file=sys.stderr)
        return 1
    if not os.environ.get("GOWORK"):
        print(
            "warning: GOWORK is unset; measurements would use the integration's own "
            "module graph rather than the workspace",
            file=sys.stderr,
        )

    modules = module_names(integrations_dir)
    print(f"measuring {len(modules)} integration modules\n")
    header = (
        f"{'integration':<44} {'packages':>9} {'backend':>8} "
        f"{'test_pkgs':>10} {'test_backend':>13}"
    )
    print(header)
    print("-" * len(header))

    production_coupled: list[tuple[str, list[str]]] = []
    for directory, module in modules:
        prod = deps(module, include_tests=False)
        if prod is None:
            continue
        test = deps(module, include_tests=True) or []
        prod_backend = sorted(p for p in prod if p.startswith("backend/"))
        test_backend = sorted(p for p in test if p.startswith("backend/"))
        print(
            f"{directory:<44} {len(prod):>9} {len(prod_backend):>8} "
            f"{len(test):>10} {len(test_backend):>13}"
        )
        if prod_backend:
            production_coupled.append((module, prod_backend))

    print()
    if not production_coupled:
        print("every integration is decoupled in its compiled artifact.")
        return 0
    print(f"PRODUCTION-coupled integrations ({len(production_coupled)}):")
    for module, packages in production_coupled:
        print(f"  {module}")
        for package in packages:
            print(f"      {package}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
