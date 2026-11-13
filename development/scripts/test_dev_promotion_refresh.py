#!/usr/bin/env python3
"""Regression guard for ci#1067 - the `[Dev promotion]` PR body must not go stale.

`.github/workflows/run-pr-ci-tests-and-deployments.yaml` ->
`Open or refresh dev to main promotion PR` refreshed an existing promotion PR
with a title-only PATCH:

    -d "$(jq -n --arg title "$title" '{title: $title}')"

so `open-chat-go#36` kept advertising an older promotion: it still named the
previous dev head `858392ab` and listed `#32` as the consolidated ticket long
after `dev` had moved to `4a0d27ec` (r83). The release PR is what a reviewer
reads to decide a merge into `main`, so it was stale by construction.

The refresh now re-renders title **and** body from live API data on every run:

* `head_sha` from `GET /repos/{repo}/git/ref/heads/dev`, falling back to the
  newest `.commits[].sha` of the already-fetched `compare/main...dev`;
* title `[Dev promotion] Promote dev -> main - dev @ <sha7> (ci#833)`;
* body: the summary table, a `### Consolidated tickets` section built from
  `ci#[0-9]+` occurrences in the promoted commit messages (up to 20 links,
  overflow summarised) and the newest 10 commits;
* an unresolvable head warns (`::warning::`) and does a title-only PATCH instead
  of publishing an empty/stale body;
* the ci#1077 auto-merge arming on the refresh path is untouched.

This guard extracts the step's shell **verbatim** from the workflow and runs it
against stubbed `curl`/`gh` in a temp `bin/` on `PATH`, in the style of
`test_auto_approve_flaky_pulls.py`. No network, no token, no user data.
Deliberately stdlib-only: PR CI runs it with the system `python3`.
"""

from __future__ import annotations

import json
import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = REPO_ROOT / ".github" / "workflows" / "run-pr-ci-tests-and-deployments.yaml"

STEP_NAME = "Open or refresh dev to main promotion PR"

REPOSITORY = "msgmate-io/open-chat-go"
SERVER_URL = "https://github.com"
FAKE_TOKEN = "ghs_fakePromotionTokenDoNotLeak0123456789"
LIVE_DEV_HEAD = "4a0d27ec0f1e2d3c4b5a69788796a5b4c3d2e1f0"

# The values the stale PR body still advertised (ci#1067).
STALE_SHA = "858392ab"
STALE_TICKET = "#32"


def _step_shell() -> str:
    """Extract the step's shell, dedented, verbatim."""
    lines = WORKFLOW.read_text(encoding="utf-8").splitlines()
    try:
        start = next(
            idx for idx, line in enumerate(lines) if line.strip() == f"- name: {STEP_NAME}"
        )
    except StopIteration:  # pragma: no cover - a rename is the failure
        raise AssertionError(
            f"step {STEP_NAME!r} not found in {WORKFLOW.name}: was it renamed "
            "without updating this guard?"
        ) from None

    run_at = None
    for idx in range(start + 1, len(lines)):
        if lines[idx].strip() == "run: |":
            run_at = idx
            break
    assert run_at is not None, f"step {STEP_NAME!r} has no `run: |` block"

    body: list[str] = []
    indent = None
    for line in lines[run_at + 1 :]:
        if not line.strip():
            body.append("")
            continue
        current = len(line) - len(line.lstrip())
        if indent is None:
            indent = current
        if current < indent:
            break
        body.append(line[indent:])
    while body and not body[-1].strip():
        body.pop()
    assert body, "the step's `run:` block is empty"
    return "\n".join(body)


CURL_STUB = """#!/usr/bin/env python3
import json, os, sys

args = sys.argv[1:]
method = "GET"
url = ""
data = ""
idx = 0
while idx < len(args):
    arg = args[idx]
    if arg == "-X" and idx + 1 < len(args):
        method = args[idx + 1]
        idx += 2
        continue
    if arg in ("-d", "--data", "--data-raw") and idx + 1 < len(args):
        data = args[idx + 1]
        idx += 2
        continue
    if arg in ("-H", "--header") and idx + 1 < len(args):
        idx += 2
        continue
    if arg in ("-u", "-o", "--output", "--retry", "--max-time") and idx + 1 < len(args):
        idx += 2
        continue
    if arg.startswith("-"):
        idx += 1
        continue
    url = arg
    idx += 1

with open(os.environ["CURL_LOG"], "a", encoding="utf-8") as handle:
    handle.write(json.dumps({"method": method, "url": url, "data": data}) + "\\n")
# The token must never be rendered anywhere by the caller; the stub only
# verifies it was sent as a header when the step shells out.
with open(os.environ["CURL_CONFIG"], encoding="utf-8") as handle:
    cfg = json.load(handle)

def respond(text):
    sys.stdout.write(text if text.endswith("\\n") else text + "\\n")

routes = cfg.get("routes", {})
for key, value in routes.items():
    if key in url and method == value.get("method", method):
        if value.get("status", 200) != 200:
            sys.stderr.write("curl: (22) The requested URL returned error: %d\\n" % value["status"])
            sys.exit(22)
        respond(value.get("body", ""))
        sys.exit(0)

if "/git/ref/heads/dev" in url:
    sys.stderr.write("curl: (22) The requested URL returned error: 404\\n")
    sys.exit(22)
respond("{}")
sys.exit(0)
"""

