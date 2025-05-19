#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import os
import subprocess
from pathlib import Path


CORE_MODULES = {
    "github.com/msgmate-io/mcp-integration",
    "github.com/msgmate-io/rest-api-tool-integration",
}


def run_go_mod_edit(modfile: Path, module_dir: Path, *args: str) -> None:
    cmd = ["go", "mod", "edit", f"-modfile={modfile}", *args]
    result = subprocess.run(cmd, check=False, cwd=module_dir, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode != 0:
        stderr = result.stderr.strip()
        if "not a known dependency" in stderr or "not in go.mod" in stderr:
            return
        raise RuntimeError(f"{' '.join(cmd)} failed: {stderr or result.stdout.strip()}")


def dep_selected(dep: dict[str, object], profile: str) -> bool:
    module = str(dep.get("module", "")).strip()
    default_enabled = bool(dep.get("default_enabled", False))
    if profile == "full":
        return True
    if profile == "core-only":
        return module in CORE_MODULES
    return default_enabled


def main() -> int:
    parser = argparse.ArgumentParser(description="Resolve effective integrations and create an optional effective modfile")
    parser.add_argument("--manifest", required=True, help="Path to integrationdeps.json")
    parser.add_argument("--profile", choices=["default", "core-only", "full"], default="default")
    parser.add_argument("--repo-root", required=True, help="Repository root used to resolve local paths")
    parser.add_argument("--base-go-mod", required=True, help="Path to base go.mod")
    parser.add_argument("--output-manifest", required=True, help="Path to write effective integration manifest")
    parser.add_argument("--output-modfile", required=True, help="Path to write effective go.mod")
    args = parser.parse_args()

    repo_root = Path(args.repo_root).resolve()
    manifest_path = Path(args.manifest).resolve()
    base_go_mod = Path(args.base_go_mod).resolve()
    output_manifest = Path(args.output_manifest).resolve()
    output_modfile = Path(args.output_modfile).resolve()

    payload = json.loads(manifest_path.read_text(encoding="utf-8"))
    raw_deps = payload.get("dependencies", [])
    if not isinstance(raw_deps, list):
        raise RuntimeError("manifest dependencies must be a list")

    all_modules: list[str] = []
    selected_deps: list[dict[str, str]] = []
    keep_local_replace: set[str] = set()

    for dep in raw_deps:
        if not isinstance(dep, dict):
            continue
        module = str(dep.get("module", "")).strip()
        import_path = str(dep.get("import", "")).strip()
        version = str(dep.get("version", "")).strip()
        local_path = str(dep.get("local_path", "")).strip()
        source = str(dep.get("source", "")).strip() or "submodule"

        if not module or not import_path:
            continue
        all_modules.append(module)

        if not dep_selected(dep, args.profile):
            continue

        use_local = False
        if local_path:
            local_go_mod = repo_root / local_path / "go.mod"
            use_local = local_go_mod.exists()

        # Submodule-backed integrations can fall back to remote modules when local path is absent.
        if source == "submodule" and local_path and not use_local:
            pass

        selected_deps.append({"module": module, "import": import_path, "version": version})
        if use_local:
            keep_local_replace.add(module)

    output_manifest.parent.mkdir(parents=True, exist_ok=True)
    output_manifest.write_text(json.dumps({"dependencies": selected_deps}, indent=2) + "\n", encoding="utf-8")

    output_modfile.parent.mkdir(parents=True, exist_ok=True)
    output_modfile.write_text(base_go_mod.read_text(encoding="utf-8"), encoding="utf-8")
    module_dir = base_go_mod.parent

    selected_modules = {dep["module"] for dep in selected_deps}
    for module in all_modules:
        if module not in selected_modules:
            run_go_mod_edit(output_modfile, module_dir, f"-droprequire={module}")
            run_go_mod_edit(output_modfile, module_dir, f"-dropreplace={module}")
            continue
        if module not in keep_local_replace:
            run_go_mod_edit(output_modfile, module_dir, f"-dropreplace={module}")

    # Local interface submodules can also be absent; prefer remote modules in that case.
    optional_interface_replaces = {
        "github.com/msgmate-io/go-tool-interface": "clients/go_tool_interface",
        "github.com/msgmate-io/go-integration-interface": "clients/go_integration_interface",
    }
    for module, local_path in optional_interface_replaces.items():
        local_mod = repo_root / local_path / "go.mod"
        if not local_mod.exists():
            run_go_mod_edit(output_modfile, module_dir, f"-dropreplace={module}")

    summary = {
        "profile": args.profile,
        "selected_modules": sorted(selected_modules),
        "output_manifest": str(output_manifest),
        "output_modfile": str(output_modfile),
    }
    print(json.dumps(summary, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
