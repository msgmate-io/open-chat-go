#!/usr/bin/env python3
"""Container entrypoint for the dev ``integration-sync`` service.

The service bind-mounts the host checkout at ``/workspace`` and, running as
root by default, would otherwise leave every generated artifact (integration
clones, ``.integrations`` cache/markers, the private ``ci`` fragment, the
generated Go workspace and frontend symlinks) owned by ``root``. That breaks
the host checkout: the developer cannot edit or re-run the manager, and git
rejects the root-owned clones as "dubious ownership".

This entrypoint keeps the workspace owned by whoever owns it on the host:

1. detect the owner uid/gid of the checkout,
2. repair generated paths that a previous root run left root-owned,
3. drop privileges to that uid/gid (``setgroups``/``setgid``/``setuid``) and
   exec ``openchat-integrations``.

When the checkout is itself root-owned (CI images, release builds) the drop is
a no-op, so the previous behaviour is preserved. Repair failures (e.g. a
root-squashed network filesystem) are logged and the command still runs.
"""

from __future__ import annotations

import os
import pwd
import stat
import sys
from pathlib import Path

# Paths the manager writes into the bind-mounted checkout. Only these are
# repaired; the rest of the repository (tracked source) is never touched.
GENERATED_PATHS = (
    ".integrations",
    "clients/integrations",
    "development/ci",
    "development/helm",
    "clients/gomobile",
    "clients/llm_coding_agents",
    "frontend/pages/integrations",
    "frontend/integrations",
    "backend/go.work",
    "backend/go.work.sum",
    "backend/integrations/externalintegrations/imports_gen.go",
    "integrations.local.yaml",
)


def log(message: str) -> None:
    print(f"[integration-sync] {message}", file=sys.stderr, flush=True)


def _lchown_tree(path: Path, uid: int, gid: int) -> int:
    """Chown ``path`` and everything below it without following symlinks."""
    changed = 0

    def one(candidate: str) -> None:
        nonlocal changed
        try:
            info = os.lstat(candidate)
        except OSError:
            return
        if info.st_uid == uid and info.st_gid == gid:
            return
        try:
            os.lchown(candidate, uid, gid)
            changed += 1
        except OSError:
            pass

    try:
        info = os.lstat(path)
    except OSError:
        return 0

    if stat.S_ISDIR(info.st_mode) and not stat.S_ISLNK(info.st_mode):
        for root, dirs, files in os.walk(path):
            one(root)
            for name in dirs:
                one(os.path.join(root, name))
            for name in files:
                one(os.path.join(root, name))
    else:
        one(str(path))
    return changed


def heal(repo_root: Path, uid: int, gid: int) -> int:
    total = 0
    for rel in GENERATED_PATHS:
        total += _lchown_tree(repo_root / rel, uid, gid)
    return total


def _target_groups(uid: int, gid: int) -> list[int]:
    try:
        name = pwd.getpwuid(uid).pw_name
        return os.getgrouplist(name, gid)
    except (KeyError, OSError):
        return [gid]


def drop_privileges(uid: int, gid: int) -> None:
    os.setgid(gid)
    try:
        os.setgroups(_target_groups(uid, gid))
    except OSError:
        os.setgroups([gid])
    os.setuid(uid)


def main() -> int:
    repo_root = Path(os.environ.get("OPENCHAT_WORKSPACE", "/workspace"))
    argv = sys.argv[1:] or ["prepare"]

    if os.geteuid() != 0:
        # Already running as the developer (e.g. an explicit `user:` override);
        # nothing to repair or drop.
        os.execvp("openchat-integrations", ["openchat-integrations", *argv])

    try:
        info = repo_root.stat()
        owner_uid, owner_gid = info.st_uid, info.st_gid
    except OSError as exc:
        log(f"warning: cannot stat {repo_root}: {exc}; running as root")
        os.execvp("openchat-integrations", ["openchat-integrations", *argv])

    if owner_uid == 0:
        # Root-owned checkout (CI/release): keep the historical behaviour.
        os.execvp("openchat-integrations", ["openchat-integrations", *argv])

    try:
        changed = heal(repo_root, owner_uid, owner_gid)
        if changed:
            log(f"repaired ownership of {changed} generated path(s) to {owner_uid}:{owner_gid}")
    except OSError as exc:
        log(f"warning: ownership repair failed ({exc}); continuing as root")

    # git/ssh need a writable HOME; credentials are injected via GIT_CONFIG_*.
    os.environ.setdefault("HOME", "/tmp")
    drop_privileges(owner_uid, owner_gid)
    os.execvp("openchat-integrations", ["openchat-integrations", *argv])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
