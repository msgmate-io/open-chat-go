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

Migration before/after
----------------------

``--write-baseline FILE`` records the measured surface and ``--baseline FILE``
diffs a later run against it, adding a ``d_pkgs``/``d_backend`` column per
integration. With ``--baseline`` the script also *fails* when an integration that
is recorded in the baseline gains a ``backend/...`` package (or more of them),
which is what turns the recipe's "measure the delta" step into a gate. The
committed ``integrations.dep-surface.json`` pins the integrations that are already
migrated; refresh it with ``--write-baseline`` after a migration. Integrations
absent from the baseline are still being migrated and are not pinned.

Note: the experimental ``msgmate_coder_integration_wt*`` / ``*_ask_wt`` /
``*_task_hardening`` / ``*_trust`` directories all declare the *same* module path
(``github.com/msgmate-io/msgmate-coder-integration``) and must not be added to
the workspace alongside the canonical checkout, or ``go`` refuses with
"module ... appears multiple times in workspace". The script skips them.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


def delta(value: int, base: Any) -> str:
    """Format the change of a measured number against a baseline value.

    A number that the baseline does not record (an integration that is still
    being migrated) is reported as ``n/a`` rather than as a bogus delta.
    """
    if not isinstance(base, int):
        return "n/a"
    return f"{value - base:+d}"


def write_baseline(path: Path, measured: list[dict]) -> None:
    """Record the measured surface so a later run can diff against it.

    Entries are keyed by module path (not directory name): the module path is the
    stable identity, the local checkout directory is not.
    """
    payload = {
        "version": 1,
        "integrations": {
            entry["module"]: {
                "directory": entry["directory"],
                "packages": len(entry["production"]),
                "backend": len(entry["backend"]),
                "backend_packages": entry["backend"],
                "test_packages": len(entry["test"]),
                "test_backend": len(entry["test_backend"]),
            }
            for entry in measured
        },
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"wrote baseline for {len(measured)} integrations to {path}")


def load_baseline(path: Path) -> dict:
    """Load a baseline written by --write-baseline, keyed by module path.

    A baseline that is missing or of an unknown version is a hard error (exit 2):
    silently measuring without a baseline would report deltas as ``n/a``.
    """
    if not path.is_file():
        print(
            f"error: baseline {path} not found; create it with --write-baseline",
            file=sys.stderr,
        )
        raise SystemExit(2)
    payload = json.loads(path.read_text(encoding="utf-8"))
    if payload.get("version") != 1:
        print(f"error: unsupported baseline version in {path}", file=sys.stderr)
        raise SystemExit(2)
    return payload.get("integrations") or {}

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
    packages = [line for line in result.stdout.split() if line]
    if not packages:
        # An empty graph means the pattern matched nothing (e.g. the module is
        # not in this workspace): that is not evidence of decoupling.
        print(f"  no packages matched for {module} (is it in GOWORK?)", file=sys.stderr)
        return None
    return packages


