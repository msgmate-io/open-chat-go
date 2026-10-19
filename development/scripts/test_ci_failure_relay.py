#!/usr/bin/env python3
"""Unit tests for development/scripts/ci_failure_relay.py (ci#734).

Covers the acceptance criteria of the ticket:
  * a failed feature/bugfix PR produces no comment and no interaction;
  * a failed consolidation PR relays exactly one message to the live lead
    interaction and creates no `issue-plan-bot` / `plan_auto_implement` session;
  * the lead message carries repo/PR/run/failed jobs and the never-poll-CI rule;
  * exactly one PR comment names the lead and the run, and it is deduplicated per
    (PR, head SHA) with a bounded attempt counter.
"""
import argparse
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
# The relay module lives next to this test file; keep both that directory and the
# `development/` root importable so the test works from any working directory
# (CI runs `python3 -m unittest development/scripts/test_ci_failure_relay.py`).
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.dirname(HERE))

import ci_failure_relay as relay  # noqa: E402


class FakeGitHub:
    """Records the GitHub side: comment reads, the single comment, the label."""

    def __init__(self, comments=None):
        self.bodies = list(comments or [])
        self.posted = []
        self.labels = []

    def comment_bodies(self, pr_number):
        return list(self.bodies)

    def add_comment(self, pr_number, body):
        self.posted.append((pr_number, body))
        self.bodies.append(body)
        return 201, {}

    def add_label(self, pr_number, label):
        self.labels.append((pr_number, label))
        return 200, {}


class FakeChat:
    """Records the Open-Chat side: list / continue / dispatch."""

    def __init__(self, rows=None):
        self.rows = list(rows or [])
        self.listed = 0
        self.continued = []
        self.dispatched = []

    def list_interactions(self):
        self.listed += 1
        return list(self.rows)

    def continue_interaction(self, chat_uuid, message):
        self.continued.append((chat_uuid, message))
        return 200, {}

    def dispatch(self, body):
        self.dispatched.append(body)
        return 200, {"chat_uuid": "brand-new-lead-chat"}


def event(base_ref="main", pr_branch="chore/consolidate-r40", **over):
    payload = {
        "repo": "msgmate-io/open-chat-go",
        "pr_number": 812,
        "pr_branch": pr_branch,
        "pr_sha": "abcdef1234567890",
        "base_ref": base_ref,
        "run_url": "https://github.com/msgmate-io/open-chat-go/actions/runs/42",
        "failed_jobs": [{"name": "Go Tests", "html_url": "https://example/1"},
                        {"name": "E2E Tests", "html_url": "https://example/2"}],
    }
    payload.update(over)
    return payload


def write_event(payload):
    handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8")
    json.dump(payload, handle)
    handle.close()
    return handle.name


def run(payload, comments=None, rows=None, dry_run=False, env=None):
    gh = FakeGitHub(comments)
    chat = FakeChat(rows)
    args = argparse.Namespace(event=write_event(payload), dry_run=dry_run)
    environment = {
        "GITHUB_REPOSITORY": payload.get("repo", "msgmate-io/open-chat-go"),
        "GITHUB_TOKEN": "test-token",
        "OPEN_CHAT_HOST": "https://ci.example",
        "OPEN_CHAT_API_TOKEN": "test-token",
    }
    environment.update(env or {})
    code = relay.run_relay(args, env=environment, gh_transport=gh, chat_transport=chat)
    return code, gh, chat


LEAD_ROW = {"bot": relay.DEFAULT_LEAD_BOT, "chat_uuid": "lead-chat", "is_active": True}


