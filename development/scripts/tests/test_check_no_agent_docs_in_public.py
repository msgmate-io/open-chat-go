#!/usr/bin/env python3
"""Unit tests for development/scripts/check_no_agent_docs_in_public.py (ci#736).

Run: python3 -m unittest development.scripts.tests.test_check_no_agent_docs_in_public
or:  python3 development/scripts/tests/test_check_no_agent_docs_in_public.py
"""

from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

_SCRIPT = Path(__file__).resolve().parents[1] / "check_no_agent_docs_in_public.py"
_spec = importlib.util.spec_from_file_location("check_no_agent_docs_in_public", _SCRIPT)
assert _spec and _spec.loader
guard = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(guard)


class GuardTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def _write(self, rel: str, content: str = "") -> Path:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def test_clean_tree_passes(self) -> None:
        self._write("DEVELOPMENT.md", "# Development\n\nBuild with make.\n")
        self._write("docs/setup.md", "human docs\n")
        self.assertEqual(guard.main(["--root", str(self.root)]), 0)

    def test_devlead_toolkit_path_fails(self) -> None:
        self._write("DEVELOPMENT.md", "# Development\n")
        self._write("development/scripts/devlead/fleet.py", "print('x')\n")
        self.assertEqual(guard.main(["--root", str(self.root)]), 1)
        self.assertTrue(
            any(
                "development/scripts/devlead/fleet.py" in v
                for v in guard.check_paths(self.root)
            )
        )

    def test_public_docs_agent_playbook_fails(self) -> None:
        self._write("docs/ci-733-devlead-fleet-moderation-playbook.md", "# x\n")
        self.assertEqual(guard.main(["--root", str(self.root)]), 1)

    def test_agent_playbook_glob_fails(self) -> None:
        self._write("docs/agent-playbook-devbot.md", "# x\n")
        violations = guard.check_paths(self.root)
        self.assertTrue(any("agent-playbook" in v for v in violations), violations)

    def test_development_md_agent_markers_fail(self) -> None:
        self._write(
            "DEVELOPMENT.md",
            "# Development\n\n"
            "### Dev-lead fleet moderation\n\n"
            "Run `development/scripts/devlead/fleet.py` and follow the\n"
            "agent-only playbook for handovers.\n",
        )
        violations = guard.check_development_md(self.root)
        self.assertGreaterEqual(len(violations), 3, violations)
        self.assertEqual(guard.main(["--root", str(self.root)]), 1)

    def test_human_sections_pass(self) -> None:
        self._write(
            "DEVELOPMENT.md",
            "# Development\n\n"
            "### Setup\n\n### Build\n\n### Test\n\n### Deploy\n\n"
            "### Contributing\n\n### Public architecture\n",
        )
        self.assertEqual(guard.check_development_md(self.root), [])
        self.assertEqual(guard.main(["--root", str(self.root)]), 0)


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
