#!/usr/bin/env python3
"""Build optional integration-provided auxiliary binaries and record Go tags.

An integration can declare an ``integration.bundle.json`` at its checkout root
describing an auxiliary binary (for example the standalone Rust TUI) that should
be compiled and embedded into the open-chat Go binary whenever the integration
is part of the selected profile::

    {
      "tag": "openchattui",          # Go build tag that enables the embed
      "tool": "cargo",               # required tool; skipped when missing
      "build": ["cargo", "build", "--release", "--locked"],
      "artifact": "target/release/open-chat-tui",
      "dest": "internal/bundled/open-chat-tui"
    }

The script is intentionally best-effort: a missing tool, a failed build or a
cross-compilation target all just skip the bundle (and its tag), so the
integration still compiles and ``open-chat`` falls back to an externally
installed binary at runtime. Nothing here is integration-specific; the public
build only discovers bundles through the generated effective plan.

The tags that were successfully bundled are written to
``backend/.generated/bundle_tags`` (comma separated); ``full_build.sh`` appends
them to the Go build tags.
"""

from __future__ import annotations

import json
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Dict, List, Optional


def _host_goos() -> str:
    sys_platform = sys.platform
    if sys_platform.startswith("linux"):
        return "linux"
    if sys_platform == "darwin":
        return "darwin"
    if sys_platform.startswith(("win32", "cygwin", "msys")):
        return "windows"
    return sys_platform


def _host_goarch() -> str:
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64"):
        return "amd64"
    if machine in ("aarch64", "arm64"):
        return "arm64"
    if machine in ("i386", "i686", "x86"):
        return "386"
    return machine


def _log(message: str) -> None:
    print(f"[bundle] {message}", file=sys.stderr)


def _load_json(path: Path) -> Optional[Dict]:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        _log(f"ignoring {path}: {exc}")
        return None


def bundle(repo_root: Path) -> List[str]:
    plan_path = repo_root / "backend" / ".generated" / "integrations.effective.json"
    tags_path = repo_root / "backend" / ".generated" / "bundle_tags"
    tags_path.parent.mkdir(parents=True, exist_ok=True)

    if not plan_path.exists():
        _log(f"no effective plan at {plan_path}; nothing to bundle")
        return []

    plan = _load_json(plan_path)
    if not plan:
        return []

    selected = {m.get("id") for m in plan.get("modules", []) if m.get("selected")}

    host_os = _host_goos()
    host_arch = _host_goarch()
    target_os = os.environ.get("GOOS") or host_os
    target_arch = os.environ.get("GOARCH") or host_arch
    native = target_os == host_os and target_arch == host_arch

    tags: List[str] = []
    for module in plan.get("modules", []):
        if module.get("id") not in selected:
            continue
        integ_dir = repo_root / str(module.get("path", ""))
        spec_path = integ_dir / "integration.bundle.json"
        if not spec_path.exists():
            continue

        spec = _load_json(spec_path)
        if not spec:
            continue
        tag = str(spec.get("tag") or "").strip()
        if not tag:
            _log(f"{module.get('id')}: bundle spec without a tag; skipping")
            continue

        if not native or target_os == "windows":
            _log(
                f"{module.get('id')}: skipping bundle for non-native target "
                f"{target_os}/{target_arch} (host {host_os}/{host_arch})"
            )
            continue

        tool = str(spec.get("tool") or "").strip()
        if tool and shutil.which(tool) is None:
            _log(f"{module.get('id')}: required tool {tool!r} not found; skipping")
            continue

        build_cmd = spec.get("build") or []
        if not isinstance(build_cmd, list) or not build_cmd:
            _log(f"{module.get('id')}: bundle spec without a build command; skipping")
            continue

        _log(f"{module.get('id')}: building bundled asset with {' '.join(build_cmd)}")
        try:
            subprocess.run([str(part) for part in build_cmd], cwd=integ_dir, check=True)
        except (OSError, subprocess.CalledProcessError) as exc:
            _log(f"{module.get('id')}: bundle build failed ({exc}); skipping")
            continue

        artifact = integ_dir / str(spec.get("artifact") or "")
        if not artifact.is_file():
            _log(f"{module.get('id')}: bundle artifact {artifact} not found; skipping")
            continue

        dest = integ_dir / str(spec.get("dest") or "")
        if not str(spec.get("dest") or "").strip():
            _log(f"{module.get('id')}: bundle spec without a dest; skipping")
            continue
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(artifact, dest)
        _log(f"{module.get('id')}: embedded {dest.relative_to(repo_root)} (tag={tag})")
        if tag not in tags:
            tags.append(tag)

    tags_path.write_text(",".join(tags), encoding="utf-8")
    return tags


def main(argv: Optional[List[str]] = None) -> int:
    repo_root = Path(__file__).resolve().parents[2]
    if argv is None:
        argv = sys.argv[1:]
    if argv:
        repo_root = Path(argv[0]).resolve()
    try:
        tags = bundle(repo_root)
    except Exception as exc:  # noqa: BLE001 - never break the build
        _log(f"unexpected error: {exc}")
        tags = []
    if tags:
        _log(f"bundled tags: {','.join(tags)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
