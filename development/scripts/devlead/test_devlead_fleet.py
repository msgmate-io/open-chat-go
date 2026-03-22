#!/usr/bin/env python3
"""Unit tests for the ci#733 development-lead fleet-moderation toolkit.

Run:  python3 -m unittest discover -s development/scripts/devlead -v
"""

from __future__ import annotations

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import devlead_fleet as fleet  # noqa: E402


def view(**overrides):
    base = {
        "chat_uuid": "0000-0000",
        "bot": "msgmate-coder",
        "tags": ["issue:733", "repo:open-chat-go"],
        "state": "active",
        "is_active": True,
    }
    base.update(overrides)
    return base


class TestDeriveMetrics(unittest.TestCase):
    def test_explicit_counts_win(self):
        metrics = fleet.derive_metrics(view(tool_call_count=41, edit_count=0, duplicate_call_count=4))
        self.assertEqual(metrics, {"tool_calls": 41, "edits": 0, "duplicates": 4})

    def test_heuristic_parses_terminal_text(self):
        metrics = fleet.derive_metrics(
            view(last_agent_text="The agent ran 120 tool calls but ended without a final summary.")
        )
        self.assertEqual(metrics["tool_calls"], 120)

    def test_heuristic_counts_edits_in_transcript(self):
        text = "coder_read a\ncoder_edit b\ncoder_write c\ncoder_patch d"
        self.assertEqual(fleet.derive_metrics(view(last_agent_text=text))["edits"], 3)

    def test_missing_values_default_to_zero(self):
        self.assertEqual(fleet.derive_metrics({}), {"tool_calls": 0, "edits": 0, "duplicates": 0})


class TestTicketExtraction(unittest.TestCase):
    def test_issue_tag(self):
        self.assertEqual(fleet.interaction_ticket(view(tags=["issue:733"])), "733")

    def test_ci_shorthand_tag(self):
        self.assertEqual(fleet.interaction_ticket(view(tags=["ci#652"])), "652")

    def test_no_ticket(self):
        self.assertIsNone(fleet.interaction_ticket(view(tags=["repo:open-chat-go"])))


class TestClassify(unittest.TestCase):
    def test_thrash_30_calls_zero_edits(self):
        finding = fleet.classify(view(tool_call_count=31, edit_count=0))
        self.assertEqual(finding["classification"], "thrash")
        self.assertEqual(finding["action"], "stop_and_rescue")
        self.assertTrue(finding["needs_stop"])

    def test_below_call_threshold_is_not_thrash(self):
        finding = fleet.classify(view(tool_call_count=29, edit_count=0))
        self.assertNotEqual(finding["classification"], "thrash")

    def test_edits_disqualify_call_thrash(self):
        finding = fleet.classify(view(tool_call_count=64, edit_count=2))
        self.assertNotEqual(finding["classification"], "thrash")

    def test_three_identical_calls_are_thrash(self):
        finding = fleet.classify(view(tool_call_count=12, edit_count=3, duplicate_call_count=3))
        self.assertEqual(finding["classification"], "thrash")

    def test_idle_thrash_needs_no_stop(self):
        finding = fleet.classify(view(is_active=False, state="idle", tool_call_count=66, edit_count=0))
        self.assertEqual(finding["action"], "stop_and_rescue")
        self.assertFalse(finding["needs_stop"])

    def test_rescue_budget_exhausted(self):
        finding = fleet.classify(view(tool_call_count=66, edit_count=0), rescues=2)
        self.assertEqual(finding["action"], "none")
        self.assertIn("budget exhausted", finding["reason"])

    def test_stall_no_first_turn_continues_once(self):
        finding = fleet.classify(view(state="idle", is_active=False, stall_reason="stall_no_first_turn"))
        self.assertEqual(finding["classification"], "stalled")
        self.assertEqual(finding["action"], "continue")

    def test_repeated_stall_redispatches(self):
        finding = fleet.classify(
            view(state="idle", is_active=False, stalled=True), rescues=1
        )
        self.assertEqual(finding["action"], "redispatch")

    def test_terminal_without_pr_needs_one_continue(self):
        finding = fleet.classify(
            view(state="finished", is_active=False, turn_end_reason="no_usable_tool_call", tool_call_count=22)
        )
        self.assertEqual(finding["classification"], "finished_without_pr")
        self.assertEqual(finding["action"], "continue")

    def test_custom_thresholds(self):
        finding = fleet.classify(view(tool_call_count=5, edit_count=0), {"tool_calls": 5})
        self.assertEqual(finding["classification"], "thrash")

    def test_ready_pr(self):
        pr = {
            "number": 700,
            "state": "OPEN",
            "draft": False,
            "mergeable": True,
            "base": "main",
            "required_check_state": "green",
        }
        finding = fleet.classify(view(state="finished", is_active=False, pull_request=pr))
        self.assertEqual(finding["classification"], "ready_pr")
        self.assertEqual(finding["action"], "ready_for_consolidation")

    def test_ready_pr_behind_updates_branch(self):
        pr = {"number": 700, "state": "OPEN", "mergeable": "MERGEABLE", "base": "main", "behind": True,
              "required_check_state": "success"}
        finding = fleet.classify(view(state="finished", is_active=False, pull_request=pr))
        self.assertEqual(finding["action"], "update_branch")

    def test_draft_pr_after_done_declaration_is_undrafted(self):
        pr = {"number": 700, "state": "OPEN", "draft": True, "mergeable": True, "base": "main"}
        finding = fleet.classify(
            view(state="finished", is_active=False, pull_request=pr, turn_end_reason="completed")
        )
        self.assertEqual(finding["classification"], "pr_hygiene")
        self.assertEqual(finding["action"], "undraft")

    def test_unready_pr_is_not_a_consolidation_candidate(self):
        pr = {"number": 700, "state": "OPEN", "mergeable": True, "base": "main", "required_check_state": "pending"}
        finding = fleet.classify(view(state="finished", is_active=False, pull_request=pr))
        self.assertNotEqual(finding["classification"], "ready_pr")


