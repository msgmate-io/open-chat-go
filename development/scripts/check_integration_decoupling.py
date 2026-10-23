#!/usr/bin/env python3
"""Decoupling guard for integration modules (ci#423).

An integration is *decoupled* once its compiled artifact stops importing
``backend/...`` packages: the module then builds with ``GOWORK=off`` and without
a monorepo checkout around it, so its compile inputs are stable - the
precondition for per-integration prebuilds (ci#345 design, ci#410 footprint).

The guard fails when the module is still coupled:

  (i)  ``go.mod`` still carries a ``replace backend => ...`` directive, or
  (ii) ``go list -deps`` of the shipped packages yields a ``backend/...``
       package.

``-test`` is deliberately not passed: test-only ``backend/...`` imports are not
part of the shipped artifact and stay legal while the migration is incremental.

Usage, from the integration module checkout:

    GOWORK=off python3 development/scripts/check_integration_decoupling.py \
        --module-dir .

Exit codes: 0 = decoupled, 1 = coupled, 2 = usage/environment error.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

# `replace backend => ...`, `replace backend v0.0.0 => ...` (not `replace backendfoo`).
BACKEND_REPLACE_RE = re.compile(r"^\s*replace\s+backend(\s|=>)", re.M)


def go_mod_backend_replace(module_dir: Path) -> bool:
    """True when go.mod still replaces the monorepo ``backend`` module."""
    go_mod = module_dir / "go.mod"
    if not go_mod.is_file():
        print(f"error: {go_mod} not found", file=sys.stderr)
        raise SystemExit(2)
    return bool(BACKEND_REPLACE_RE.search(go_mod.read_text(encoding="utf-8")))


def backend_packages(module_dir: Path) -> list[str] | None:
    """Return the ``backend/...`` packages of the shipped graph, None on error."""
    env = dict(os.environ)
    env["GOWORK"] = "off"
    try:
        result = subprocess.run(
            ["go", "list", "-deps", "./..."],
            cwd=module_dir,
            capture_output=True,
            text=True,
            env=env,
        )
    except FileNotFoundError:
        print("warning: `go` is not on PATH; cannot measure the dependency graph", file=sys.stderr)
        return None
    if result.returncode != 0:
        print(
            f"warning: `go list -deps ./...` failed in {module_dir}: "
            f"{result.stderr.strip()}",
            file=sys.stderr,
        )
        return None
    return sorted({line for line in result.stdout.split() if line.startswith("backend/")})


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--module-dir",
        type=Path,
        default=Path("."),
        help="integration module checkout to check (default: current directory)",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="emit a machine-readable report on stdout (baseline diffs, ci#423 step 0)",
    )
    args = parser.parse_args()

    module_dir = args.module_dir.resolve()
    if not (module_dir / "go.mod").is_file():
        print(f"error: {module_dir} is not a Go module (no go.mod)", file=sys.stderr)
        return 2

    replace = go_mod_backend_replace(module_dir)
    packages = backend_packages(module_dir)
    if packages is None and not replace:
        print(
            "error: could not measure the dependency graph; run this from the module "
            "checkout with a working `go` toolchain",
            file=sys.stderr,
        )
        return 2
    packages = packages or []
    coupled = replace or bool(packages)

    if args.json:
        print(
            json.dumps(
                {
                    "module_dir": str(module_dir),
                    "replace_backend": replace,
                    "backend_packages": packages,
                    "coupled": coupled,
                },
                indent=2,
                sort_keys=True,
            )
        )
    elif coupled:
        print(f"COUPLED: {module_dir} still depends on the monorepo core")
        if replace:
            print(
                "  - go.mod still replaces `backend`; drop `replace backend` and the "
                "`backend` requirement so the module builds standalone"
            )
        for package in packages:
            print(
                f"  - shipped package imports {package}; use "
                "integrationinterface.RequireHost instead"
            )
    else:
        print(f"decoupled: {module_dir} builds without backend/... imports")

    return 1 if coupled else 0


if __name__ == "__main__":
    raise SystemExit(main())
