#!/usr/bin/env python3
"""Regression guard for ci#1066 - the promotion PR refresh must not go stale.

`.github/workflows/run-pr-ci-tests-and-deployments.yaml` ->
`Open or refresh dev to main promotion PR` renders the `[Dev promotion]` PR
title and body. Before ci#1066 the refresh path PATCHed **only the title**, so
PR #36 kept the body of an older promotion: it still advertised the previous
dev head sha (`858392ab`) and listed `#32` as the consolidated ticket long
after both had moved on. Anyone reading the release PR saw a stale promotion
description - including the reviewer deciding whether to merge it into `main`.

The fix re-renders title *and* body from live API data on every run, so a
stale head sha / stale ticket list cannot survive a refresh. This guard runs
the step's shell verbatim against a stubbed `curl` (the step talks to the REST
API directly, not through `gh`) and asserts on the captured PATCH payload:

* the body contains the *current* dev head sha and the real `ci#` tickets,
* the body does NOT contain the stale sha/ticket from the historical PR body,
* with an unresolvable head sha the refresh degrades to a title-only PATCH
  plus a `::warning::` instead of publishing an empty "unknown" body.

Stdlib only, mirroring the sibling guards.
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
RUN_INDENT = 10
REPOSITORY = "example/example"
EXISTING_PR = 36

# What the historical PR #36 body still said (the ci#1066 bug report).
STALE_SHA = "858392ab"
STALE_TICKET = "#32"

LIVE_DEV_SHA = "4a0d27ec9f1b2c3d4e5f60718293a4b5c6d7e8f9"
LIVE_DEV_SHA_SHORT = LIVE_DEV_SHA[:7]

COMPARE_JSON = {
    "ahead_by": 3,
    "total_commits": 3,
    "commits": [
        {
            "sha": LIVE_DEV_SHA,
            "commit": {
                "message": "feat(ci): guard the promotion refresh (ci#1066)\n\nbody line"
            },
        },
        {
            "sha": "1111111111111111111111111111111111111111",
            "commit": {"message": "fix(auto-approve): stay green (ci#1050)\n\nbody line"},
        },
        {
            "sha": "2222222222222222222222222222222222222222",
            "commit": {"message": "chore: bump deps"},
        },
    ],
}

HEAD_REF_JSON = {"ref": "refs/heads/dev", "object": {"sha": LIVE_DEV_SHA}}

OPEN_PULLS_JSON = [{"number": EXISTING_PR}]

CURL_STUB = r"""#!/usr/bin/env bash
set -uo pipefail
method="GET"
url=""
payload=""
while [ $# -gt 0 ]; do
  case "$1" in
    -X) method="$2"; shift 2 ;;
    -H) shift 2 ;;
    -d) payload="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
printf '%s\t%s\n' "$method" "$url" >> "$CURL_LOG"
if [ -n "$payload" ]; then
  # Compact the JSON so a multi-line body stays on one log line.
  printf '%s\t%s\t%s\n' "$method" "$url" "$(printf '%s' "$payload" | jq -c .)" >> "$CURL_PAYLOAD_LOG"
fi
case "$url" in
  *"/compare/main...dev")
    [ "${CURL_COMPARE_MODE:-ok}" = "ok" ] || exit 22
    cat "$CURL_COMPARE_FILE"
    ;;
  *"/git/ref/heads/dev")
    [ "${CURL_HEADREF_MODE:-ok}" = "ok" ] || exit 22
    cat "$CURL_HEADREF_FILE"
    ;;
  *"/pulls?state=open"*)
    printf '%s' "$CURL_OPEN_PULLS"
    ;;
  *"/issues/"*"/labels")
    echo '[]'
    ;;
  *)
    echo '{}'
    ;;
