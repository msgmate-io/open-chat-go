#!/usr/bin/env python3
"""Build the pinned workspace, so a lock/pin bump cannot land a broken tree.

The pin-freshness gate (`check_pin_freshness.py`) proves every tracked pin is at
the tip of its upstream ref. It says nothing about whether that tip actually
*compiles*: a pin can be perfectly fresh and still not satisfy its consumers
(ci#600 — a phase-PR train into one integration `main` dropped a tool
registration and `main` no longer built; the pin was correct and the tree was
broken).

This script closes that gap. It materialises a frozen workspace from the pin
sources (each integration at the commit recorded in the lock, the interface
submodules at the gitlinks recorded in the commit tree), then builds it with the
toolchain the profile declares:

    python3 development/scripts/check_frozen_workspace.py --root .
    python3 development/scripts/check_frozen_workspace.py --root . --tags goolm
    python3 development/scripts/check_frozen_workspace.py --root . \\
        --lock .integrations/private/integrations.private.lock.json

Everything is fetched from the recorded SHAs, never from a branch tip, and the
checkout is written under `.integrations/` (gitignored) with the generated
workspace file at `backend/go.work` (gitignored) — so running it never dirties
the tree. Any pre-existing file at those paths is restored on exit.

Exit status is non-zero (fails closed) when a pin cannot be resolved, a checkout
does not land on its pinned SHA, or the build fails; the report itemises which
component caused it. No secrets are ever printed: only repository URLs, commit
SHAs and build output.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Tuple

GIT_TIMEOUT = int(os.environ.get("PIN_CHECK_GIT_TIMEOUT", "300"))
BUILD_TIMEOUT = int(os.environ.get("FROZEN_BUILD_TIMEOUT", "1800"))

WORKSPACE_DIR = ".integrations/frozen"
GOWORK_REL = Path("backend") / "go.work"

PUBLIC_LOCK = "integrations.lock.json"
PRIVATE_LOCK_CANDIDATES = (
    ".integrations/private/integrations.private.lock.json",
    "development/ci/openchat/integrations.private.lock.json",
)


class CheckError(RuntimeError):
    pass


def run_git(args: List[str], cwd: Optional[Path] = None) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["git", *args],
        cwd=str(cwd) if cwd else None,
        capture_output=True,
        text=True,
        timeout=GIT_TIMEOUT,
    )


def gitlink_shas(root: Path) -> Dict[str, str]:
    """Return {submodule path: gitlink sha} for the commit checked out at root."""
    proc = run_git(["ls-tree", "-r", "HEAD"], cwd=root)
    if proc.returncode != 0:
        raise CheckError(f"git ls-tree failed: {proc.stderr.strip()}")
    out: Dict[str, str] = {}
    for line in proc.stdout.splitlines():
        meta, _, path = line.partition("\t")
        parts = meta.split()
        if len(parts) >= 3 and parts[1] == "commit" and parts[0] == "160000":
            out[path] = parts[2]
    return out


def submodule_urls(root: Path) -> Dict[str, str]:
    """Return {submodule path: url} from .gitmodules."""
    proc = run_git(["config", "-f", ".gitmodules", "--get-regexp", r"^submodule\..*\.url$"], cwd=root)
    if proc.returncode != 0:
        return {}
    urls: Dict[str, str] = {}
    for line in proc.stdout.splitlines():
        key, _, url = line.partition(" ")
        # submodule.<name>.url
        name = key[len("submodule.") : -len(".url")]
        path_proc = run_git(["config", "-f", ".gitmodules", f"submodule.{name}.path"], cwd=root)
        path = path_proc.stdout.strip() or name
        urls[path] = url.strip()
    return urls


@dataclass
class Component:
    ident: str
    repo: str
    commit: str
    path: str
    kind: str


@dataclass
class Result:
    component: Component
    status: str  # OK | FAILED
    detail: str = ""


def load_lock(path: Path) -> Dict[str, dict]:
    try:
        data = json.loads(path.read_text())
    except FileNotFoundError as exc:
        raise CheckError(f"lock not found: {path}") from exc
    except json.JSONDecodeError as exc:
        raise CheckError(f"lock is not valid JSON ({path}): {exc}") from exc
    integrations = data.get("integrations")
    if not isinstance(integrations, dict):
        raise CheckError(f"lock has no `integrations` map: {path}")
    return integrations


def load_manifest(path: Path) -> Dict[str, dict]:
    """Minimal reader of a manifest fragment (integration path overrides)."""
    if not path.exists():
        return {}
    try:
        import yaml  # type: ignore
    except ImportError:
        return {}
    data = yaml.safe_load(path.read_text()) or {}
    integrations = data.get("integrations") or {}
    return integrations if isinstance(integrations, dict) else {}


def checkout(repo: str, commit: str, dest: Path) -> Tuple[bool, str]:
    """Fetch exactly `commit` from `repo` into `dest`."""
    if dest.exists():
        shutil.rmtree(dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    proc = subprocess.run(
        ["git", "init", "--quiet", str(dest)], capture_output=True, text=True, timeout=GIT_TIMEOUT
    )
    if proc.returncode != 0:
        return False, f"git init failed: {proc.stderr.strip()}"
    if run_git(["remote", "add", "origin", repo], cwd=dest).returncode != 0:
        return False, "git remote add failed"
    # Prefer the exact object; fall back to a full fetch when the server refuses
    # to serve an unreferenced SHA.
    fetched = run_git(["fetch", "--quiet", "--depth", "1", "origin", commit], cwd=dest)
    if fetched.returncode != 0:
        fetched = run_git(["fetch", "--quiet", "origin"], cwd=dest)
        if fetched.returncode != 0:
            return False, f"git fetch failed: {fetched.stderr.strip() or fetched.stdout.strip()}"
    proc = run_git(["checkout", "--quiet", "--detach", commit], cwd=dest)
    if proc.returncode != 0:
        return False, f"git checkout {commit[:12]} failed: {proc.stderr.strip()}"
    head = run_git(["rev-parse", "HEAD"], cwd=dest).stdout.strip()
    if head != commit:
        return False, f"checked out {head[:12]} but the pin is {commit[:12]}"
    return True, ""


def build_workspace(root: Path, workspace: Path, components: List[Component], extra_uses: List[Path], tags: str) -> Tuple[int, str]:
    gowork = root / GOWORK_REL
    lines = ["go 1.26.0", "", "use ("]
    lines.append("\t.")
    for path in extra_uses:
        lines.append(f"\t{os.path.relpath(path, gowork.parent)}")
    for comp in components:
        lines.append(f"\t{os.path.relpath(workspace / comp.path, gowork.parent)}")
    lines.append(")")
    gowork.write_text("\n".join(lines) + "\n")

    env = dict(os.environ)
    env.pop("GOFLAGS", None)  # workspace mode rejects -mod=mod
    env.setdefault("GOPROXY", "https://proxy.golang.org,direct")
    cmd = ["go", "build"]
    if tags:
        cmd += ["-tags", tags]
    cmd.append("./...")
    proc = subprocess.run(
        cmd,
        cwd=str(root / "backend"),
        capture_output=True,
        text=True,
        env=env,
        timeout=BUILD_TIMEOUT,
    )
    output = (proc.stdout + proc.stderr).strip()
    return proc.returncode, output


def parse_args(argv: Optional[List[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument(
        "--lock",
        action="append",
        default=None,
        help=f"pin source (repeatable); default {PUBLIC_LOCK}",
    )
    parser.add_argument(
        "--manifest",
        action="append",
        default=None,
        help="manifest fragment for integration paths (repeatable)",
    )
    parser.add_argument("--tags", default="", help="build tags for the workspace build")
    parser.add_argument(
        "--label",
        default="frozen workspace",
        help="label used in the report",
    )
    parser.add_argument(
        "--keep",
        action="store_true",
        help="keep the materialised checkout (debugging)",
    )
    return parser.parse_args(argv)


def main(argv: Optional[List[str]] = None) -> int:
    args = parse_args(argv)
    root = Path(args.root).resolve()
    if not (root / PUBLIC_LOCK).exists() and not args.lock:
        print(f"::error::no lock file found at {root / PUBLIC_LOCK}")
        return 1

    locks = [Path(p) for p in (args.lock or [PUBLIC_LOCK])]
    manifests = [Path(p) for p in (args.manifest or ["integrations.yaml"])]

    try:
        lock: Dict[str, dict] = {}
        for lock_path in locks:
            lock.update(load_lock(lock_path if lock_path.is_absolute() else root / lock_path))
    except CheckError as exc:
        print(f"::error::{exc}")
        return 1

    manifest: Dict[str, dict] = {}
    for manifest_path in manifests:
        manifest.update(load_manifest(manifest_path if manifest_path.is_absolute() else root / manifest_path))

    workspace = root / WORKSPACE_DIR
    gowork = root / GOWORK_REL
    backup: Optional[Tuple[Path, bytes]] = None
    if gowork.exists():
        backup = (gowork, gowork.read_bytes())

    components: List[Component] = []
    for name, info in sorted(lock.items()):
        repo = info.get("repo")
        commit = info.get("commit")
        if not repo or not commit:
            print(f"::error::integration `{name}` has no repo/commit in the lock")
            return 1
        path = (manifest.get(name) or {}).get("path") or f"clients/integrations/{name}_integration"
        components.append(Component(f"integration:{name}", repo, commit, path, "integration"))

    # Interface submodules: pinned by the gitlink recorded in this commit's tree.
    urls = submodule_urls(root)
    for path, sha in sorted(gitlink_shas(root).items()):
        url = urls.get(path, "")
        if not url:
            continue
        ident = f"submodule:{path}"
        components.append(Component(ident, url, sha, f"{WORKSPACE_DIR}-submodule/{path}", "submodule"))

    print(f"Frozen workspace check: build the pinned {args.label} tree")
    print(f"Repository: {root}")
    print(f"Lock(s): {', '.join(str(p) for p in locks)}")
    print()

    results: List[Result] = []
    failures = 0
    for comp in components:
        if comp.kind == "submodule":
            dest = root / WORKSPACE_DIR / "submodules" / comp.path
        else:
            dest = workspace / comp.path
        ok, detail = checkout(comp.repo, comp.commit, dest)
        results.append(Result(comp, "OK" if ok else "FAILED", detail))
        if not ok:
            failures += 1

    extra_uses = [
        root / WORKSPACE_DIR / "submodules" / c.path for c in components if c.kind == "submodule"
    ]

    build_ok = False
    build_output = ""
    if failures == 0:
        try:
            code, build_output = build_workspace(root, workspace, components, extra_uses, args.tags)
            build_ok = code == 0
        except subprocess.TimeoutExpired:
            build_output = f"go build timed out after {BUILD_TIMEOUT}s"
        except FileNotFoundError:
            build_output = "go toolchain not found on PATH"
    if not build_ok:
        failures += 1

    width = max((len(r.component.ident) for r in results), default=10)
    pin_width = 14
    print(f"{'component':<{width}}  {'pin':<{pin_width}}  status   detail")
    print("-" * (width + pin_width + 30))
    for res in results:
        print(f"{res.component.ident:<{width}}  {res.component.commit[:12]:<{pin_width}}  {res.status:<8} {res.detail}")
    print()
    if build_output:
        print("go build output:")
        print(build_output)
        print()

    if not args.keep:
        shutil.rmtree(workspace, ignore_errors=True)
        shutil.rmtree(root / WORKSPACE_DIR, ignore_errors=True)
    if backup:
        backup[0].write_bytes(backup[1])
    else:
        gowork.unlink(missing_ok=True)

    if failures:
        print(f"::error::frozen workspace check: {failures} problem(s) — the pinned tree does not build.")
        return 1
    print("Frozen workspace check satisfied: the pinned tree builds.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