class ShouldRelayTests(unittest.TestCase):
    def test_consolidation_pr_targeting_main_relays(self):
        self.assertTrue(relay.should_relay("main", "chore/consolidate-r40"))

    def test_feature_pr_does_not_relay(self):
        self.assertFalse(relay.should_relay("main", "fix/ci-relay-quiet"))
        self.assertFalse(relay.should_relay("main", "feat/something"))

    def test_consolidation_pr_off_main_does_not_relay(self):
        self.assertFalse(relay.should_relay("production", "chore/consolidate-r40"))
        self.assertFalse(relay.should_relay("", "chore/consolidate-r40"))

    def test_missing_refs_do_not_relay(self):
        self.assertFalse(relay.should_relay("", ""))
        self.assertFalse(relay.should_relay(None, None))


class PayloadAndMessageTests(unittest.TestCase):
    def test_payload_carries_the_ticket_contract(self):
        payload = relay.build_ci_failure_payload(event(), 2, 3)
        self.assertEqual(payload["kind"], "ci_failure")
        self.assertEqual(payload["schema_version"], 1)
        self.assertEqual(payload["repo"], "msgmate-io/open-chat-go")
        self.assertEqual(payload["pr_number"], 812)
        self.assertEqual(payload["branch"], "chore/consolidate-r40")
        self.assertEqual(payload["sha"], "abcdef1234567890")
        self.assertEqual(payload["failing_jobs"], ["Go Tests", "E2E Tests"])
        self.assertEqual(payload["severity"], "error")
        self.assertRegex(payload["occurred_at"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
        self.assertEqual((payload["attempt"], payload["max_attempts"]), (2, 3))

    def test_message_names_run_jobs_and_forbids_polling(self):
        ev = event()
        payload = relay.build_ci_failure_payload(ev, 1, 3)
        message = relay.build_relay_message(ev, payload)
        self.assertIn(ev["run_url"], message)
        self.assertIn("Go Tests", message)
        self.assertIn("ci_failure", message)
        self.assertIn("Do not poll", message)
        self.assertIn("no fix-plan session was created", message)

    def test_message_never_mentions_a_plan_bot(self):
        ev = event()
        message = relay.build_relay_message(ev, relay.build_ci_failure_payload(ev, 1, 3))
        self.assertNotIn("issue-plan-bot", message)
        self.assertNotIn("plan_auto_implement", message)

    def test_dedupe_key_is_stable_per_pr_and_head_sha(self):
        first = relay.build_dedupe_key(event())
        self.assertEqual(first, relay.build_dedupe_key(event()))
        self.assertEqual(first, "ci-failure:msgmate-io/open-chat-go#812:abcdef123456")
        self.assertNotEqual(first, relay.build_dedupe_key(event(pr_sha="999999999999")))


class CommentBodyTests(unittest.TestCase):
    def test_unresolved_target_reads_as_resolved_at_runtime(self):
        """A dry-run resolves no interaction, so the body must not print a bare '?'."""
        body = relay.build_comment_body(event(), 1, 3, relay.DEFAULT_LEAD_BOT, None, False)
        self.assertIn("resolved at runtime", body)
        self.assertNotIn("interaction `?`", body)

    def test_resolved_target_names_the_interaction(self):
        body = relay.build_comment_body(event(), 1, 3, relay.DEFAULT_LEAD_BOT,
                                        {"chat_uuid": "lead-chat"}, False)
        self.assertIn("interaction `lead-chat`", body)
        self.assertNotIn("resolved at runtime", body)


class MarkerAndAttemptTests(unittest.TestCase):
    def test_marker_round_trips_attempt_and_sha(self):
        marker = relay.build_relay_marker(event(), 2)
        self.assertIn(relay.RELAY_MARKER_NAME, marker)
        self.assertIn("attempt=2", marker)
        self.assertEqual(relay.parse_relay_markers([marker])[0]["sha"], "abcdef1234567890")

    def test_attempts_are_counted_from_the_marker(self):
        comments = [relay.build_relay_marker(event(), 1), relay.build_relay_marker(event(), 2)]
        self.assertEqual(relay.count_attempts(comments), 2)
        self.assertEqual(relay.next_attempt(comments), 3)

    def test_legacy_fix_plan_markers_still_count_as_attempts(self):
        comments = [relay.build_relay_marker(event(), 1),
                    "<!-- open-chat-ci-failure-plan -->\n:x: failed (fix attempt 2/3)"]
        self.assertEqual(relay.count_attempts(comments), 2)
        self.assertEqual(relay.next_attempt(comments), 3)

    def test_attempts_are_zero_without_comments(self):
        self.assertEqual(relay.count_attempts([]), 0)
        self.assertEqual(relay.next_attempt(["unrelated comment"]), 1)

    def test_already_relayed_only_matches_the_same_head_sha(self):
        comments = [relay.build_relay_marker(event(), 1)]
        self.assertTrue(relay.already_relayed(comments, "abcdef1234567890"))
        self.assertFalse(relay.already_relayed(comments, "deadbeefdeadbeef"))


class LeadResolutionTests(unittest.TestCase):
    def test_configured_uuid_wins(self):
        chosen = relay.pick_lead_interaction([LEAD_ROW], chat_uuid="configured-chat")
        self.assertEqual(chosen["chat_uuid"], "configured-chat")
        self.assertEqual(chosen["resolved_by"], "configured")

    def test_active_interaction_is_preferred(self):
        rows = [{"bot": relay.DEFAULT_LEAD_BOT, "chat_uuid": "past", "is_active": False},
                {"bot": relay.DEFAULT_LEAD_BOT, "chat_uuid": "live", "is_active": True}]
        self.assertEqual(relay.pick_lead_interaction(rows)["chat_uuid"], "live")

    def test_most_recent_is_used_when_none_is_active(self):
        rows = [{"bot": relay.DEFAULT_LEAD_BOT, "chat_uuid": "newest", "is_active": False},
                {"bot": relay.DEFAULT_LEAD_BOT, "chat_uuid": "older", "is_active": False}]
        chosen = relay.pick_lead_interaction(rows)
        self.assertEqual(chosen["chat_uuid"], "newest")
        self.assertEqual(chosen["resolved_by"], "recent")

    def test_other_bots_are_ignored(self):
        rows = [{"bot": "issue-plan-bot", "chat_uuid": "planner", "is_active": True}]
        self.assertIsNone(relay.pick_lead_interaction(rows))


class FeaturePRTests(unittest.TestCase):
    def test_feature_pr_gets_no_relay_and_no_comment(self):
        for branch in ("fix/ci-relay-quiet", "feat/thing", "chore/other"):
            code, gh, chat = run(event(pr_branch=branch))
            self.assertEqual(code, 0)
            self.assertEqual(gh.posted, [], branch)
            self.assertEqual(chat.continued, [], branch)
            self.assertEqual(chat.dispatched, [], branch)
            self.assertEqual(chat.listed, 0, branch)

    def test_non_main_base_gets_nothing(self):
        code, gh, chat = run(event(base_ref="production"))
        self.assertEqual(code, 0)
        self.assertEqual((gh.posted, chat.continued, chat.dispatched), ([], [], []))


class ConsolidatedPRTests(unittest.TestCase):
    def test_relays_exactly_one_message_to_the_live_lead(self):
        code, gh, chat = run(event(), rows=[LEAD_ROW])
        self.assertEqual(code, 0)
        self.assertEqual(chat.listed, 1)
        self.assertEqual(len(chat.continued), 1)
        self.assertEqual(chat.continued[0][0], "lead-chat")
        self.assertEqual(chat.dispatched, [])
        self.assertIn("Go Tests", chat.continued[0][1])

    def test_posts_exactly_one_comment_naming_lead_and_run(self):
        _, gh, _ = run(event(), rows=[LEAD_ROW])
        self.assertEqual(len(gh.posted), 1)
        pr_number, body = gh.posted[0]
        self.assertEqual(pr_number, 812)
        self.assertIn(relay.DEFAULT_LEAD_BOT, body)
        self.assertIn(event()["run_url"], body)
        self.assertIn("Go Tests", body)
        self.assertIn(relay.RELAY_MARKER_NAME, body)
        self.assertNotIn(relay.LEGACY_MARKER_NAME, body)

    def test_no_plan_session_or_badge_is_created(self):
        _, gh, chat = run(event(), rows=[LEAD_ROW])
        everything = "\n".join([body for _, body in gh.posted]
                               + [message for _, message in chat.continued]
                               + [json.dumps(body, sort_keys=True) for body in chat.dispatched])
        self.assertNotIn("issue-plan-bot", everything)
        self.assertNotIn("plan_auto_implement", everything)
        self.assertNotIn("badge.svg", everything)

    def test_dispatch_is_used_when_no_live_lead_exists(self):
        _, gh, chat = run(event(), rows=[])
        self.assertEqual(chat.continued, [])
        self.assertEqual(len(chat.dispatched), 1)
        body = chat.dispatched[0]
        self.assertEqual(body["bot"], relay.DEFAULT_LEAD_BOT)
        self.assertEqual(body["dedupe_key"], relay.build_dedupe_key(event()))
        self.assertIn("ci-failure", body["tags"])
        self.assertIn("Go Tests", body["message"])
        self.assertEqual(len(gh.posted), 1)
        self.assertIn("new interaction dispatched", gh.posted[0][1])

    def test_dispatch_is_not_used_when_other_bots_are_running(self):
        _, _, chat = run(event(), rows=[{"bot": "issue-plan-bot", "chat_uuid": "planner",
                                        "is_active": True}])
        self.assertEqual(len(chat.dispatched), 1)

    def test_deduplicates_per_head_sha(self):
        comments = [relay.build_relay_marker(event(), 1)]
        code, gh, chat = run(event(), comments=comments, rows=[LEAD_ROW])
        self.assertEqual(code, 0)
        self.assertEqual(gh.posted, [])
        self.assertEqual((chat.continued, chat.dispatched), ([], []))

    def test_relays_again_for_a_new_head_sha(self):
        comments = [relay.build_relay_marker(event(pr_sha="old-sha"), 1)]
        _, gh, chat = run(event(), comments=comments, rows=[LEAD_ROW])
        self.assertEqual(len(gh.posted), 1)
        self.assertEqual(len(chat.continued), 1)
        self.assertIn("attempt 2/3", gh.posted[0][1])

    def test_attempt_cap_posts_a_blocked_comment_and_label(self):
        comments = [relay.build_relay_marker(event(pr_sha="sha%d" % attempt), attempt)
                    for attempt in (1, 2, 3)]
        code, gh, chat = run(event(), comments=comments, rows=[LEAD_ROW])
        self.assertEqual(code, 0)
        self.assertEqual(chat.continued, [])
        self.assertEqual(chat.dispatched, [])
        self.assertEqual(len(gh.posted), 1)
        self.assertIn("limit reached", gh.posted[0][1])
        self.assertEqual(gh.labels, [(812, "ci-fix-blocked")])

    def test_delivery_failure_is_reported_in_the_comment(self):
        chat = FakeChat(rows=[LEAD_ROW])
        chat.continue_interaction = _boom
        gh = FakeGitHub()
        args = argparse.Namespace(event=write_event(event()), dry_run=False)
        code = relay.run_relay(args, env={"GITHUB_REPOSITORY": event()["repo"],
                                          "GITHUB_TOKEN": "t"},
                               gh_transport=gh, chat_transport=chat)
        self.assertEqual(code, 1)
        self.assertEqual(len(gh.posted), 1)
        self.assertIn("maintainer action required", gh.posted[0][1])

    def test_dry_run_posts_nothing(self):
        code, gh, chat = run(event(), rows=[LEAD_ROW], dry_run=True)
        self.assertEqual(code, 0)
        self.assertEqual(gh.posted, [])
        self.assertEqual(chat.continued, [])


def _boom(_chat_uuid, _message):
    raise RuntimeError("continue -> HTTP 503 busy")


if __name__ == "__main__":
    unittest.main(verbosity=2)
