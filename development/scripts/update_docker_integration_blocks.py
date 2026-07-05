#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import re
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
INTEGRATION_MANIFEST = ROOT / "backend" / "integrationdeps.json"
GO_MOD_FILE = ROOT / "backend" / "go.mod"
DOCKERFILES = [ROOT / "Dockerfile", ROOT / "dev.dockerfile"]

BEGIN_MANIFEST = "# BEGIN GENERATED: integration-mod-manifests"
END_MANIFEST = "# END GENERATED: integration-mod-manifests"
BEGIN_SOURCE = "# BEGIN GENERATED: integration-source-copies"
END_SOURCE = "# END GENERATED: integration-source-copies"
BEGIN_FRONTEND = "# BEGIN GENERATED: integration-frontend-asset-copies"
END_FRONTEND = "# END GENERATED: integration-frontend-asset-copies"

PLACEHOLDER_SEGMENT_MAP = {
    "table_name": "{table_name}",
    "id": "{id}",
    "tool_uuid": "tool-uuid-placeholder",
    "key_uuid": "key-uuid-placeholder",
    "server_uuid": "server-uuid-placeholder",
}


@dataclass
class IntegrationLocalPath:
    module: str
    context_path: str


@dataclass
class IntegrationFrontendAssetCopy:
    context_path: str
    integration_name: str
    dist_relative_path: str
    asset_relative_path: str


def load_manifest_dependencies() -> list[dict[str, str]]:
    data = json.loads(INTEGRATION_MANIFEST.read_text(encoding="utf-8"))
    deps = data.get("dependencies")
    if not isinstance(deps, list):
        raise RuntimeError("backend/integrationdeps.json missing dependencies list")
    out: list[dict[str, str]] = []
    for idx, dep in enumerate(deps):
        if not isinstance(dep, dict):
            raise RuntimeError(f"dependencies[{idx}] must be an object")
        module = str(dep.get("module", "")).strip()
        import_path = str(dep.get("import", "")).strip()
        out.append({"module": module, "import": import_path})
    return out


def parse_local_replace_map() -> dict[str, str]:
    replace_map: dict[str, str] = {}
    pattern = re.compile(r"^replace\s+(\S+)\s*=>\s*(\S+)")
    for raw_line in GO_MOD_FILE.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        match = pattern.match(line)
        if not match:
            continue
        module = match.group(1).strip()
        target = match.group(2).strip()
        if target.startswith("../"):
            target_abs = (GO_MOD_FILE.parent / target).resolve()
            try:
                target_rel = target_abs.relative_to(ROOT)
            except ValueError:
                continue
            replace_map[module] = target_rel.as_posix()
    return replace_map


def resolve_local_integrations() -> list[IntegrationLocalPath]:
    deps = load_manifest_dependencies()
    replace_map = parse_local_replace_map()

    out: list[IntegrationLocalPath] = []
    seen: set[str] = set()
    for dep in deps:
        module = dep["module"] or dep["import"]
        if not module or module in seen:
            continue
        local_path = replace_map.get(module)
        if not local_path:
            continue
        if not local_path.startswith("clients/integrations/"):
            continue
        seen.add(module)
        out.append(IntegrationLocalPath(module=module, context_path=local_path))
    return out


def replace_block(content: str, begin: str, end: str, lines: list[str]) -> str:
    start_idx = content.find(begin)
    end_idx = content.find(end)
    if start_idx < 0 or end_idx < 0 or end_idx < start_idx:
        raise RuntimeError(f"missing block markers: {begin} ... {end}")
    end_idx += len(end)
    replacement = begin + "\n"
    if lines:
        replacement += "\n".join(lines) + "\n"
    replacement += end
    return content[:start_idx] + replacement + content[end_idx:]


def render_manifest_lines(local_integrations: list[IntegrationLocalPath]) -> list[str]:
    lines: list[str] = []
    for item in local_integrations:
        lines.append(f"COPY {item.context_path}/go.mod /{item.context_path}/go.mod")
        sum_file = ROOT / item.context_path / "go.sum"
        if sum_file.exists():
            lines.append(f"COPY {item.context_path}/go.sum /{item.context_path}/go.sum")
    return lines


def render_source_lines(local_integrations: list[IntegrationLocalPath]) -> list[str]:
    return [f"COPY {item.context_path} /{item.context_path}" for item in local_integrations]