esac
exit 0
"""

GH_STUB = """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$GH_LOG"
exit "${GH_MERGE_AUTO_RC:-0}"
"""


def _step_script() -> str:
    """Extract the promotion step's shell, dedented, verbatim."""
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

    body: list[str] = []
    in_run = False
    for line in lines[start + 1 :]:
        if not in_run:
            if line.strip() == "run: |":
                in_run = True
            continue
        if line.strip() and len(line) - len(line.lstrip()) < RUN_INDENT:
            break
        body.append(line[RUN_INDENT:] if line.strip() else "")

    assert body, "the step's `run:` block is empty"
    return "\n".join(body)


class PromoRun:
    """One execution of the shipped promotion step against stubbed tools."""

    def __init__(
        self,
        compare: dict | None = COMPARE_JSON,
        head_ref: dict | None = HEAD_REF_JSON,
        open_pulls: list[dict] | None = None,
        merged_pr_number: int = 44,
    ):
        self.compare = compare
        self.head_ref = head_ref
        self.open_pulls = open_pulls if open_pulls is not None else OPEN_PULLS_JSON
        self.merged_pr_number = merged_pr_number

    def run(self) -> tuple[int, str, list[tuple[str, str, str]], list[str]]:
        work = tempfile.mkdtemp(prefix="ci1066-promo-")
        bindir = os.path.join(work, "bin")
        os.makedirs(bindir)

        def write(name: str, content: str) -> None:
            path = os.path.join(bindir, name)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(content)
            os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)

        write("curl", CURL_STUB)
        write("gh", GH_STUB)

        compare_path = os.path.join(work, "compare.json")
        with open(compare_path, "w", encoding="utf-8") as handle:
            json.dump(self.compare or {}, handle)
        headref_path = os.path.join(work, "headref.json")
        with open(headref_path, "w", encoding="utf-8") as handle:
            json.dump(self.head_ref or {}, handle)

        curl_log = os.path.join(work, "curl.log")
        payload_log = os.path.join(work, "curl_payload.log")
        gh_log = os.path.join(work, "gh.log")
        for path in (curl_log, payload_log, gh_log):
            with open(path, "w", encoding="utf-8"):
                pass

        script_path = os.path.join(work, "promo.sh")
        with open(script_path, "w", encoding="utf-8") as handle:
            handle.write("#!/usr/bin/env bash\n" + _step_script())

        env = dict(os.environ)
        env.update(
            {
                "PATH": bindir + os.pathsep + env["PATH"],
                "CURL_LOG": curl_log,
                "CURL_PAYLOAD_LOG": payload_log,
                "CURL_COMPARE_FILE": compare_path,
                "CURL_HEADREF_FILE": headref_path,
                "CURL_COMPARE_MODE": "ok" if self.compare is not None else "fail",
                "CURL_HEADREF_MODE": "ok" if self.head_ref is not None else "fail",
                "CURL_OPEN_PULLS": json.dumps(self.open_pulls),
                "GH_LOG": gh_log,
                "GH_MERGE_AUTO_RC": "0",
                "GITHUB_API_URL": "https://api.github.com",
                "GITHUB_SERVER_URL": "https://github.com",
                "PR_TOKEN": "stub-token",
                "REPOSITORY": REPOSITORY,
                "REPO_OWNER": REPOSITORY.split("/")[0],
                "MERGED_PR_NUMBER": str(self.merged_pr_number),
                "MERGED_PR_TITLE": "chore: consolidate (ci#1066)",
                "MERGED_PR_URL": "https://github.com/example/example/pull/44",
                "DEV_HEAD_SHA": LIVE_DEV_SHA,
            }
        )
        result = subprocess.run(
            ["bash", script_path], capture_output=True, text=True, check=False, env=env
        )
        payloads: list[tuple[str, str, str]] = []
        with open(payload_log, encoding="utf-8") as handle:
            for line in handle.read().splitlines():
                if not line:
                    continue
                method, url, payload = line.split("\t", 2)
                payloads.append((method, url, payload))
        with open(gh_log, encoding="utf-8") as handle:
            gh_calls = [line for line in handle.read().splitlines() if line]
        return result.returncode, result.stdout, payloads, gh_calls


def _patch_payloads(payloads: list[tuple[str, str, str]]) -> list[dict]:
    return [
        json.loads(payload)
        for method, url, payload in payloads
        if method == "PATCH" and f"/pulls/{EXISTING_PR}" in url
    ]


