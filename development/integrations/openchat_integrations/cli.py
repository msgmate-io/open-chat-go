"""Command line interface for the Open-Chat integration manager.

Usage:
    python3 -m openchat_integrations sync [--profile P] [--frozen] [--update]
    python3 -m openchat_integrations resolve [--profile P]
    python3 -m openchat_integrations export [--profile P] [--check]
    python3 -m openchat_integrations prepare [--profile P] [--frozen] [--update]
    python3 -m openchat_integrations check [--profile P]
    python3 -m openchat_integrations list [--profile P]
    python3 -m openchat_integrations dev --integration ID --path DIR
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import List, Optional

from . import frontend, gowork, sources
from .manifest import Manifest, ManifestError, MANIFEST_NAME, LOCAL_OVERLAY_NAME
from .sources import Lockfile, SourceError

LOCKFILE_NAME = "integrations.lock.json"


def log(message: str) -> None:
    print(f"[integrations] {message}", file=sys.stderr, flush=True)


def find_repo_root(start: Optional[Path]) -> Path:
    current = (start or Path.cwd()).resolve()
    for candidate in [current, *current.parents]:
        if (candidate / MANIFEST_NAME).exists():
            return candidate
    raise ManifestError(f"could not find {MANIFEST_NAME} above {current}")


def _load(args) -> Manifest:
    repo_root = find_repo_root(Path(args.repo_root) if args.repo_root else None)
    return Manifest.load(repo_root, apply_local_overlay=True)


def _profile(args, manifest: Manifest) -> str:
    return manifest.resolve_profile(getattr(args, "profile", None))


def cmd_sync(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    selected = manifest.profile_ids(profile)
    closure = manifest.closure(selected)

    lock_path = manifest.repo_root / LOCKFILE_NAME
    lock = Lockfile.load(lock_path)

    log(f"sync: profile={profile} selected={','.join(selected)}")
    for integ_id in closure:
        integ = manifest.integrations[integ_id]
        entry = sources.ensure_checkout(
            manifest,
            integ,
            lock,
            frozen=args.frozen,
            update=args.update,
            log=log,
        )
        lock.integrations[integ_id] = entry

    # Drop entries for integrations that no longer exist in the manifest.
    # The lockfile pins every integration, so syncing one profile must not
    # prune the entries belonging to other profiles.
    for integ_id in list(lock.integrations):
        if integ_id not in manifest.integrations:
            del lock.integrations[integ_id]

    lock.save(lock_path)
    log(f"sync: wrote {lock_path.relative_to(manifest.repo_root)}")
    return 0


def cmd_resolve(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    plan = gowork.build_plan(manifest, profile)
    log(f"resolve: profile={profile} closure={','.join(plan['closure'])}")
    gowork.write_go_work(manifest, plan["closure"], log)
    gowork.write_imports_gen(manifest, plan["selected"], log)
    gowork.write_plan(manifest, plan, log)
    if plan["tags"]:
        log(f"resolve: build tags: {','.join(plan['tags'])}")
    return 0


def cmd_export(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    selected = manifest.profile_ids(profile)
    dist_dir = Path(args.dist_dir) if args.dist_dir else manifest.repo_root / "frontend" / "dist" / "client"
    frontend.export_pages(manifest, selected, dist_dir, check=args.check, log=log)
    return 0


def cmd_prepare(args) -> int:
    rc = cmd_sync(args)
    if rc != 0:
        return rc
    rc = cmd_resolve(args)
    if rc != 0:
        return rc
    return cmd_frontend(args)


def cmd_frontend(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    selected = manifest.profile_ids(profile)
    frontend.link(manifest, selected, log)
    return 0


def cmd_check(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    plan = gowork.build_plan(manifest, profile)

    lock_path = manifest.repo_root / LOCKFILE_NAME
    lock = Lockfile.load(lock_path)
    problems: List[str] = []
    for integ_id in plan["closure"]:
        integ = manifest.integrations[integ_id]
        path = manifest.path_for(integ)
        if not (path / "go.mod").exists():
            problems.append(f"{integ_id}: not materialized at {path}")
            continue
        entry = lock.integrations.get(integ_id)
        if entry is None:
            problems.append(f"{integ_id}: missing lock entry")
            continue
        current = sources._rev_parse(path)
        if current and entry.commit and current != entry.commit:
            problems.append(
                f"{integ_id}: checkout {current[:12]} != lock {entry.commit[:12]}"
            )

    gowork_path = manifest.repo_root / "backend" / gowork.GOWORK_REL
    if not gowork_path.exists():
        problems.append(f"missing {gowork_path.relative_to(manifest.repo_root)} (run resolve)")

    dist_dir = manifest.repo_root / "frontend" / "dist" / "client"
    if dist_dir.exists():
        try:
            frontend.export_pages(manifest, plan["selected"], dist_dir, check=True, log=log)
        except frontend.ExportError as exc:
            problems.append(str(exc))

    if problems:
        for problem in problems:
            log(f"check: FAIL {problem}")
        return 1
    log(f"check: OK profile={profile}")
    return 0


def cmd_list(args) -> int:
    manifest = _load(args)
    profile = _profile(args, manifest)
    selected = manifest.profile_ids(profile)
    closure = set(manifest.closure(selected))
    for integ_id, integ in manifest.integrations.items():
        marker = "*" if integ_id in selected else ("." if integ_id in closure else " ")
        front = integ.frontend.name if integ.frontend else "-"
        print(f"{marker} {integ_id:20s} {integ.module:55s} frontend={front}")
    return 0


def cmd_dev(args) -> int:
    manifest = _load(args)
    if args.integration not in manifest.integrations:
        raise ManifestError(f"unknown integration {args.integration!r}")
    target = Path(args.path).resolve()
    if not (target / "go.mod").exists():
        raise ManifestError(f"{target} is not a Go module (missing go.mod)")

    overlay_path = manifest.repo_root / LOCAL_OVERLAY_NAME
    overlay = {"overrides": {}}
    if overlay_path.exists():
        import yaml

        overlay = yaml.safe_load(overlay_path.read_text(encoding="utf-8")) or {"overrides": {}}
        overlay.setdefault("overrides", {})
    overlay["overrides"][args.integration] = {"source": "local", "path": str(target)}
    import yaml

    overlay_path.write_text(yaml.safe_dump(overlay, sort_keys=False), encoding="utf-8")
    log(f"dev: {args.integration} -> {target} (written to {overlay_path.name})")
    return cmd_prepare(args)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="openchat_integrations")
    parser.add_argument("--repo-root", help="repository root (defaults to nearest ancestor with integrations.yaml)")
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p):
        p.add_argument("--profile", help="integration profile (defaults to INTEGRATION_PROFILE or manifest default)")
        p.add_argument("--repo-root", help=argparse.SUPPRESS)

    for name in ("sync", "prepare"):
        p = sub.add_parser(name, help="materialize integration checkouts")
        add_common(p)
        p.add_argument("--frozen", action="store_true", help="use integrations.lock.json commits")
        p.add_argument("--update", action="store_true", help="update existing checkouts to ref")

    p = sub.add_parser("resolve", help="generate go.work, imports_gen.go and the effective plan")
    add_common(p)

    p = sub.add_parser("frontend", help="link integration frontend packages into the aggregator")
    add_common(p)

    p = sub.add_parser("export", help="export prerendered integration pages")
    add_common(p)
    p.add_argument("--check", action="store_true", help="verify sources without copying")
    p.add_argument("--dist-dir", help="frontend dist/client directory")

    p = sub.add_parser("check", help="validate lockfile, checkouts and frontend pages")
    add_common(p)

    p = sub.add_parser("list", help="list integrations for a profile")
    add_common(p)

    p = sub.add_parser("dev", help="point an integration at a local checkout and prepare")
    add_common(p)
    p.add_argument("--integration", required=True)
    p.add_argument("--path", required=True)

    return parser


def main(argv: Optional[List[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    handlers = {
        "sync": cmd_sync,
        "prepare": cmd_prepare,
        "resolve": cmd_resolve,
        "frontend": cmd_frontend,
        "export": cmd_export,
        "check": cmd_check,
        "list": cmd_list,
        "dev": cmd_dev,
    }
    try:
        return handlers[args.command](args)
    except (ManifestError, SourceError, gowork.ResolveError, frontend.ExportError) as exc:
        log(f"error: {exc}")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
