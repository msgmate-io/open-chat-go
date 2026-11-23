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
# ci#769: an uncapped matrix saturates the shared key's in-flight credit and the
# provider answers 402 "would exceed your available credits given your current
# in-flight requests". That is a self-clearing concurrency rejection, not a
# drained balance: classifying it as `account` skipped the retry for every test
# at once and turned the whole guard issue red.
new_case
printf '{"text":"provider error: HTTP 402 This request would exceed your available credits given your current in-flight requests"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "an in-flight-credit 402 is transient, not a drained balance (ci#769)"

new_case
printf '{"text":"HTTP 402: available credits given your current in-flight requests"}\n' > "$case_dir/a.last_bot.json"
assert_class unreachable "the in-flight-credit phrase alone is transient even with a bare 402"

new_case
printf '{"text":"402 would exceed your available credits"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"HTTP 402 This request would exceed your available credits given your current in-flight requests"}\n' > "$case_dir/b.last_bot.json"
assert_class unreachable "every reply on an in-flight-credit 402 is transient"

new_case
printf '{"text":"HTTP 402 This request would exceed your available credits given your current in-flight requests"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"I edited the wrong file and the test asserts failed"}\n' > "$case_dir/b.last_bot.json"
assert_class "" "an in-flight 402 mixed with a coder failure still stays a code failure"

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

# --- empty_reply: the model returned no text - pure model variance -----------
new_case
printf '{"text":"model returned no text for this turn"}\n' > "$case_dir/a.last_bot.json"
assert_class empty_reply "a pure empty reply is model variance, not a code regression (ci#938)"

new_case
printf '{"text":"model returned no text for this turn"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"model returned no text for this turn"}\n' > "$case_dir/b.last_bot.json"
assert_class empty_reply "every reply empty is model variance even across files"

new_case
printf '{"text":"model returned no text for this turn"}\n' > "$case_dir/a.last_bot.json"
printf '{"text":"I edited the wrong file and the test asserts failed"}\n' > "$case_dir/b.last_bot.json"
assert_class "" "an empty reply mixed with a coder failure stays a code failure"

new_case
printf '{"text":"Model returned no text for this turn"}\n' > "$case_dir/a.last_bot.json"
assert_class empty_reply "the empty-reply match is case-insensitive"

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

# --- turn_stall: the turn never started (ci#1172) ---------------------------
# assert_stall <expected> <description> -- same contract as assert_class but for
# sandbox_turn_stall_class, which reads attempt/probe logs instead of replies.
assert_stall() {
  local expected="$1" description="$2"
  case_no=$(( case_no + 1 ))
  local actual
  actual="$(sandbox_turn_stall_class "$case_dir")"
  if [ "$actual" = "$expected" ]; then
    printf 'ok %d - %s\n' "$case_no" "$description"
  else
    printf 'not ok %d - %s (expected=%q actual=%q)\n' "$case_no" "$description" "$expected" "$actual"
    failures=$(( failures + 1 ))
  fi
}

# The ci#1172 fingerprint as the probe writes it: a timeout of a chat that is
# still active with no bot turn, so no reply exists to classify.
stall_log() {
  printf '::warning::sandbox test ran\n'
  printf '##[error]interaction timed out after 720s (runtime=native, state=active, messages=4, last_message=false) the interaction is still active with no bot turn\n'
}

new_case
stall_log > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall turn_stall "an active chat with no bot turn is a stalled turn, not variance"

new_case
stall_log > "$case_dir/msgmate_coder_bugfix.attempt1.log"
stall_log > "$case_dir/msgmate_coder_bugfix.attempt2.log"
assert_stall turn_stall "every attempt stalling is still a stalled turn"

new_case
stall_log > "$case_dir/msgmate_coder_bugfix.attempt1.log"
printf 'FAIL: the marker was never printed\n' > "$case_dir/msgmate_coder_bugfix.attempt2.log"
assert_stall "" "a stall mixed with a real assertion failure stays a code failure"

# The probe appends every attempt of one test to the same <test>.out, so a stall
# of attempt 1 and an assertion failure of attempt 2 can share a single file; the
# later real failure must still win over the earlier stall.
new_case
{ stall_log; printf 'FAIL: the marker was never printed\n'; } > "$case_dir/msgmate_coder_bugfix.out"
assert_stall "" "a stall and a later real failure inside one out file is a code failure"

new_case
printf '##[error]interaction timed out after 720s (runtime=native, state=active, messages=4, last_message=true)\n' > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall "" "a timeout after the bot already answered keeps the ordinary verdict"

new_case
printf 'FAIL: unexpected behaviour\n' > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall "" "an ordinary failing attempt is not a stalled turn"

# ci#1171: a probe log also carries the `run:` block it executed, so the
# annotation markers of the harness *source* appear in the log. Those lines are
# not failures; counting them turned eleven identical ci#1171 probes into
# "stalls=2, other_failures=9" and the stall never classified.
echoed_source_log() {
  stall_log
  printf '  echo "::error::bot token cannot read ${REPO}@${REF}: ${body}"\n'
  printf "  printf '::error::interaction timed out after %ss' \"\$timeout\"\n"
  printf '  warn "::warning::no bot reply in %ss"\n'
}

new_case
echoed_source_log > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall turn_stall "echoed harness source is not an extra failure marker"

# The same block as GitHub renders it when the runner is configured with colour:
# ANSI-wrapped source lines still must not mask the stall.
new_case
{
  stall_log
  printf '  \033[36;1mecho "::error::sandbox test ${name} failed" \033[0m\n'
  printf '  \033[36;1mprintf %s %s \033[0m\n' "'::error::no bot reply'" '"$name"'
} > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall turn_stall "ANSI-coloured echoed source is not an extra failure marker"

# A rendered annotation (not echoed source) is still a real failure marker.
new_case
{ stall_log; printf '::error::assertion failed: expected 3 got 4\n'; } > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall "" "a rendered ::error annotation still beats a stall"

# The harness's own derived verdict annotation must not mask the stall either.
new_case
{ stall_log; printf '##[error]Process completed with exit code 1.\n'; } > "$case_dir/msgmate_coder_bugfix.attempt1.log"
assert_stall turn_stall "the runner's exit-code annotation is not an extra failure marker"

new_case
assert_stall "" "an empty results dir has no stalled turn"

new_case
printf 'ok\n' > "$case_dir/msgmate_coder_bugfix.out"
assert_stall "" "a passing run is not a stalled turn"

if [ "$failures" -ne 0 ]; then
  printf 'test_sandbox_provider_failure: FAILED (%d/%d)\n' "$failures" "$case_no" >&2
  exit 1
fi
printf 'test_sandbox_provider_failure: OK (%d checks)\n' "$case_no"