class TestFleetAudit(unittest.TestCase):
    def test_summary_counts_every_class(self):
        rows = [
            view(chat_uuid="thrash", tool_call_count=41, edit_count=0),
            view(chat_uuid="stalled", state="idle", is_active=False, stall_reason="stall_no_first_turn"),
            view(chat_uuid="no-summary", state="finished", is_active=False, turn_end_reason="turn_timeout",
                 tool_call_count=9),
            view(chat_uuid="ready", state="finished", is_active=False,
                 pull_request={"state": "OPEN", "mergeable": True, "base": "main", "required_check_state": "green"}),
        ]
        result = fleet.audit(rows)
        self.assertEqual(result["summary"]["managed"], 4)
        self.assertEqual(result["summary"]["thrash"], 1)
        self.assertEqual(result["summary"]["stalled"], 1)
        self.assertEqual(result["summary"]["finished_without_pr"], 1)
        self.assertEqual(result["summary"]["ready_pr"], 1)
        # only the live thrash row needs a stop; its rescue includes one stop + one continue
        self.assertEqual(result["summary"]["active"], 1)
        self.assertEqual(result["summary"]["rescue"], 1)


class TestRescue(unittest.TestCase):
    def _finding(self, **kwargs):
        return fleet.classify(view(**kwargs))

    def test_thrash_prompt_is_narrow_and_names_the_ticket(self):
        prompt = fleet.render_rescue_prompt("thrash", self._finding(tool_call_count=66, edit_count=0))
        self.assertIn("ci#733", prompt)
        self.assertIn("10-15", prompt)
        self.assertIn("DRAFT PR", prompt)
        self.assertIn("ZERO edits", prompt)
        self.assertIn("66 calls", prompt)

    def test_thrash_prompt_embeds_concrete_steps(self):
        prompt = fleet.render_rescue_prompt(
            "thrash", self._finding(tool_call_count=66, edit_count=0), steps=["add the audit script"]
        )
        self.assertIn("add the audit script", prompt)

    def test_no_summary_prompt(self):
        prompt = fleet.render_rescue_prompt("no_summary", self._finding())
        self.assertIn("10 calls", prompt)
        self.assertIn("committed", prompt)

    def test_stalled_prompt(self):
        prompt = fleet.render_rescue_prompt("stalled", self._finding(tool_call_count=0, edit_count=0))
        self.assertIn("never started", prompt)

    def test_unknown_template_is_an_error(self):
        with self.assertRaises(ValueError):
            fleet.render_rescue_prompt("nope", self._finding())

    def test_live_turn_is_stopped_before_continue(self):
        seen = []
        finding = self._finding(tool_call_count=66, edit_count=0)
        result = fleet.rescue_actions(
            finding, base="https://ci.msgmate.io", dry_run=False, post=lambda path, payload: seen.append(path)
        )
        self.assertEqual([a["kind"] for a in result["actions"]], ["stop", "continue"])
        self.assertTrue(seen[0].endswith("/stop"))
        self.assertTrue(seen[1].endswith("/continue"))
        self.assertEqual(len(seen), 2)

    def test_idle_turn_is_not_stopped(self):
        seen = []
        finding = self._finding(is_active=False, state="idle", tool_call_count=66, edit_count=0)
        result = fleet.rescue_actions(
            finding, base="https://ci.msgmate.io", dry_run=False, post=lambda path, payload: seen.append(path)
        )
        self.assertEqual([a["kind"] for a in result["actions"]], ["continue"])
        self.assertEqual(len(seen), 1)

    def test_dry_run_sends_nothing(self):
        seen = []
        finding = self._finding(tool_call_count=66, edit_count=0)
        fleet.rescue_actions(
            finding, base="https://ci.msgmate.io", dry_run=True, post=lambda path, payload: seen.append(path)
        )
        self.assertEqual(seen, [])

    def test_rescue_does_not_resume_auto_continue_by_default(self):
        payloads = []

        def capture(path, payload):
            payloads.append((path, payload))

        finding = self._finding(tool_call_count=66, edit_count=0)
        fleet.rescue_actions(
            finding, base="https://ci.msgmate.io", dry_run=False, post=capture,
            resume_auto_continue=False,
        )
        self.assertFalse(payloads[-1][1]["resume_auto_continue"])

    def test_chat_uuid_is_required(self):
        with self.assertRaises(ValueError):
            fleet.rescue_actions({"template": "thrash"}, base="https://ci.msgmate.io")


