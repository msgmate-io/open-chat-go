"""Data-driven export of prerendered integration frontend pages.

Replaces the previously hardcoded `frontend/scripts/export_integration_pages.sh`
mapping. The page list comes from the manifest (`frontend.pages`), or from an
integration-owned `integration.frontend.json` when present (Phase 2).
"""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path
from typing import List

from .manifest import FrontendPage, Integration, Manifest


class ExportError(RuntimeError):
    pass


def _replace_symlink(target: Path, link: Path) -> None:
    if link.is_symlink() or link.exists():
        if link.is_dir() and not link.is_symlink():
            shutil.rmtree(link)
        else:
            link.unlink()
    link.parent.mkdir(parents=True, exist_ok=True)
    os.symlink(target, link)


def _ensure_frontend_workspace(frontend_root: Path, log) -> None:
    """Add `integrations/*` to the frontend npm workspaces once."""
    package_json = frontend_root / "package.json"
    if not package_json.exists():
        return
    data = json.loads(package_json.read_text(encoding="utf-8"))
    workspaces = data.get("workspaces")
    if not isinstance(workspaces, list):
        return
    if "integrations/*" in workspaces:
        return
    workspaces.append("integrations/*")
    data["workspaces"] = workspaces
    package_json.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
    log("frontend: added integrations/* to npm workspaces")


def link(manifest: Manifest, selected: List[str], log) -> int:
    """Link integration-provided Vike frontend packages into the aggregator.

    Each integration may ship `frontend/` (with a `package.json` and a `pages/`
    directory). The package is linked into `frontend/integrations/<id>` and its
    pages into `frontend/pages/integrations/<name>` so Vike's filesystem routing
    picks them up. No-op for integrations without a frontend package.
    """
    frontend_root = manifest.repo_root / "frontend"
    if not frontend_root.exists():
        log("frontend: aggregator not present, skipping")
        return 0

    integ_root = frontend_root / "integrations"
    pages_root = frontend_root / "pages" / "integrations"
    linked = 0
    for integ_id in selected:
        integ = manifest.integrations[integ_id]
        if integ.frontend is None or not integ.frontend.package:
            continue
        src = manifest.path_for(integ) / integ.frontend.path
        if not (src / "package.json").exists():
            continue
        _replace_symlink(src, integ_root / integ_id)
        src_pages = src / "pages"
        if src_pages.exists():
            _replace_symlink(src_pages, pages_root / integ.frontend.name)
        linked += 1

    if linked:
        _ensure_frontend_workspace(frontend_root, log)
        log(f"frontend: linked {linked} integration frontend packages")
    return linked


def _pages_from_integration_json(path: Path) -> List[FrontendPage]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    pages: List[FrontendPage] = []
    for page in raw.get("pages") or []:
        source = str(page.get("source", "")).strip()
        asset = str(page.get("asset", "")).strip()
        if not source or not asset:
            raise ExportError(f"{path}: page needs source and asset")
        pages.append(
            FrontendPage(
                source=source,
                asset=asset,
                route=str(page["route"]) if page.get("route") else None,
                public=bool(page.get("public", False)),
            )
        )
    return pages


def pages_for(manifest: Manifest, integ: Integration) -> List[FrontendPage]:
    if integ.frontend is None:
        return []
    override = manifest.path_for(integ) / "integration.frontend.json"
    if override.exists():
        return _pages_from_integration_json(override)
    return integ.frontend.pages


def export_pages(
    manifest: Manifest,
    selected: List[str],
    dist_dir: Path,
    *,
    check: bool,
    log,
) -> int:
    """Copy prerendered pages into each integration's embedded assets.

    Returns the number of pages exported. When `check` is true, only verifies
    that every source exists.
    """
    count = 0
    missing: List[str] = []
    for integ_id in selected:
        integ = manifest.integrations[integ_id]
        pages = pages_for(manifest, integ)
        if not pages:
            continue
        target_root = manifest.path_for(integ) / "frontend_assets"
        for page in pages:
            src = dist_dir / page.source / "index.html"
            dst = target_root / page.asset
            if not src.exists():
                missing.append(f"{integ_id}: {src}")
                continue
            if not check:
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(src, dst)
            count += 1

    if missing:
        raise ExportError(
            "missing prerendered pages (run the frontend build first):\n  "
            + "\n  ".join(missing)
        )
    action = "verified" if check else "exported"
    log(f"export: {action} {count} integration pages")
    return count
