#!/usr/bin/env python3
"""Unit tests for development/scripts/pr_ci_superseded_reconcile.py (ci#659).

Locks the acceptance criteria of the superseded-PR-CI-run gate fix: a cancelled
gate is accepted only when the commit no longer needs gating (PR closed, or a
newer commit is the live PR head) or when a *replacement* run for the exact same
commit reached the gate job with a real verdict, whose conclusion is adopted.
Everything else fails closed.
"""
import argparse
import io
import os
import sys
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

import pr_ci_superseded_reconcile as mod  # noqa: E402

GATE = mod.GATE_JOB_NAME
WORKFLOW_FILE = mod.WORKFLOW_FILE
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


class PaginationTests(unittest.TestCase):
    """Listings larger than one API page must not hide the gate job (ci#659).

    `/actions/runs/<id>/jobs` is offset-paginated at 100 items and a run of this
    workflow is close to that once the deployment jobs are counted. A single-page
    read could miss `PR Test Merge Requirement`, treat the run as never having
    gated the commit and fall back to an older run for the same SHA.
    """

    def test_paged_path_appends_query_parameters(self):
        self.assertEqual(
            mod.paged_path("/actions/runs/1/jobs", 2),
            "/actions/runs/1/jobs?per_page=100&page=2",
        )
        self.assertEqual(
            mod.paged_path("/actions/workflows/w.yaml/runs?head_sha=abc", 3),
            "/actions/workflows/w.yaml/runs?head_sha=abc&per_page=100&page=3",
        )

    def test_gate_job_on_a_later_page_is_found(self):
        filler = [{"name": "job-%d" % i, "conclusion": "success"} for i in range(mod.PAGE_SIZE)]
        pages = [{"jobs": filler}, {"jobs": filler + [gate()]}]
        urls = []

        def fetch(url, token):
            urls.append(url)
            return pages[len(urls) - 1]

        mod._FETCH = fetch
        try:
            conclusion = mod.gate_conclusion("msgmate-io/open-chat-go", NEWER, "token")
        finally:
            mod._FETCH = mod.fetch_json

        self.assertEqual(conclusion, "success")
        self.assertEqual(len(urls), 2)
        self.assertIn("page=2", urls[1])

    def test_pagination_stops_at_the_first_short_page(self):
        pages = [100, 1]
        urls = []

        def fetch(url, token):
            urls.append(url)
            return {"jobs": [gate() for _ in range(pages[len(urls) - 1])]}

        mod._FETCH = fetch
        try:
            items = list(mod.api_pages("r", "/actions/runs/1/jobs", "token"))
        finally:
            mod._FETCH = mod.fetch_json

        self.assertEqual(len(items), 101)
        self.assertEqual(len(urls), 2)

    def test_page_budget_bounds_a_listing_that_never_ends(self):
        urls = []

        def fetch(url, token):
            urls.append(url)
            return {"jobs": [gate() for _ in range(mod.PAGE_SIZE)]}

        mod._FETCH = fetch
        try:
            items = list(mod.api_pages("r", "/actions/runs/1/jobs", "token", limit=3))
        finally:
            mod._FETCH = mod.fetch_json

        self.assertEqual(len(items), 3 * mod.PAGE_SIZE)
        self.assertEqual(len(urls), 3)

    def test_reconcile_reads_a_gate_job_beyond_the_first_page(self):
        filler = [{"name": "job-%d" % i} for i in range(mod.PAGE_SIZE)]

        class PagedApi(FakeApi):
            def __init__(self, **kwargs):
                super().__init__(**kwargs)

            def __call__(self, url, token):
                if "/jobs" in url and "page=2" not in url:
                    return {"jobs": filler}
                if "/jobs" in url:
                    return {"jobs": [gate()]}
                return super().__call__(url, token)

        code, summary = self.reconcile(PagedApi(runs=[run(CURRENT), run(NEWER)]))
        self.assertEqual(code, 0)
        self.assertIn("replacement run %s gate job concluded success" % NEWER, summary)

    def reconcile(self, api, **over):
        mod._FETCH = api
        try:
            return mod.reconcile(ns(**over), "token")
        finally:
            mod._FETCH = mod.fetch_json


