#!/usr/bin/env python3
"""No-Go self-test of the dep-surface measurement and gate (ci#423).

The real measurement needs a full Go workspace and must stay out of fast local
CI. This test substitutes plain directories whose go.mod files *declare* the
module paths, drives the same code paths that CI exercises ("module discovery"
and "baseline diff/gate" plus the malformed-baseline rejection) and asserts the
exact behavior. It imports the script by path so it keeps testing the tree it
lives in, and it needs neither a Go toolchain nor a network.
"""

import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "backend" / "scripts"))
import integration_dep_surface as dep  # noqa: E402


def make_checkout(tmp: Path, name: str, module: str) -> None:
    d = tmp / "clients" / "integrations" / name
    d.mkdir(parents=True)
    (d / "go.mod").write_text(f"module {module}\n", encoding="utf-8")


class DepSurfaceFixtureTest(unittest.TestCase):
    def test_module_names_skips_duplicates(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            base = Path(td)
            make_checkout(base, "alpha-go", "example.com/alpha")
            make_checkout(base, "beta-go", "example.com/beta")
            make_checkout(base, "msgmate_coder_ask_wt", "github.com/msgmate-io/msgmate-coder-ask-integration")
            names = dep.module_names(base / "clients" / "integrations")
            self.assertEqual(names, [("alpha-go", "example.com/alpha"), ("beta-go", "example.com/beta")])

    def test_check_baseline_rejects_malformed_and_coupled(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            broken = Path(td) / "baseline.json"
            broken.write_text(json.dumps({"version": 1, "integrations": {"example.com/alpha": {"backend": 1}}}), encoding="utf-8")
            buf = io.StringIO()
            with redirect_stderr(buf):
                code = dep.check_baseline(broken)
            self.assertEqual(code, 2)
            self.assertIn("backend/backend_packages missing", buf.getvalue())
            coupled = Path(td) / "coupled.json"
            coupled.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "integrations": {
                            "example.com/alpha": {"backend": 2, "backend_packages": ["backend/x", "backend/y"], "packages": 10}
                        },
                    }
                ),
                encoding="utf-8",
            )
            buf = io.StringIO()
            with redirect_stderr(buf):
                code = dep.check_baseline(coupled)
            self.assertEqual(code, 1)
            self.assertIn("pins integrations that are not decoupled", buf.getvalue())


if __name__ == "__main__":
    unittest.main()