def check_baseline(path: Path) -> int:
    """Validate a committed baseline without needing a Go toolchain.

    The committed ``integrations.dep-surface.json`` is the record of which
    integrations are already decoupled, so a malformed entry (or an entry that
    pins an integration which is *not* decoupled) would silently turn the gate
    into a no-op. Exit codes: 0 = valid, 1 = pins a coupled integration,
    2 = malformed.
    """
    entries = load_baseline(path)
    problems: list[str] = []
    coupled: list[str] = []
    for module, entry in sorted(entries.items()):
        if not isinstance(entry, dict):
            problems.append(f"{module}: entry is not an object")
            continue
        backend = entry.get("backend")
        packages = entry.get("backend_packages")
        if not isinstance(backend, int) or not isinstance(packages, list):
            problems.append(f"{module}: backend/backend_packages missing")
            continue
        if backend != len(packages):
            problems.append(
                f"{module}: backend={backend} but {len(packages)} backend_packages recorded"
            )
        if backend:
            coupled.append(f"{module}: pinned with {backend} backend/... package(s)")
        if not isinstance(entry.get("packages"), int):
            problems.append(f"{module}: packages missing")
    if problems:
        print(f"error: malformed baseline {path}:", file=sys.stderr)
        for problem in problems:
            print(f"  {problem}", file=sys.stderr)
        return 2
    if coupled:
        print(f"error: {path} pins integrations that are not decoupled:", file=sys.stderr)
        for item in coupled:
            print(f"  {item}", file=sys.stderr)
        print(
            "       a baseline may only pin decoupled integrations; drop the entry "
            "or finish the migration first",
            file=sys.stderr,
        )
        return 1
    print(f"baseline {path}: ok ({len(entries)} decoupled integration(s) pinned)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="repository root holding clients/integrations (default: this checkout)",
    )
    parser.add_argument(
        "--baseline",
        type=Path,
        help="baseline written by --write-baseline; adds a delta column and fails on "
        "a regression (more backend/... packages than recorded)",
    )
    parser.add_argument(
        "--write-baseline",
        type=Path,
        help="record the measured surface to this file (migration before/after)",
    )
    parser.add_argument(
        "--check-baseline",
        type=Path,
        help="validate a committed baseline without a Go toolchain (0 valid, "
        "1 pins a coupled integration, 2 malformed)",
    )
    parser.add_argument(
        "--fail-only-on-regressions",
        action="store_true",
        help="gate mode: fail only on a regression against the baseline (or on "
        "nothing measurable); integrations the baseline deliberately does not pin "
        "are reported as still-migrating instead of failing. Use this when the "
        "measurement runs over the full integration set during the migration.",
    )
    args = parser.parse_args()

    if args.check_baseline:
        return check_baseline(args.check_baseline)

    baseline = load_baseline(args.baseline) if args.baseline else None
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
    if baseline is not None:
        header += f" {'d_pkgs':>8} {'d_backend':>10}"
    print(header)
    print("-" * len(header))

    measured: list[dict] = []
    production_coupled: list[tuple[str, list[str]]] = []
    regressions: list[tuple[str, str]] = []
    for directory, module in modules:
        prod = deps(module, include_tests=False)
        if prod is None:
            continue
        test = deps(module, include_tests=True) or []
        prod_backend = sorted(p for p in prod if p.startswith("backend/"))
        test_backend = sorted(p for p in test if p.startswith("backend/"))
        line = (
            f"{directory:<44} {len(prod):>9} {len(prod_backend):>8} "
            f"{len(test):>10} {len(test_backend):>13}"
        )
        base = (baseline or {}).get(module, {})
        if baseline is not None:
            line += f" {delta(len(prod), base.get('packages')):>8} {delta(len(prod_backend), base.get('backend')):>10}"
        print(line)
        measured.append(
            {
                "directory": directory,
                "module": module,
                "production": prod,
                "backend": prod_backend,
                "test": test,
                "test_backend": test_backend,
            }
        )
        if prod_backend:
            production_coupled.append((module, prod_backend))
        if baseline is not None and module in baseline:
            # Only integrations recorded in the baseline are pinned; an
            # integration that is still being migrated is absent on purpose.
            # A baseline entry is validated (backend == len(backend_packages)),
            # so any shipped backend/... package that is not recorded is a
            # regression -- including one that was recorded only as test-only.
            known = set(base.get("backend_packages") or [])
            new_backend = [p for p in prod_backend if p not in known]
            if new_backend:
                regressions.append((module, ", ".join(new_backend)))

    if args.write_baseline and measured:
        write_baseline(args.write_baseline, measured)

    print()
    if regressions:
        print(f"REGRESSIONS vs baseline ({len(regressions)}):")
        for module, detail in regressions:
            print(f"  {module}: {detail}")
        return 1
    if baseline is not None:
        print("no regression against the baseline.")
    if not measured:
        # Nothing could be measured (no checkout with a go.mod, or `go list`
        # failed for all of them): never claim a decoupled surface here.
        print("no integration module could be measured; see the warnings above.")
        return 1
    if not production_coupled:
        print("every integration is decoupled in its compiled artifact.")
        return 0
    if args.fail_only_on_regressions:
        # Gate mode: during the ci#423 migration most integrations are still
        # coupled on purpose, so only a *regression* (or nothing measurable)
        # fails. Unpinned integrations are reported so the migration stays
        # visible in the log rather than turning the gate red.
        print(
            f"still-migrating integrations ({len(production_coupled)}) - not pinned in "
            f"{args.baseline} yet:"
        )
        for module, packages in production_coupled:
            print(f"  {module} ({len(packages)} backend/... package(s))")
        print(
            "gate mode: no regression against the baseline; see the ci#423 migration "
            "for the remaining integrations."
        )
        return 0
    print(f"PRODUCTION-coupled integrations ({len(production_coupled)}):")
    for module, packages in production_coupled:
        print(f"  {module}")
        for package in packages:
            print(f"      {package}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
