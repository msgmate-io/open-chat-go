#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import subprocess
from pathlib import Path
from urllib.parse import quote


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_SWAGGER = ROOT / "backend" / "server" / "swagger.json"


def _run_manifest_export() -> dict:
    result = subprocess.run(
        ["go", "run", "./scripts/tool_typing_manifest"],
        cwd=ROOT / "backend",
        check=True,
        capture_output=True,
        text=True,
    )
    payload = json.loads(result.stdout)
    if not isinstance(payload, dict):
        raise RuntimeError("tool typing manifest must be a JSON object")
    return payload


def _sanitize_operation_suffix(tool_name: str) -> str:
    base = re.sub(r"[^a-zA-Z0-9]+", "_", tool_name).strip("_")
    return base or "tool"


def _ensure_definition(swagger: dict, name: str, schema: dict) -> str:
    definitions = swagger.setdefault("definitions", {})
    if not isinstance(definitions, dict):
        raise RuntimeError("swagger definitions must be an object")
    key = f"tools.{name}"
    definitions[key] = schema
    return f"#/definitions/{key}"


def _build_validate_operation(
    *,
    summary: str,
    description: str,
    operation_id: str,
    payload_ref: str,
    tool_name: str,
    kind: str,
) -> dict:
    return {
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "tags": ["tools"],
        "summary": summary,
        "description": description,
        "operationId": operation_id,
        "parameters": [
            {
                "name": "payload",
                "in": "body",
                "required": True,
                "schema": {"$ref": payload_ref},
            }
        ],
        "responses": {
            "200": {
                "description": "Payload is valid",
                "schema": {"$ref": "#/definitions/tools.ToolValidatePayloadResponse"},
            },
            "400": {"description": "Invalid payload"},
        },
        "x-tool-name": tool_name,
        "x-tool-kind": kind,
    }


def augment_swagger(swagger: dict, manifest: dict) -> dict:
    paths = swagger.setdefault("paths", {})
    if not isinstance(paths, dict):
        raise RuntimeError("swagger paths must be an object")

    rows = manifest.get("rows")
    if not isinstance(rows, list):
        raise RuntimeError("manifest.rows must be a list")

    for row in rows:
        if not isinstance(row, dict):
            continue
        tool_name = str(row.get("name", "")).strip()
        if not tool_name:
            continue

        call_type_name = str(row.get("call_type_name", "")).strip() or "ToolCall"
        call_schema = row.get("call_schema") if isinstance(row.get("call_schema"), dict) else {"type": "object"}
        call_ref = _ensure_definition(swagger, call_type_name, call_schema)

        suffix = _sanitize_operation_suffix(tool_name)
        encoded_tool = quote(tool_name, safe="")
        call_path = f"/api/v1/tools/typing/{encoded_tool}/call/validate"
        paths[call_path] = {
            "post": _build_validate_operation(
                summary=f"Validate {tool_name} call payload",
                description=f"Validate payload for tool `{tool_name}` call arguments.",
                operation_id=f"post_api_v1_tools_typing_{suffix}_call_validate",
                payload_ref=call_ref,
                tool_name=tool_name,
                kind="call",
            )
        }

        if bool(row.get("requires_init")):
            init_type_name = str(row.get("init_type_name", "")).strip() or f"{call_type_name}Init"
            init_schema = row.get("init_schema") if isinstance(row.get("init_schema"), dict) else {"type": "object"}
            init_ref = _ensure_definition(swagger, init_type_name, init_schema)
            init_path = f"/api/v1/tools/typing/{encoded_tool}/init/validate"
            paths[init_path] = {
                "post": _build_validate_operation(
                    summary=f"Validate {tool_name} init payload",
                    description=f"Validate payload for tool `{tool_name}` init configuration.",
                    operation_id=f"post_api_v1_tools_typing_{suffix}_init_validate",
                    payload_ref=init_ref,
                    tool_name=tool_name,
                    kind="init",
                )
            }

    return swagger


def main() -> int:
    parser = argparse.ArgumentParser(description="Inject per-tool typed OpenAPI endpoints into Swagger 2.0 schema")
    parser.add_argument("--swagger-path", default=str(DEFAULT_SWAGGER), help="Path to swagger.json")
    args = parser.parse_args()

    swagger_path = Path(args.swagger_path).resolve()
    if not swagger_path.exists():
        raise FileNotFoundError(f"Swagger schema not found: {swagger_path}")

    with swagger_path.open("r", encoding="utf-8") as handle:
        swagger = json.load(handle)

    manifest = _run_manifest_export()
    augmented = augment_swagger(swagger, manifest)

    with swagger_path.open("w", encoding="utf-8") as handle:
        json.dump(augmented, handle, indent=4, sort_keys=True)
        handle.write("\n")

    row_count = len(manifest.get("rows", [])) if isinstance(manifest.get("rows"), list) else 0
    print(f"Augmented {swagger_path} with typed endpoints for {row_count} tools")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
