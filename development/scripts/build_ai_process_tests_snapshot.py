#!/usr/bin/env python3
"""Build a static JSON snapshot for AI process (Python) tests docs.

Scans:
- development/ci/ai_process_tests/mocked/test_*.py
- development/ci/ai_process_tests/real/test_*.py

Extracts pytest test functions and their docstrings, then writes deterministic JSON
consumed by frontend docs components.
"""

from __future__ import annotations

import ast
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUT = ROOT / "frontend" / "packages" / "ui" / "src" / "components" / "docs" / "ai-process-tests.static.json"
REPO_SOURCE_BASE_URL = "https://github.com/msgmate-io/open-chat-go/blob/main"


@dataclass
class TestItem:
    test_id: str
    name: str
    suite: str
    file_path: str
    line: int
    source_url: str
    description: str


def _iter_suite_files() -> Iterable[tuple[str, Path]]:
    base = ROOT / "development" / "ci" / "ai_process_tests"
    for suite in ("mocked", "real"):
        suite_dir = base / suite
        if not suite_dir.exists():
            continue
        for path in sorted(suite_dir.glob("test_*.py")):
            if path.is_file():
                yield suite, path


def _normalize_doc(value: str | None) -> str:
    if not value:
        return ""
    return " ".join(value.strip().split())


def collect_items() -> list[TestItem]:
    items: list[TestItem] = []

    for suite, path in _iter_suite_files():
        source = path.read_text(encoding="utf-8")
        tree = ast.parse(source)
        rel = path.relative_to(ROOT).as_posix()

        for node in tree.body:
            if not isinstance(node, ast.FunctionDef):
                continue
            if not node.name.startswith("test_"):
                continue

            line = int(getattr(node, "lineno", 1) or 1)
            source_url = f"{REPO_SOURCE_BASE_URL}/{rel}#L{line}"
            description = _normalize_doc(ast.get_docstring(node))
            test_id = f"{suite}:{rel}:{node.name}"

            items.append(
                TestItem(
                    test_id=test_id,
                    name=node.name,
                    suite=suite,
                    file_path=rel,
                    line=line,
                    source_url=source_url,
                    description=description,
                )
            )

    items.sort(key=lambda item: (item.suite, item.file_path, item.name))
    return items


def build_payload() -> dict:
    tests = [item.__dict__ for item in collect_items()]
    return {
        "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "tests": tests,
    }


def main() -> int:
    import argparse

    parser = argparse.ArgumentParser(description="Build AI process tests static snapshot JSON")
    parser.add_argument("--output", default=str(DEFAULT_OUT), help="Output JSON path")
    parser.add_argument("--stdout", action="store_true", help="Print JSON to stdout instead of writing file")
    args = parser.parse_args()

    payload = build_payload()
    encoded = json.dumps(payload, indent=2) + "\n"

    if args.stdout:
        print(encoded, end="")
        return 0

    out = Path(args.output)
    if not out.is_absolute():
        out = ROOT / out
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(encoded, encoding="utf-8")
    print(f"Wrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