def detect_integration_name(context_path: str) -> str | None:
    integration_dir = ROOT / context_path
    go_files = sorted(integration_dir.glob("*.go"))
    marker = "integrationinterface.MustRegister(integrationinterface.Definition{"
    name_pattern = re.compile(r'Name:\s*"([a-z0-9_]+)"')

    for go_file in go_files:
        lines = go_file.read_text(encoding="utf-8").splitlines()
        for idx, line in enumerate(lines):
            if marker not in line:
                continue
            for look_ahead in range(idx, min(idx + 80, len(lines))):
                match = name_pattern.search(lines[look_ahead])
                if match:
                    return match.group(1)
    return None


def map_asset_rel_to_dist_rel(asset_relative_path: str) -> str:
    parts = Path(asset_relative_path).parts
    mapped = [PLACEHOLDER_SEGMENT_MAP.get(part, part) for part in parts]
    return Path(*mapped).as_posix()


def collect_frontend_asset_copies(local_integrations: list[IntegrationLocalPath]) -> list[IntegrationFrontendAssetCopy]:
    out: list[IntegrationFrontendAssetCopy] = []
    for item in local_integrations:
        integration_name = detect_integration_name(item.context_path)
        if not integration_name:
            continue
        assets_root = ROOT / item.context_path / "frontend_assets"
        if not assets_root.exists() or not assets_root.is_dir():
            continue
        for html_file in sorted(assets_root.rglob("*.html")):
            asset_relative = html_file.relative_to(assets_root).as_posix()
            dist_relative = map_asset_rel_to_dist_rel(asset_relative)
            out.append(
                IntegrationFrontendAssetCopy(
                    context_path=item.context_path,
                    integration_name=integration_name,
                    dist_relative_path=dist_relative,
                    asset_relative_path=asset_relative,
                )
            )
    return out


def render_frontend_asset_lines(local_integrations: list[IntegrationLocalPath]) -> list[str]:
    lines: list[str] = []
    for item in collect_frontend_asset_copies(local_integrations):
        lines.append(
            "COPY --from=frontend "
            f"/frontend/dist/client/integrations/{item.integration_name}/{item.dist_relative_path} "
            f"/{item.context_path}/frontend_assets/{item.asset_relative_path}"
        )
    return lines


def replace_block_if_present(content: str, begin: str, end: str, lines: list[str]) -> str:
    if begin not in content and end not in content:
        return content
    return replace_block(content, begin, end, lines)


def update_dockerfile(path: Path, local_integrations: list[IntegrationLocalPath]) -> bool:
    original = path.read_text(encoding="utf-8")
    updated = replace_block(original, BEGIN_MANIFEST, END_MANIFEST, render_manifest_lines(local_integrations))
    updated = replace_block(updated, BEGIN_SOURCE, END_SOURCE, render_source_lines(local_integrations))
    updated = replace_block_if_present(updated, BEGIN_FRONTEND, END_FRONTEND, render_frontend_asset_lines(local_integrations))
    if updated == original:
        return False
    path.write_text(updated, encoding="utf-8")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description="Update Dockerfile integration copy blocks from integrationdeps.json")
    parser.add_argument("--check", action="store_true", help="Exit non-zero if files need updates")
    args = parser.parse_args()

    local_integrations = resolve_local_integrations()
    changed_files: list[Path] = []

    for dockerfile in DOCKERFILES:
        before = dockerfile.read_text(encoding="utf-8")
        after = replace_block(before, BEGIN_MANIFEST, END_MANIFEST, render_manifest_lines(local_integrations))
        after = replace_block(after, BEGIN_SOURCE, END_SOURCE, render_source_lines(local_integrations))
        after = replace_block_if_present(after, BEGIN_FRONTEND, END_FRONTEND, render_frontend_asset_lines(local_integrations))
        if after != before:
            if args.check:
                changed_files.append(dockerfile)
            else:
                dockerfile.write_text(after, encoding="utf-8")
                changed_files.append(dockerfile)

    if args.check and changed_files:
        print("Docker integration blocks are out of date:")
        for changed in changed_files:
            print(f"- {changed.relative_to(ROOT)}")
        return 1

    if changed_files:
        for changed in changed_files:
            print(f"Updated {changed.relative_to(ROOT)}")
    else:
        print("Docker integration blocks already up to date")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
