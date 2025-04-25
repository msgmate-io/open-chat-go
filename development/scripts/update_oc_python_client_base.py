#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CLIENT_ROOT = ROOT / "clients" / "oc_python_client"
DEFAULT_SWAGGER = ROOT / "backend" / "server" / "swagger.json"
GEN_CONFIG = CLIENT_ROOT / "openapi-python-client-config.yaml"
GEN_OUTPUT = CLIENT_ROOT / "client" / "generated_api"
TOOL_ENUM_OUTPUT = CLIENT_ROOT / "client" / "tool_names.py"


def _run(command: list[str], cwd: Path) -> None:
    subprocess.run(command, cwd=cwd, check=True)


def _enum_member_name(tool_name: str) -> str:
    name = re.sub(r"[^A-Za-z0-9]+", "_", tool_name).strip("_").upper()
    if not name:
        name = "UNKNOWN_TOOL"
    if name[0].isdigit():
        name = f"TOOL_{name}"
    return name


def _generate_tool_name_enum() -> None:
    manifest = subprocess.run(
        ["go", "run", "./scripts/tool_typing_manifest"],
        cwd=ROOT / "backend",
        check=True,
        capture_output=True,
        text=True,
    )
    payload = json.loads(manifest.stdout)
    rows = payload.get("rows") if isinstance(payload, dict) else None
    if not isinstance(rows, list):
        rows = []

    tools: list[str] = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        name = str(row.get("name", "")).strip()
        if not name:
            continue
        tools.append(name)
    tools = sorted(set(tools))

    lines: list[str] = [
        "from __future__ import annotations",
        "",
        "from enum import StrEnum",
        "",
        "",
        "class ToolName(StrEnum):",
    ]
    if not tools:
        lines.append("    pass")
    else:
        for tool_name in tools:
            lines.append(f"    {_enum_member_name(tool_name)} = {tool_name!r}")
    lines.append("")
    TOOL_ENUM_OUTPUT.write_text("\n".join(lines), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description="Regenerate typed Python client from backend Swagger docs")
    parser.add_argument(
        "--swagger-path",
        default=str(DEFAULT_SWAGGER),
        help="Path to Swagger 2.0 JSON input (default: backend/server/swagger.json)",
    )
    args = parser.parse_args()

    swagger_path = Path(args.swagger_path).resolve()
    if not swagger_path.exists():
        raise FileNotFoundError(f"Swagger schema not found: {swagger_path}")

    if not GEN_CONFIG.exists():
        raise FileNotFoundError(f"Generator config not found: {GEN_CONFIG}")

    with tempfile.TemporaryDirectory(prefix="oc-py-client-gen-") as tmp:
        temp_root = Path(tmp)
        swagger_augmented = temp_root / "swagger.augmented.json"
        openapi_path = temp_root / "openapi.json"
        generated_tmp = temp_root / "generated_api"

        shutil.copyfile(swagger_path, swagger_augmented)

        _run(
            [
                "python3",
                str(ROOT / "development" / "scripts" / "augment_swagger_with_tool_typed_endpoints.py"),
                "--swagger-path",
                str(swagger_augmented),
            ],
            cwd=ROOT,
        )

        _run(
            [
                "npx",
                "--yes",
                "swagger2openapi",
                str(swagger_augmented),
                "-o",
                str(openapi_path),
            ],
            cwd=ROOT,
        )

        _run(
            [
                "python3",
                "-m",
                "openapi_python_client",
                "generate",
                "--path",
                str(openapi_path),
                "--output-path",
                str(generated_tmp),
                "--meta",
                "none",
                "--config",
                str(GEN_CONFIG),
                "--overwrite",
            ],
            cwd=ROOT,
        )

        if GEN_OUTPUT.exists():
            shutil.rmtree(GEN_OUTPUT)
        shutil.copytree(generated_tmp, GEN_OUTPUT)

    _generate_tool_name_enum()

    print(f"Updated generated client at {GEN_OUTPUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
