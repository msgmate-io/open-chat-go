#!/usr/bin/env python3
"""No-Go check that the committed Host surface baselines still match the
integrationinterface module's declared surface (ci#423 boundary guard).

This re-runs the two hermetic gates that PR #1317 added (the host-surface
conformance baseline and the dependency-surface checkout baseline) against the
*committed* baseline files, so a follow-up cannot land a surface regression
without Go. It needs neither a Go toolchain nor a network.
"""

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "development" / "scripts"))
import check_host_surface as surface  # noqa: E402


def main() -> int:
    fails = 0

    print("== host-surface baseline (committed) ==\n")
    baseline = ROOT / "integrations.host-surface.json"
    try:
        surface.load(str(baseline))
        code = surface.report(surface.validate(surface.load(str(baseline))), [])
        if code != 0:
            raise SystemExit(code)
        print("ok: integrations.host-surface.json matches the surface guard")
    except SystemExit as exc:
        code = exc.code if isinstance(exc.code, int) else 1
        print(f"FAIL: host-surface baseline (exit {code})")
        fails += 1

    print("== dep-surface baseline (committed) ==")
    dep_baseline = ROOT / "integrations.dep-surface.json"
    try:
        data = json.loads(dep_baseline.read_text(encoding="utf-8"))
        if data.get("version") != 1 or not data.get("integrations"):
            raise ValueError("malformed dep-surface baseline")
        for name, row in data["integrations"].items():
            if not isinstance(row, dict) or "backend" not in row:
                raise ValueError(f"malformed dep-surface entry {name}")
            if row["backend"] != 0 or row.get("backend_packages"):
                print(f"WARN: {name} is pinned with backend refs (not decoupled)")
        print("ok: integrations.dep-surface.json is well-formed")
    except Exception as exc:  # noqa: BLE001
        print(f"FAIL: dep-surface baseline: {exc}")
        fails += 1

    if fails:
        print(f"{fails} ci#423 baseline check(s) failed")
        return 1
    print("ci#423 committed-baseline checks: all pass")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())