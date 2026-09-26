"""Integration source materialization (git clone/fetch, local, submodule)."""

from __future__ import annotations

import json
import subprocess
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Dict, Optional

from .manifest import Integration, Manifest


class SourceError(RuntimeError):
    pass


@dataclass
class LockEntry:
    repo: Optional[str]
    ref: str
    commit: str
    source: str


@dataclass
class Lockfile:
    version: int = 1
    integrations: Dict[str, LockEntry] = field(default_factory=dict)

    @classmethod
    def load(cls, path: Path) -> "Lockfile":
        if not path.exists():
            return cls()
        raw = json.loads(path.read_text(encoding="utf-8"))
        entries: Dict[str, LockEntry] = {}
        for integ_id, entry in (raw.get("integrations") or {}).items():
            entries[integ_id] = LockEntry(
                repo=entry.get("repo"),
                ref=str(entry.get("ref", "")),
                commit=str(entry.get("commit", "")),
                source=str(entry.get("source", "git")),
            )
        return cls(version=int(raw.get("version", 1)), integrations=entries)

    def save(self, path: Path) -> None:
        payload = {
            "version": self.version,
            "integrations": {
                integ_id: asdict(entry)
                for integ_id, entry in sorted(self.integrations.items())
            },
        }
        path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")


def _run_git(args, cwd: Optional[Path] = None) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=str(cwd) if cwd else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.returncode != 0:
        cmd = "git " + " ".join(args)
        raise SourceError(f"{cmd} failed: {result.stderr.strip() or result.stdout.strip()}")
    return result.stdout.strip()


def _rev_parse(path: Path) -> str:
    try:
        return _run_git(["rev-parse", "HEAD"], cwd=path)
    except SourceError:
        return ""


def ensure_checkout(
    manifest: Manifest,
    integ: Integration,
    lock: Lockfile,
    *,
    frozen: bool,
    update: bool,
    log,
) -> LockEntry:
    """Materialize an integration checkout and return its lock entry."""
    path = manifest.path_for(integ)
    locked = lock.integrations.get(integ.id)

    if integ.source == "submodule":
        log(f"{integ.id}: submodule update --init {integ.path}")
        _run_git(["submodule", "update", "--init", "--", integ.path], cwd=manifest.repo_root)
        if not (path / "go.mod").exists():
            raise SourceError(f"{integ.id}: submodule checkout missing at {path}")
        return LockEntry(repo=integ.repo, ref=integ.ref, commit=_rev_parse(path), source="submodule")

    if integ.source == "local":
        if not (path / "go.mod").exists():
            raise SourceError(f"{integ.id}: local checkout missing at {path}")
        return LockEntry(repo=None, ref=integ.ref, commit=_rev_parse(path), source="local")

    # source == "git"
    has_checkout = (path / "go.mod").exists()
    if has_checkout and not update:
        current = _rev_parse(path)
        # Frozen syncs are authoritative: check out the locked commit even when
        # a checkout already exists (e.g. a stale submodule working tree).
        if frozen and locked and locked.commit and current and current != locked.commit:
            log(f"{integ.id}: frozen checkout {locked.commit[:12]}")
            _run_git(["fetch", "--tags", "--force", "origin"], cwd=path)
            _run_git(["checkout", "--force", locked.commit], cwd=path)
            current = _rev_parse(path)
        else:
            log(f"{integ.id}: using existing checkout {integ.path}")
        return LockEntry(repo=integ.repo, ref=integ.ref, commit=current, source="git")

    if not has_checkout:
        if frozen and (not locked or not locked.commit):
            raise SourceError(
                f"{integ.id}: frozen sync requires a lock entry; run sync without --frozen first"
            )
        path.parent.mkdir(parents=True, exist_ok=True)
        log(f"{integ.id}: cloning {integ.repo}")
        _run_git(["clone", "--filter=blob:none", "--no-checkout", integ.repo, str(path)])

    target = locked.commit if (frozen and locked and locked.commit) else integ.ref
    log(f"{integ.id}: checkout {target}")
    _run_git(["fetch", "--tags", "--force", "origin"], cwd=path)
    _run_git(["checkout", "--force", target], cwd=path)

    if not (path / "go.mod").exists():
        raise SourceError(f"{integ.id}: checkout at {path} has no go.mod")
    return LockEntry(repo=integ.repo, ref=integ.ref, commit=_rev_parse(path), source="git")
