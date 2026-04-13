#!/usr/bin/env bash
# Unit tests for development/scripts/sandbox_provider_failure.sh.
#
# Pins the classifier the sandbox benchmark workflow uses to tell a provider-side
# infrastructure failure apart from a real coder regression (ci#740 follow-up):
# a drained balance / bad key must never be retried, a transient provider outage
# must be reported as infrastructure instead of burning a second full attempt,
# and anything mixed with an ordinary failure must keep the ordinary verdict.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../sandbox_provider_failure.sh
source "${script_dir}/../sandbox_provider_failure.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failures=0
case_no=0

# assert_class <expected> <description> -- reads the reply JSON files staged in
# "$case_dir" (created by the caller) and compares the classifier output.
assert_class() {
  local expected="$1" description="$2"
  case_no=$(( case_no + 1 ))
  local actual
  actual="$(sandbox_provider_failure_class "$case_dir")"
  if [ "$actual" = "$expected" ]; then
    printf 'ok %d - %s\n' "$case_no" "$description"
  else
    printf 'not ok %d - %s (expected=%q actual=%q)\n' "$case_no" "$description" "$expected" "$actual"
    failures=$(( failures + 1 ))
  fi
}

new_case() {
  case_dir="$work/case-$case_no-$RANDOM"
  mkdir -p "$case_dir"
}

# --- account: a drained balance or a bad key, never retryable ---------------
new_case
printf '{"text":"provider error: HTTP 402 payment required"}\n' > "$case_dir/a.last_bot.json"
assert_class account "HTTP 402 is an account failure"

new_case
printf '{"text":"HTTP 401 invalid_api_key"}\n' > "$case_dir/a.last_bot.json"
assert_class account "HTTP 401 is an account failure"

new_case
printf '{"text":"You exceeded your current quota (insufficient_quota)"}\n' > "$case_dir/a.last_bot.json"
assert_class account "insufficient_quota is an account failure"

new_case
printf '{"text":"Please add a positive balance to continue"}\n' > "$case_dir/a.last_bot.json"
assert_class account "positive balance is an account failure"

new_case
printf '{"text":"upstream said HTTP 429: insufficient_quota"}\n' > "$case_dir/a.last_bot.json"
assert_class account "a quota error surfaced as 429 still wins over unreachable"

new_case
printf '{"text":"HTTP 401 invalid_api_key"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"HTTP 402 payment required"}\n' > "$case_dir/b.last_bot.json"
assert_class account "every reply on an account error is an account failure"

# --- unreachable: transient provider infrastructure -------------------------
new_case
printf '{"text":"I can'"'"'t reach my AI provider right now. This looks like a temporary network problem reaching the AI provider."}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "the bot's provider-unreachable text is transient"

new_case
printf '{"text":"provider returned HTTP 429 rate limit exceeded"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "HTTP 429 is transient"

new_case
printf '{"text":"provider returned HTTP 408 request timeout"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "HTTP 408 is transient (matches the Go retry list)"

new_case
printf '{"text":"provider returned HTTP 503 service unavailable"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"read tcp: connection reset by peer"}\n' > "$case_dir/b.last_bot.json"
assert_class unreachable "HTTP 5xx together with a connection reset is transient"

new_case
printf '{"text":"dial tcp: lookup openrouter.ai: no such host"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "a DNS resolution failure is transient"

new_case
printf '{"text":"Post \\"https://api.openai.com\\": proxyconnect tcp: i/o timeout"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "a proxy/i-o timeout is transient"

new_case
printf '{"text":"It ended on an internal error (request failed: net/http: TLS handshake timeout). The work already done is kept."}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "the mixed-case Go TLS handshake timeout text is transient"

new_case
printf '{"text":"It ended on an internal error (request failed: unexpected EOF). The work already done is kept."}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "the mixed-case Go unexpected EOF text is transient"

# --- unclassified: the test verdict stands ---------------------------------
new_case
printf '{"text":"I could not finish: HTTP 503 service unavailable"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"I edited the wrong file and the test asserts failed"}\n' > "$case_dir/b.last_bot.json"
assert_class "" "a mixed provider + coder failure stays a code failure"

new_case
printf '{"text":"the fix is applied but the test still fails"}\n' > "$case_dir/a.last_bot.json"
assert_class "" "an ordinary coder failure stays a code failure"

new_case
assert_class "" "an empty results dir is unclassified"

new_case
assert_class "" "a missing results dir is unclassified"

# Left in place for the next case_no increment; nothing after this point.
new_case
printf '{"text":"ok"}\n' > "$case_dir/not-a-reply.txt"
assert_class "" "files that are not *.last_bot.json are ignored"

if [ "$failures" -ne 0 ]; then
  printf 'test_sandbox_provider_failure: FAILED (%d/%d)\n' "$failures" "$case_no" >&2
  exit 1
fi
printf 'test_sandbox_provider_failure: OK (%d checks)\n' "$case_no"