class TestCapacity(unittest.TestCase):
    def test_full_cap_refuses_dispatch(self):
        decision = fleet.capacity_decision({"effective_cap": 16, "in_flight": 16, "queue": 4})
        self.assertFalse(decision["allowed"])
        self.assertEqual(decision["free"], 0)
        self.assertIn("cap full", decision["reason"])

    def test_free_slot_allows_one_dispatch(self):
        decision = fleet.capacity_decision({"effective_cap": 16, "in_flight": 14, "queue": 0})
        self.assertTrue(decision["allowed"])
        self.assertEqual(decision["free"], 2)

    def test_free_slot_with_queue_reports_residual_queue(self):
        decision = fleet.capacity_decision({"provider_cap": 16, "in_flight": 15, "queued": 2})
        self.assertTrue(decision["allowed"])
        self.assertIn("queue is non-empty", decision["reason"])

    def test_unknown_cap_never_dispatches(self):
        self.assertFalse(fleet.capacity_decision({})["allowed"])

    def test_required_batch_larger_than_free_refused(self):
        self.assertFalse(fleet.capacity_decision({"effective_cap": 16, "in_flight": 15}, required=3)["allowed"])


class TestLabelBudget(unittest.TestCase):
    def test_feature_pr_gets_basic_labels_only(self):
        actions = fleet.label_actions(["e2e", "deploy", "test"])
        self.assertEqual(actions["add"], ["build"])
        self.assertEqual(sorted(actions["remove"]), ["deploy", "e2e"])
        self.assertEqual(sorted(actions["offending"]), ["deploy", "e2e"])

    def test_feature_pr_missing_everything_gets_both_basics(self):
        self.assertEqual(fleet.label_actions([])["add"], ["test", "build"])

    def test_consolidation_pr_gets_the_full_set(self):
        actions = fleet.label_actions(["test"], kind="consolidation")
        self.assertNotIn("test", actions["add"])
        for label in ("build", "e2e", "sandbox-test", "ai-test-mock", "ai-test-real", "deploy"):
            self.assertIn(label, actions["add"])
        self.assertEqual(actions["remove"], [])

    def test_optional_core_label_is_never_added_automatically(self):
        self.assertNotIn("test-core-only", fleet.label_actions([])["add"])


