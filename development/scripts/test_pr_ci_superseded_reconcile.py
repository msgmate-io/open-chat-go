#!/usr/bin/env python3
"""Unit tests for development/scripts/pr_ci_superseded_reconcile.py (ci#741).

Locks the acceptance criteria of the superseded-PR-CI-run gate fix: a cancelled
gate is accepted only when the commit no longer needs gating (PR closed, or a
newer commit is the live PR head) or when a *replacement* run for the exact same
commit reached the gate job with a real verdict, whose conclusion is adopted.
Everything else fails closed.
"""
import argparse
import os
import sys
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import pr_ci_superseded_reconcile as mod  # noqa: E402

GATE = mod.GATE_JOB_NAME
HEAD = "faf784c5f832f144ad2d8f33bca10fd0c4d7995a"
CURRENT = 39245536652
NEWER = 39246623047
LABELS = [{"name": "test"}]


def run(run_id, status="completed", attempt=1):
    return {"id": run_id, "status": status, "run_attempt": attempt}


def gate(name=GATE, status="completed", conclusion="success"):
    return {"name": name, "status": status, "conclusion": conclusion}


def open_pr(head=HEAD, state="open"):
    return {"state": state, "head": {"sha": head}, "labels": LABELS}


class FakeApi:
    """Stand-in for the module-level ``_FETCH`` hook."""

    def __init__(self, pull=None, runs=(), jobs=None, error=None):
        self.pull = pull if pull is not None else open_pr()
        self.runs = list(runs)
        self.jobs = jobs or {}
        self.error = error

    def __call__(self, url, token):
        if self.error is not None:
            raise self.error
        if "/pulls/" in url:
            return self.pull
        if "/jobs" in url:
            run_id = int(url.split("/actions/runs/")[1].split("/")[0])
            return {"jobs": self.jobs.get(run_id, [])}
        return {"workflow_runs": self.runs}


def ns(**over):
    base = dict(
        repo="msgmate-io/open-chat-go",
        pr=835,
        head_sha=HEAD,
        run_id=CURRENT,
        wait_seconds=0,
    )
    base.update(over)
    return argparse.Namespace(**base)


class ReconcileTests(unittest.TestCase):
    def setUp(self):
        self._fetch = mod._FETCH
        self._mono = mod._MONOTONIC
        self._sleep = mod._SLEEP
        self.now = [1000.0]
        mod._MONOTONIC = lambda: self.now[0]
        mod._SLEEP = lambda seconds: self.now.__setitem__(0, self.now[0] + 1)

    def tearDown(self):
        mod._FETCH = self._fetch
        mod._MONOTONIC = self._mono
        mod._SLEEP = self._sleep

    def reconcile(self, api, **over):
        mod._FETCH = api
        return mod.reconcile(ns(**over), "token")

    def test_replacement_green_is_accepted(self):
        api = FakeApi(runs=[run(CURRENT), run(NEWER)], jobs={NEWER: [gate()]})
        code, summary = self.reconcile(api)
        self.assertEqual(code, 0)
        self.assertIn("replacement run %s gate job concluded success" % NEWER, summary)

    def test_replacement_red_is_rejected(self):
        api = FakeApi(
            runs=[run(CURRENT), run(NEWER)],
            jobs={NEWER: [gate(conclusion="failure")]},
        )
        code, summary = self.reconcile(api)
        self.assertEqual(code, 1)
        self.assertTrue(summary.startswith("rejected:"))
        self.assertIn("concluded failure", summary)

    def test_cancelled_run_is_never_its_own_replacement(self):
        api = FakeApi(runs=[run(CURRENT)], jobs={CURRENT: [gate()]})
        code, summary = self.reconcile(api)
        self.assertEqual(code, 3)
        self.assertIn("no replacement run", summary)

    def test_closed_pr_is_accepted(self):
        api = FakeApi(pull=open_pr(state="closed"), runs=[run(CURRENT)])
        code, summary = self.reconcile(api)
        self.assertEqual(code, 0)
        self.assertIn("no longer gates anything", summary)

    def test_moved_head_is_accepted(self):
        api = FakeApi(pull=open_pr(head="deadbeef" + HEAD), runs=[run(CURRENT)])
        code, summary = self.reconcile(api)
        self.assertEqual(code, 0)
        self.assertIn("obsolete commit", summary)

    def test_runs_without_the_gate_job_do_not_testify(self):
        api = FakeApi(
            runs=[run(CURRENT), run(NEWER)],
            jobs={NEWER: [gate(name="Run PR CI Go Tests")]},
        )
        code, summary = self.reconcile(api)
        self.assertEqual(code, 2)
        self.assertIn("never gated", summary)

    def test_cancelled_gate_job_is_skipped(self):
        api = FakeApi(
            runs=[run(CURRENT), run(NEWER)],
            jobs={NEWER: [gate(conclusion="cancelled")]},
        )
        code, _ = self.reconcile(api)
        self.assertEqual(code, 2)

    def test_pending_replacement_times_out_closed(self):
        api = FakeApi(runs=[run(CURRENT), run(NEWER, status="in_progress")], jobs={})
        code, summary = self.reconcile(api, wait_seconds=0)
        self.assertEqual(code, 3)
        self.assertIn("still in_progress", summary)

    def test_api_error_fails_closed(self):
        mod._FETCH = FakeApi(error=ValueError("boom"))
        env = {"GITHUB_TOKEN": "token"}
        with mock.patch.dict(os.environ, env, clear=False):
            code = mod.main(
                [
                    "--repo",
                    "msgmate-io/open-chat-go",
                    "--pr",
                    "835",
                    "--head-sha",
                    HEAD,
                    "--run-id",
                    str(CURRENT),
                ]
            )
        self.assertEqual(code, 4)

    def test_missing_token_is_a_usage_error(self):
        env = {"GITHUB_TOKEN": "", "LABEL_TOKEN": ""}
        with mock.patch.dict(os.environ, env, clear=False):
            code = mod.main(
                [
                    "--repo",
                    "msgmate-io/open-chat-go",
                    "--pr",
                    "835",
                    "--head-sha",
                    HEAD,
                    "--run-id",
                    str(CURRENT),
                ]
            )
        self.assertEqual(code, 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
