#!/usr/bin/env bash
# ci#745: behaviour test for development/scripts/upsert_pr_ci_progress_comment.sh.
#
# Hermetic: `curl` and `jq` are stubbed on PATH, so this needs no network and no
# token. Pins the contract the PR-CI progress-comment steps rely on:
#   1. an incomplete target (empty repo/PR) warns and exits 0 without touching the
#      API - the ci#745 fail-safe that replaced the `repos//issues//comments` 404;
#   2. a missing token or body file is a hard error (exit 2);
#   3. an existing marker comment is PATCHed in place (one comment per PR);
#   4. with no marker comment a new comment is POSTed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
HELPER="${REPO_ROOT}/development/scripts/upsert_pr_ci_progress_comment.sh"

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
mkdir -p "${work}/bin"

cat >"${work}/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${FAKE_CURL_LOG}"
if [ -n "${FAKE_CURL_RESPONSE:-}" ]; then
  printf '%s' "${FAKE_CURL_RESPONSE}"
fi
STUB

cat >"${work}/bin/jq" <<'STUB'
#!/usr/bin/env python3
"""Stub jq: payload build for --rawfile, comment id lookup otherwise."""
import os
import sys

args = sys.argv[1:]
if "--rawfile" in args:
    print('{"body":"stub-body"}')
else:
    print(os.environ.get("FAKE_COMMENT_ID", ""))
STUB

chmod +x "${work}/bin/curl" "${work}/bin/jq"
export PATH="${work}/bin:${PATH}"
export FAKE_CURL_LOG="${work}/curl.log"
: >"${FAKE_CURL_LOG}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[ -x "${HELPER}" ] || fail "${HELPER} is missing or not executable"

body="${work}/body.md"
printf 'progress body' >"${body}"

# 1. Incomplete target: warn + exit 0, no API call at all.
: >"${FAKE_CURL_LOG}"
out="$("${HELPER}" --repo "" --pr "" --body-file "${body}" --token "t" 2>&1)"
grep -q '::warning::' <<<"${out}" || fail "empty target must warn, got: ${out}"
if [ -s "${FAKE_CURL_LOG}" ]; then
  fail "empty target must not call the API: $(cat "${FAKE_CURL_LOG}")"
fi

# 2. Missing body file is a hard error.
set +e
"${HELPER}" --repo "o/r" --pr 7 --body-file "${work}/nope.md" --token "t" >/dev/null 2>&1
rc=$?
set -e
[ "${rc}" -eq 2 ] || fail "missing body file must exit 2, got ${rc}"

# 3. Missing token is a hard error.
set +e
"${HELPER}" --repo "o/r" --pr 7 --body-file "${body}" --token "" >/dev/null 2>&1
rc=$?
set -e
[ "${rc}" -eq 2 ] || fail "missing token must exit 2, got ${rc}"

# 4. Existing marker comment: PATCH in place, no duplicate POST.
export FAKE_COMMENT_ID=42
export FAKE_CURL_RESPONSE='[{"id":42,"user":{"login":"cur1ousdude"},"body":"marker"}]'
: >"${FAKE_CURL_LOG}"
"${HELPER}" --repo "o/r" --pr 7 --body-file "${body}" --token "t" --bot-user cur1ousdude >/dev/null
grep -q -- '-X PATCH' "${FAKE_CURL_LOG}" || fail "expected PATCH: $(cat "${FAKE_CURL_LOG}")"
grep -q 'https://api.github.com/repos/o/r/issues/comments/42' "${FAKE_CURL_LOG}" ||
  fail "expected PATCH on the found comment: $(cat "${FAKE_CURL_LOG}")"
if grep -q -- '-X POST' "${FAKE_CURL_LOG}"; then
  fail "must not POST when the marker comment exists"
fi

# 5. No marker comment: POST a new one.
export FAKE_COMMENT_ID=''
: >"${FAKE_CURL_LOG}"
"${HELPER}" --repo "o/r" --pr 7 --body-file "${body}" --token "t" --bot-user cur1ousdude >/dev/null
grep -q -- '-X POST' "${FAKE_CURL_LOG}" || fail "expected POST: $(cat "${FAKE_CURL_LOG}")"
grep -q 'https://api.github.com/repos/o/r/issues/7/comments' "${FAKE_CURL_LOG}" ||
  fail "expected POST to the PR comments endpoint: $(cat "${FAKE_CURL_LOG}")"

echo "OK: upsert_pr_ci_progress_comment.sh contract holds"