class TestConsolidationPlan(unittest.TestCase):
    @staticmethod
    def _ready(number, branch="feat/x"):
        return {
            "number": number,
            "branch": branch,
            "state": "OPEN",
            "mergeable": True,
            "base": "main",
            "required_check_state": "green",
            "labels": ["test", "build"],
        }

    def test_selects_only_ready_prs(self):
        plan = fleet.plan_consolidation(
            [
                self._ready(1, "feat/a"),
                {"number": 2, "branch": "feat/b", "state": "OPEN", "mergeable": True, "base": "main",
                 "required_check_state": "pending"},
                {"number": 3, "branch": "feat/c", "state": "OPEN", "mergeable": True, "base": "main",
                 "required_check_state": "green", "draft": True},
            ],
            today="2026-01-02",
        )
        self.assertEqual(plan["status"], "ready")
        self.assertEqual(plan["branches"], ["feat/a"])
        self.assertEqual(plan["branch"], "chore/consolidate-2026-01-02-r1")

    def test_batch_number_increments_past_existing_consolidations(self):
        plan = fleet.plan_consolidation(
            [self._ready(9, "chore/consolidate-2026-01-01-r7")], today="2026-01-02"
        )
        self.assertEqual(plan["branch"], "chore/consolidate-2026-01-02-r8")

    def test_never_merges_a_previous_consolidation_branch(self):
        plan = fleet.plan_consolidation(
            [
                {"number": 9, "branch": "chore/consolidate-2026-01-01-r1", "state": "CLOSED", "mergeable": True,
                 "base": "main", "required_check_state": "green"},
                self._ready(10, "feat/a"),
            ],
            today="2026-01-02",
        )
        self.assertEqual(plan["branches"], ["feat/a"])

    def test_one_open_consolidation_defers_the_next_batch(self):
        plan = fleet.plan_consolidation(
            [
                {"number": 9, "branch": "chore/consolidate-2026-01-01-r1", "state": "OPEN", "mergeable": True,
                 "base": "main", "required_check_state": "green"},
                self._ready(10, "feat/a"),
            ],
            today="2026-01-02",
        )
        self.assertEqual(plan["status"], "wait_for_open_consolidation")
        self.assertEqual(plan["open_consolidation"], "chore/consolidate-2026-01-01-r1")
        self.assertEqual(plan["deferred_branches"], ["feat/a"])

    def test_nothing_ready(self):
        plan = fleet.plan_consolidation([], today="2026-01-02")
        self.assertEqual(plan["status"], "nothing_ready")

    def test_plan_always_creates_the_branch_from_main_and_bumps_version(self):
        plan = fleet.plan_consolidation([self._ready(1)], today="2026-01-02")
        commands = " && ".join(plan["commands"])
        self.assertIn("git fetch origin main", commands)
        self.assertIn("git checkout -B chore/consolidate-2026-01-02-r1 origin/main", commands)
        self.assertIn("git merge --no-ff --no-edit origin/feat/x", commands)
        self.assertIn("bump_backend_version.sh", commands)
        self.assertNotIn("--force", commands)

    def test_plan_opens_the_pr_with_the_full_label_set(self):
        plan = fleet.plan_consolidation([self._ready(1)], today="2026-01-02")
        create = [c for c in plan["commands"] if c.startswith("gh pr create")][0]
        for label in fleet.CONSOLIDATION_LABELS:
            self.assertIn("-l " + label, create)
        self.assertIn("--base main", create)

    def test_plan_documents_the_ci683_rest_fallback(self):
        plan = fleet.plan_consolidation([self._ready(1)], today="2026-01-02")
        self.assertIn("ci#683", plan["merge"]["rest_fallback"])
        self.assertIn("Run PR CI Tests and Deployments", plan["merge"]["requires"])


class TestCli(unittest.TestCase):
    def test_every_documented_subcommand_exists(self):
        parser = fleet.build_parser()
        for name in ("audit", "rescue", "capacity", "consolidate", "label-audit"):
            with self.subTest(command=name):
                args = parser.parse_args([name, "--from-file", "/dev/null"])
                self.assertTrue(callable(args.func))

    def test_audit_renders_json_from_a_snapshot(self):
        import io
        import tempfile

        snapshot = {"rows": [view(tool_call_count=41, edit_count=0)]}
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as handle:
            json.dump(snapshot, handle)
            path = handle.name
        out = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False).name
        try:
            rc = fleet.main(["audit", "--from-file", path, "--json-out", out])
            self.assertEqual(rc, 0)
            with open(out, "r", encoding="utf-8") as handle:
                self.assertEqual(json.load(handle)["summary"]["thrash"], 1)
        finally:
            os.unlink(path)
            os.unlink(out)

    def test_capacity_exit_code_is_nonzero_when_the_cap_is_full(self):
        import tempfile

        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as handle:
            json.dump({"effective_cap": 4, "in_flight": 4}, handle)
            path = handle.name
        try:
            self.assertEqual(fleet.main(["capacity", "--from-file", path]), 1)
            self.assertEqual(fleet.main(["capacity", "--from-file", path, "--report-only"]), 0)
        finally:
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
