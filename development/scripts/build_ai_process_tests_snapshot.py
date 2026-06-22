#!/usr/bin/env python3
"""Compatibility wrapper for moved AI process snapshot script."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
TARGET = ROOT / "development" / "ci" / "ai_process_tests" / "build_snapshot.py"


def main() -> int:
    command = [sys.executable, str(TARGET), *sys.argv[1:]]
    return subprocess.call(command)


if __name__ == "__main__":
    raise SystemExit(main())