GH_STUB = """#!/usr/bin/env python3
import json, os, sys

args = sys.argv[1:]
with open(os.environ["GH_LOG"], "a", encoding="utf-8") as handle:
    handle.write(" ".join(args) + "\\n")
with open(os.environ["GH_CONFIG"], encoding="utf-8") as handle:
    cfg = json.load(handle)
if len(args) > 1 and args[0] == "pr" and args[1] == "merge":
    sys.exit(int(cfg.get("auto_merge_rc", 0)))
sys.exit(0)
"""

COMPARE = {
    "ahead_by": 3,
    "commits": [
        {
            "sha": "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "commit": {"message": "feat: land the thing (ci#1067)\n\nbody"},
        },
        {
            "sha": "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            "commit": {"message": "fix: another thing (ci#931)"},
        },
        {
            "sha": LIVE_DEV_HEAD,
            "commit": {"message": "chore: merge consolidation | escaped pipe (ci#1066)"},
        },
    ],
}


class PromotionRun:
    """One execution of the shipped promotion step against stubbed curl/gh."""

    def __init__(
        self,
        *,
        head_body: str | None = None,
        head_status: int = 200,
        compare: dict | None = None,
        existing_pr: list | None = None,
        auto_merge_rc: int = 0,
    ):
        self.head_body = head_body
        self.head_status = head_status
        self.compare = compare if compare is not None else COMPARE
        self.existing_pr = existing_pr if existing_pr is not None else [{"number": 36}]
        self.auto_merge_rc = auto_merge_rc

    def run(self):
        work = tempfile.mkdtemp(prefix="ci1067-promotion-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)

        for name, content in (("curl", CURL_STUB), ("gh", GH_STUB)):
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)

        routes = {
            "compare/main...dev": {"method": "GET", "body": json.dumps(self.compare)},
            "pulls?state=open": {"method": "GET", "body": json.dumps(self.existing_pr)},
            # Creating a promotion PR returns the new number the step labels.
            "/pulls": {"method": "POST", "body": json.dumps({"number": 37})},
        }
        if self.head_status == 200 and self.head_body is not None:
            routes["/git/ref/heads/dev"] = {"method": "GET", "body": self.head_body}

        with open(os.path.join(work, "curl_config.json"), "w", encoding="utf-8") as handle:
            json.dump({"routes": routes}, handle)
        with open(os.path.join(work, "gh_config.json"), "w", encoding="utf-8") as handle:
            json.dump({"auto_merge_rc": self.auto_merge_rc}, handle)

        curl_log = os.path.join(work, "curl.log")
        gh_log = os.path.join(work, "gh.log")
        for path in (curl_log, gh_log):
            open(path, "w", encoding="utf-8").close()

        script_path = os.path.join(work, "promotion.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write(_step_shell())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "CURL_LOG": curl_log,
                "CURL_CONFIG": os.path.join(work, "curl_config.json"),
                "GH_LOG": gh_log,
                "GH_CONFIG": os.path.join(work, "gh_config.json"),
                "PR_TOKEN": FAKE_TOKEN,
                "REPOSITORY": REPOSITORY,
                "REPO_OWNER": REPOSITORY.split("/")[0],
                "GITHUB_SERVER_URL": SERVER_URL,
                "GITHUB_API_URL": "https://api.github.com",
                "MERGED_PR_NUMBER": "1077",
                "MERGED_PR_TITLE": "chore: consolidate whatever (ci#1077)",
                "MERGED_PR_URL": f"{SERVER_URL}/{REPOSITORY}/pull/1077",
                "DEV_HEAD_SHA": "9999999abcdefabcdefabcdefabcdefabcdefab",
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )

        with open(curl_log, encoding="utf-8") as handle:
            requests = [json.loads(line) for line in handle.read().splitlines() if line]
        with open(gh_log, encoding="utf-8") as handle:
            gh_calls = [line for line in handle.read().splitlines() if line]
        return result.returncode, result.stdout + result.stderr, requests, gh_calls


def _patch_requests(requests):
    return [r for r in requests if r["method"] == "PATCH"]


def _create_requests(requests):
    return [r for r in requests if r["method"] == "POST" and r["url"].endswith("/pulls")]


class DevPromotionRefreshTest(unittest.TestCase):
    def test_the_step_shell_is_valid(self) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(_step_shell())
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_the_refresh_renders_title_and_body(self) -> None:
        script = _step_shell()
        self.assertIn("ci#1067", script)
        self.assertIn("git/ref/heads/dev", script)
        self.assertIn("### Consolidated tickets", script)
        self.assertIn("promotion_patch_payload", script)
        # The title-only PATCH that caused the stale release PR must be gone.
        self.assertNotIn(
            """'{title: $title}'""",
            script,
            "the refresh path must not PATCH a title-only payload (ci#1067)",
        )

    def test_the_refresh_patch_carries_a_live_body(self) -> None:
        rc, out, requests, gh_calls = PromotionRun(
            head_body=json.dumps({"object": {"sha": LIVE_DEV_HEAD}})
        ).run()
        self.assertEqual(rc, 0, out)
        patches = _patch_requests(requests)
        self.assertEqual(len(patches), 1, requests)
        payload = json.loads(patches[0]["data"])
        self.assertIn("body", payload, "the refresh must PATCH a body (ci#1067)")
        self.assertEqual(payload["title"], "[Dev promotion] Promote dev -> main - dev @ 4a0d27e (ci#833)")
        body = payload["body"]
        self.assertIn(LIVE_DEV_HEAD, body)
        self.assertIn("### Consolidated tickets", body)
        self.assertIn(f"{SERVER_URL}/msgmate-io/open-chat-go-ci/issues/1067", body)
        self.assertIn(f"{SERVER_URL}/msgmate-io/open-chat-go-ci/issues/931", body)
        self.assertIn(f"{SERVER_URL}/msgmate-io/open-chat-go-ci/issues/1066", body)
        self.assertIn("### Newest commits on", body)
        self.assertIn("chore: merge consolidation | escaped pipe".replace(" | ", " / "), body)
        # The stale promotion must not survive the refresh.
        self.assertNotIn(STALE_SHA, body)
        self.assertNotIn(STALE_TICKET, body)
        # ci#1077 auto-merge arming is untouched.
        self.assertTrue(
            any("--auto" in call for call in gh_calls), gh_calls
        )
        self.assertNotIn(FAKE_TOKEN, out)

    def test_unresolvable_head_warns_and_never_patches_an_empty_body(self) -> None:
        rc, out, requests, _ = PromotionRun(head_status=404, compare={"ahead_by": 3, "commits": []}).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("::warning::", out)
        patches = _patch_requests(requests)
        self.assertEqual(len(patches), 1, requests)
        payload = json.loads(patches[0]["data"])
        self.assertNotIn("body", payload, "an empty/stale body must never be published")
        self.assertIn("title", payload)

    def test_missing_ref_falls_back_to_the_compare_head(self) -> None:
        rc, out, requests, _ = PromotionRun(head_status=404).run()
        self.assertEqual(rc, 0, out)
        payload = json.loads(_patch_requests(requests)[0]["data"])
        body = payload["body"]
        self.assertIn(LIVE_DEV_HEAD, body)
        self.assertIn("@ 4a0d27e (ci#833)", payload["title"])
        self.assertNotIn("unknown", payload["title"])
        self.assertNotIn("unknown", body)
        self.assertNotIn("::warning::", out)

    def test_dev_not_ahead_opens_no_pr_at_all(self) -> None:
        rc, out, requests, gh_calls = PromotionRun(compare={"ahead_by": 0, "commits": []}).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("dev is not ahead of main", out)
        self.assertEqual(_patch_requests(requests), [])
        self.assertEqual(_create_requests(requests), [])
        self.assertEqual(gh_calls, [])

    def test_a_new_promotion_pr_still_gets_title_and_body(self) -> None:
        rc, out, requests, _ = PromotionRun(
            head_body=json.dumps({"object": {"sha": LIVE_DEV_HEAD}}), existing_pr=[]
        ).run()
        self.assertEqual(rc, 0, out)
        creates = _create_requests(requests)
        self.assertEqual(len(creates), 1, requests)
        payload = json.loads(creates[0]["data"])
        self.assertEqual(payload["head"], "dev")
        self.assertEqual(payload["base"], "main")
        self.assertIn(LIVE_DEV_HEAD, payload["body"])
        self.assertNotIn(STALE_SHA, payload["body"])

    def test_the_token_is_never_rendered(self) -> None:
        rc, out, requests, _ = PromotionRun(
            head_body=json.dumps({"object": {"sha": LIVE_DEV_HEAD}})
        ).run()
        self.assertEqual(rc, 0, out)
        self.assertNotIn(FAKE_TOKEN, out)
        for request in requests:
            self.assertNotIn(FAKE_TOKEN, request["data"])


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