class WorkflowWiringTests(unittest.TestCase):
    """The gate job must actually be able to run the reconciler (ci#659).

    The reconciler was first wired into `pr-test-merge-requirement` while that
    job had no checkout. The step died with "can't open file", and because it is
    `continue-on-error: true` it reported `accepted=false` with no annotation, so
    the ci#659 fix silently never engaged (run 39261844509 on PR #844). Pin the
    wiring: a checkout must precede the script call, the job must keep needing
    every suite it reconciles, and a missing script must stay a loud fail-closed.
    """

    WORKFLOW = os.path.abspath(
        os.path.join(os.path.dirname(HERE), "..", ".github", "workflows", WORKFLOW_FILE)
    )
    GATE_JOB = "  pr-test-merge-requirement:"
    NEXT_JOB = "  full-suite-gate:"

    @classmethod
    def setUpClass(cls):
        with io.open(cls.WORKFLOW, encoding="utf-8") as handle:
            workflow = handle.read()
        start = workflow.index(cls.GATE_JOB)
        cls.gate = workflow[start : workflow.index(cls.NEXT_JOB, start)]

    def test_gate_job_checks_out_the_repository_before_running_the_reconciler(self):
        checkout = self.gate.index("uses: actions/checkout@v4")
        reconciler = self.gate.index("development/scripts/pr_ci_superseded_reconcile.py")
        self.assertLess(
            checkout,
            reconciler,
            "the reconciler runs from the workspace, so a checkout must come first",
        )

    def test_checkout_never_hands_the_bot_token_to_fork_code(self):
        """The checkout runs PR-head code, so only same-repo PRs get BOT_TOKEN.

        The reconciler is executed from the checked-out workspace, i.e. the code
        of the commit under test. A fork PR must therefore never receive the
        privileged token: it falls back to the read-only `github.token`.
        """
        step = self.gate[
            self.gate.index("Checkout source repository") : self.gate.index(
                "- name: Resolve the pull request's live labels"
            )
        ]
        self.assertIn("github.event.pull_request.head.repo.full_name == github.repository", step)
        self.assertNotIn("token: ${{ secrets.BOT_TOKEN || github.token }}", step)
        self.assertIn("persist-credentials: false", step)

    def test_reconciler_exists_where_the_workflow_expects_it(self):
        self.assertTrue(
            os.path.isfile(os.path.join(HERE, "pr_ci_superseded_reconcile.py"))
        )

    def test_missing_reconciler_fails_closed_with_an_annotation(self):
        marker = self.gate.index("[ ! -f development/scripts/pr_ci_superseded_reconcile.py ]")
        block = self.gate[marker : marker + 400]
        self.assertIn("::error::", block)
        self.assertIn('echo "accepted=false"', block)

    def test_gate_job_still_needs_every_suite_it_reconciles(self):
        needs = self.gate[: self.gate.index("steps:")]
        for suite in (
            "run-pr-ci-go-tests",
            "run-pr-ci-ai-mock-tests",
            "run-pr-ci-e2e-tests",
            "run-pr-ci-sandbox-tests-after-deploy",
        ):
            self.assertIn(suite, needs)

    def test_reconcile_step_only_runs_for_a_cancelled_suite(self):
        self.assertIn("needs.run-pr-ci-go-tests.result == 'cancelled'", self.gate)

    def test_reconcile_step_cannot_fail_the_job_but_the_gate_stays_red(self):
        step = self.gate[
            self.gate.index("id: superseded-reconcile") : self.gate.index(
                "- name: Enforce test label and passing tests"
            )
        ]
        self.assertIn("continue-on-error: true", step)
        self.assertIn("accepted=false", step)


if __name__ == "__main__":
    unittest.main(verbosity=2)
