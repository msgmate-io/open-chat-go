#!/usr/bin/env bash
# Self-test for the PR CI merge-gate verdict (ci#1033, ci#1034).
#
#   bash development/scripts/tests/test_merge_gate_verdict.sh
#
# The `PR Test Merge Requirement` job decides a required check from two pieces
# of logic that both used to be wrong in a way no unit test covered end to end:
#
#   * ci#1034 - which leaf issues a PR body closes. Two anchored copies of one
#     `grep -oiE '^...$'` lived in the workflow; anchoring correctly rejects
#     prose, but it also rejected the same line wrapped in ordinary markdown
#     (a bullet, bold, a table cell) or ending in a full stop. Both call sites
#     now share `development/scripts/linked_ci_issues.sh`, exercised below.
#   * ci#1033 - which attempt's verdict counts. A cancelled attempt tested
#     nothing, so it must never be read as red or green, and a still-running
#     newer attempt for the same SHA means the commit is *pending*, not red.
#     Measured on head e846fb8d: five runs (four cancelled, one live) and the
#     required check stayed `COMPLETED/FAILURE` from attempt 1 while attempt 2
#     ran, so a green tree read red. The cases below drive the real reconciler
#     with a fake API and assert the adopted verdict plus the logged run id.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PARSER="${ROOT}/development/scripts/linked_ci_issues.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

failures=0
ok() { echo "ok   $1"; }
fail() { echo "FAIL $1: $2"; failures=$((failures + 1)); }

# --- ci#1034: which issues does a PR body close? -----------------------------

# parser_case <name> <expected numbers> <<< body
parser_case() {
  local name="$1" expected="$2" got
  cat > "${TMP}/body.md"
  got="$(bash "${PARSER}" "${TMP}/body.md" | tr '\n' ' ' | sed -E 's/[[:space:]]+$//')"
  if [ "${got}" = "${expected}" ]; then
    ok "linked-issue: ${name}"
  else
    fail "linked-issue: ${name}" "expected '${expected}', got '${got}'"
  fi
}

parser_case "standalone line" "1033" <<'BODY'
## Tracker

Closes msgmate-io/open-chat-go-ci#1033
BODY

parser_case "bullet-wrapped line" "1033" <<'BODY'
## Tracker

- Closes msgmate-io/open-chat-go-ci#1033
BODY

parser_case "bold line" "1033" <<'BODY'
**Closes msgmate-io/open-chat-go-ci#1033**
BODY

parser_case "table cell" "1034" <<'BODY'
| Tracker |
| --- |
| Closes msgmate-io/open-chat-go-ci#1034 |
BODY

parser_case "trailing punctuation and both standalone lines" "1033 1034" <<'BODY'
## Tracker

- **Closes msgmate-io/open-chat-go-ci#1033**.
Fixes open-chat-go-ci#1034.
BODY

parser_case "no reference" "" <<'BODY'
## Summary

Nothing to close here.
BODY

parser_case "prose is not a closing line" "" <<'BODY'
Fix ci#824: crash on empty config
BODY

parser_case "second issue in the same sentence is not linked" "" <<'BODY'
Closes ci#824 and also ci#999
BODY

parser_case "template placeholder is not a reference" "" <<'BODY'
## Tracker

Closes msgmate-io/open-chat-go-ci#<n>
BODY

if bash "${PARSER}" >/dev/null 2>&1; then
  fail "linked-issue: usage" "a missing body file must exit non-zero"
else
  ok "linked-issue: usage rejected"
fi

# --- ci#1033: which attempt's verdict counts? --------------------------------

# Drives the real reconciler (development/scripts/pr_ci_superseded_reconcile.py)
# with an in-process fake API, exactly like the unit tests, but through the same
# shell entrypoint the workflow uses, so a broken import/path fails here too.
cat > "${TMP}/gate_case.py" <<'PY'
import argparse
import sys

sys.path.insert(0, sys.argv[1])
import pr_ci_superseded_reconcile as mod  # noqa: E402

GATE = mod.GATE_JOB_NAME
HEAD = "e846fb8d2b1c0e8dd2a5f6db4c3f1a0d9b8c7e6f"
CURRENT = 130296565582   # attempt 1: the run whose red gate was observed
NEWER = 130296999999     # attempt 2 for the same SHA


def run(run_id, status="completed", conclusion=None):
    payload = {"id": run_id, "status": status, "run_attempt": 2}
    if conclusion is not None:
        payload["conclusion"] = conclusion
    return payload


def gate(conclusion="success"):
    return {"name": GATE, "status": "completed", "conclusion": conclusion}


def drive(runs, jobs):
    """Run reconcile() against a fake GitHub and return (code, summary)."""

    def fetch(url, token):
        if "/pulls/" in url:
            return {"state": "open", "head": {"sha": HEAD}, "labels": []}
        if "/jobs" in url:
            run_id = int(url.split("/actions/runs/")[1].split("/")[0])
            return {"jobs": jobs.get(run_id, [])}
        return {"workflow_runs": runs}

    mod._FETCH = fetch
    mod._POST = lambda url, token, timeout=30: {}
    mod._MONOTONIC = lambda: 1000.0
    mod._NOW = lambda: 1000000.0
    mod._SLEEP = lambda seconds: None
    args = argparse.Namespace(
        repo="msgmate-io/open-chat-go",
        pr=874,
        head_sha=HEAD,
        run_id=CURRENT,
        wait_seconds=0,
        yield_seconds=60,
    )
    return mod.reconcile(args, "token")


name = sys.argv[2]
if name == "cancelled-attempt-then-green":
    codes, summary = drive(
        [run(CURRENT), run(NEWER, conclusion="cancelled"), run(NEWER + 1, conclusion="success")],
        {NEWER: [gate("cancelled")], NEWER + 1: [gate()]},
    )
    expected = "replacement run %s gate job concluded success" % (NEWER + 1)
elif name == "newer-attempt-pending":
    codes, summary = drive([run(CURRENT), run(NEWER, status="in_progress")], {})
    expected = "newer attempt run %s" % NEWER
elif name == "newest-real-red-still-rejects":
    codes, summary = drive([run(CURRENT), run(NEWER + 1)], {NEWER + 1: [gate("failure")]})
    expected = "concluded failure"
    assert codes == 1, "a real red verdict must still reject, got exit %s" % codes
    print("ok")
    sys.exit(0)
else:
    raise SystemExit("unknown case %s" % name)

assert codes == 0, "expected exit 0, got %s (%s)" % (codes, summary)
assert expected in summary, "expected %r in summary, got %r" % (expected, summary)
print("ok")
PY

# gate_case <name> <expected>
gate_case() {
  local name="$1"
  if python3 "${TMP}/gate_case.py" "${ROOT}/development/scripts" "${name}" > "${TMP}/gate.out" 2>&1; then
    ok "verdict: ${name}"
  else
    fail "verdict: ${name}" "$(tail -3 "${TMP}/gate.out" | tr '\n' ' ')"
  fi
}

# Four cancelled attempts, then a green one: the newest *completed, non-cancelled*
# attempt decides, and the winning run id is logged.
gate_case "cancelled-attempt-then-green"
# A newer attempt is still running: the commit is pending, it must not inherit
# the older attempt's red.
gate_case "newer-attempt-pending"
# The fix must not weaken the gate: a real red on the newest attempt rejects.
gate_case "newest-real-red-still-rejects"

if [ "${failures}" -ne 0 ]; then
  echo "${failures} merge-gate verdict case(s) failed."
  exit 1
fi
echo "merge-gate verdict self-test passed."
