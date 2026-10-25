#!/usr/bin/env python3
"""Regression guard for the ci#756 dev-checkout lockfile restore.

A non-frozen ``openchat-integrations prepare``/``sync`` re-pins the tracked
``integrations.lock.json`` to the commits currently checked out under
``clients/integrations``. Those drift ahead of the committed lock, so the file
ends up dirty and every ``git switch``/``git checkout`` in the developer's
checkout refuses with "local changes would be overwritten".

``integration_sync_entrypoint.run_and_restore_lockfile`` runs the manager and
restores the developer's snapshot afterwards. These tests pin that behaviour:

  * ``prepare``/``sync`` restore the snapshot when the manager changed it;
  * other commands are passed through without touching the file;
  * a missing lockfile is not an error.

Deliberately stdlib-only and hermetic: the manager is replaced by a fake that
only rewrites the lockfile, so no network, git or integration checkouts are
needed.
"""

from __future__ import annotations

import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import integration_sync_entrypoint as entry  # noqa: E402

COMMITTED = '{"version": 1, "integrations": {"mcp": {"commit": "committed"}}}'
DRIFTED = '{"version": 1, "integrations": {"mcp": {"commit": "drifted"}}}'


class RunAndRestoreLockfileTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.repo = entry.Path(self._tmp.name)
        self.lock = self.repo / entry.TRACKED_LOCKFILE
        self._real_call = entry.subprocess.call

    def tearDown(self) -> None:
        entry.subprocess.call = self._real_call
        self._tmp.cleanup()

    def _fake_manager(self, written: str):
        def fake(argv, *args, **kwargs):
            (self.repo / entry.TRACKED_LOCKFILE).write_text(written)
            fake.argv = argv
            return 0

        return fake

    def test_prepare_restores_the_developer_snapshot(self) -> None:
        self.lock.write_text(COMMITTED)
        entry.subprocess.call = self._fake_manager(DRIFTED)
        rc = entry.run_and_restore_lockfile(self.repo, ["prepare"])
        self.assertEqual(rc, 0)
        self.assertEqual(self.lock.read_text(), COMMITTED)

    def test_sync_restores_the_developer_snapshot(self) -> None:
        self.lock.write_text(COMMITTED)
        entry.subprocess.call = self._fake_manager(DRIFTED)
        entry.run_and_restore_lockfile(self.repo, ["sync", "--profile", "core-only"])
        self.assertEqual(self.lock.read_text(), COMMITTED)

    def test_other_commands_are_left_untouched(self) -> None:
        self.lock.write_text(COMMITTED)
        entry.subprocess.call = self._fake_manager(DRIFTED)
        entry.run_and_restore_lockfile(self.repo, ["resolve", "--profile", "core-only"])
        self.assertEqual(self.lock.read_text(), DRIFTED)

    def test_missing_lockfile_is_not_an_error(self) -> None:
        entry.subprocess.call = self._fake_manager(DRIFTED)
        rc = entry.run_and_restore_lockfile(self.repo, ["prepare"])
        self.assertEqual(rc, 0)
        # The manager's output is kept when the developer had no snapshot.
        self.assertEqual(self.lock.read_text(), DRIFTED)

    def test_unchanged_lockfile_is_not_rewritten(self) -> None:
        self.lock.write_text(COMMITTED)
        # A manager that does not change the file: the snapshot write must be a
        # no-op (mtime-preserving) rather than a rewrite.
        before = self.lock.stat().st_mtime_ns

        def fake(argv, *args, **kwargs):
            return 0

        entry.subprocess.call = fake
        entry.run_and_restore_lockfile(self.repo, ["prepare"])
        self.assertEqual(self.lock.read_text(), COMMITTED)
        self.assertEqual(self.lock.stat().st_mtime_ns, before)


if __name__ == "__main__":
    unittest.main()
