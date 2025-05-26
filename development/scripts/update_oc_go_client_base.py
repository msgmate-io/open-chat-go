#!/usr/bin/env python3
from __future__ import annotations

import argparse
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CLIENT_ROOT = ROOT / "clients" / "integrations" / "go_client_integration"
DEFAULT_SWAGGER = ROOT / "backend" / "server" / "swagger.json"
GEN_CONFIG = CLIENT_ROOT / "oapi-codegen-config.yaml"
GEN_OUTPUT = CLIENT_ROOT / "generated_api" / "client.gen.go"


def _run(command: list[str], cwd: Path) -> None:
    subprocess.run(command, cwd=cwd, check=True)


def main() -> int:
    parser = argparse.ArgumentParser(description="Regenerate typed Go client from backend Swagger docs")
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

    with tempfile.TemporaryDirectory(prefix="oc-go-client-gen-") as tmp:
        temp_root = Path(tmp)
        swagger_augmented = temp_root / "swagger.augmented.json"
        openapi_path = temp_root / "openapi.json"
        generated_tmp = temp_root / "client.gen.go"

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
                "go",
                "run",
                "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1",
                "-config",
                str(GEN_CONFIG),
                "-o",
                str(generated_tmp),
                str(openapi_path),
            ],
            cwd=ROOT,
        )

        GEN_OUTPUT.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(generated_tmp, GEN_OUTPUT)

    print(f"Updated generated Go client at {GEN_OUTPUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