class DevPromotionRefreshTest(unittest.TestCase):
    def test_the_step_shell_is_valid(self) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as handle:
            handle.write(_step_script())
            path = handle.name
        try:
            result = subprocess.run(
                ["bash", "-n", path], capture_output=True, text=True, check=False
            )
        finally:
            pathlib.Path(path).unlink(missing_ok=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_the_refresh_is_documented(self) -> None:
        script = _step_script()
        self.assertIn("ci#1066", script)
        # Title AND body must be re-rendered from live data.
        self.assertIn('--arg body "$body"', script)
        self.assertIn("git/ref/heads/dev", script)

    def test_refresh_patches_the_body_previously_it_only_patched_the_title(self) -> None:
        rc, out, payloads, _ = PromoRun().run()
        self.assertEqual(rc, 0, out)

        patches = _patch_payloads(payloads)
        self.assertEqual(len(patches), 1, payloads)
        patch = patches[0]
        self.assertIn("body", patch, "the refresh must PATCH the body, not only the title")
        self.assertIn("title", patch)

        body = patch["body"]
        # Live data wins: current dev head sha and the real consolidated tickets.
        self.assertIn(LIVE_DEV_SHA, body)
        self.assertIn(LIVE_DEV_SHA_SHORT, patch["title"])
        self.assertIn("ci#1066", body)
        self.assertIn("https://github.com/msgmate-io/open-chat-go-ci/issues/1066", body)
        self.assertIn("ci#1050", body)
        # The ci#1066 bug: stale promotion text survives a refresh.
        self.assertNotIn(STALE_SHA, body)
        self.assertNotIn(f"issues/{STALE_TICKET[1:]}", body)
        self.assertIn("<!-- open-chat-dev-promotion -->", body)
        self.assertIn("Commits ahead of `main` | 3", body)

    def test_an_unresolvable_head_sha_degrades_to_title_only_with_a_warning(self) -> None:
        rc, out, payloads, _ = PromoRun(compare=None, head_ref=None).run()
        # The compare call is what decides whether dev is ahead; without it the
        # step fails loudly rather than publishing an empty promotion body.
        self.assertNotEqual(rc, 0, out)
        self.assertEqual(_patch_payloads(payloads), [])

    def test_a_missing_ref_falls_back_to_the_compare_head(self) -> None:
        rc, out, payloads, _ = PromoRun(head_ref={}).run()
        self.assertEqual(rc, 0, out)
        patches = _patch_payloads(payloads)
        self.assertEqual(len(patches), 1, payloads)
        # `.commits[].sha` is the fallback for the current dev head.
        self.assertIn(LIVE_DEV_SHA, patches[0]["body"])
        self.assertNotIn("unknown", patches[0]["body"])

    def test_no_promotion_when_dev_is_not_ahead(self) -> None:
        compare = dict(COMPARE_JSON, ahead_by=0, total_commits=0, commits=[])
        rc, out, payloads, _ = PromoRun(compare=compare).run()
        self.assertEqual(rc, 0, out)
        self.assertIn("not ahead of main", out)
        self.assertEqual(_patch_payloads(payloads), [])

    def test_ci_833_mentions_and_dev_head_are_refreshed_together(self) -> None:
        rc, out, payloads, gh_calls = PromoRun().run()
        self.assertEqual(rc, 0, out)
        patch = _patch_payloads(payloads)[0]
        self.assertIn("(ci#833)", patch["title"])
        self.assertIn("dev @ ", patch["title"])
        # ci#1077 auto-merge arming stays on the refresh path.
        self.assertTrue(
            any("--merge --auto" in call for call in gh_calls),
            f"auto-merge must stay armed: {gh_calls}",
        )
        # The token must never end up in the rendered body.
        self.assertNotIn("stub-token", patch["body"])
        self.assertNotIn("stub-token", patch["title"])


if __name__ == "__main__":  # pragma: no cover
    unittest.main()