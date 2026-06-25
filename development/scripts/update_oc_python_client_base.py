#!/usr/bin/env python3
from __future__ import annotations

import argparse
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CLIENT_ROOT = ROOT / "clients" / "oc_python_client"
DEFAULT_SWAGGER = ROOT / "backend" / "server" / "swagger.json"
GEN_CONFIG = CLIENT_ROOT / "openapi-python-client-config.yaml"
GEN_OUTPUT = CLIENT_ROOT / "client" / "generated_api"


def _run(command: list[str], cwd: Path) -> None:
    subprocess.run(command, cwd=cwd, check=True)


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
        openapi_path = temp_root / "openapi.json"
        generated_tmp = temp_root / "generated_api"

        _run(
            [
                "npx",
                "--yes",
                "swagger2openapi",
                str(swagger_path),
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

    print(f"Updated generated client at {GEN_OUTPUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
