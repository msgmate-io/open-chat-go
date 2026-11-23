#!/usr/bin/env bash
# Classify why a coder-sandbox benchmark test failed, so the workflow can tell a
# provider-side infrastructure problem apart from a real coder regression.
#
# Source this file (it has no side effects on source) and call
# `sandbox_provider_failure_class <results-dir>`; it prints one of:
#
#   account      every recorded bot reply failed on the provider's own
#                credential/balance check (HTTP 401/402, drained balance). A
#                retry can never fix it (ci#740).
#   unreachable  every recorded bot reply could not reach the AI provider, or was
#                rejected by it with a retryable rate-limit/server error
#                (HTTP 408/429/5xx, connection-level failures). Retrying burns
#                another full sandbox run per test, so this is reported as
#                transient provider infrastructure instead (ci#740 follow-up).
#                An HTTP 402 caused by the provider's *in-flight credit* cap
#                belongs here too: it clears as soon as the concurrent requests
#                drain, so it is transient and must be retried rather than read
#                as a drained balance (ci#769).
#   (empty)      not a pure provider failure: at least one reply is missing or
#                looks like an ordinary failure, so the test verdict stands.
#
#   empty_reply  every recorded bot reply came back empty for a turn ("model
#                returned no text for this turn"). A pure empty completion is
#                model variance, not a code regression: the workflow accepts it
#                after both attempts instead of turning the gate red (ci#938).
#
# The patterns mirror the Go-side markers used by the backend when it decides
# whether a provider error is retryable
# (`backend/api/msgmate/provider_retry.go`) and the bot text it returns to the
# user when the provider is unreachable
# (`backend/api/msgmate/MsgmateBotAIHandler.go`). The Go comparison lowercases
# the error first (`strings.ToLower`), so the matching here is case-insensitive
# as well: the real texts are "net/http: TLS handshake timeout" and io's
# "unexpected EOF", both of which the backend retries.

# A 402 that names the provider's *in-flight credit* cap is not a drained
# balance: the key does have credit, it is only rejecting the request because
# too many sibling requests are in flight against the same key. That clears by
# itself as soon as those requests drain, so it must be retried instead of being
# frozen as an account failure (ci#769: an uncapped sandbox matrix tripped this
# on every test at once, so no test ever got its retry and the guard issue went
# all-red). Checked before the account pattern on purpose: the in-flight 402 is
# the more specific signal and must win over the generic "HTTP 402".
sandbox_provider_inflight_pattern='would exceed your available credits'
sandbox_provider_inflight_pattern+='|available credits given your current in-flight'
sandbox_provider_inflight_pattern+='|in-flight request|in flight request|in-flight credit'

# A drained balance or a bad key: never retry, always infrastructure.
sandbox_provider_account_pattern='HTTP (401|402)|positive balance|insufficient_quota|invalid_api_key'

# The model answered nothing at all for a turn (a pure empty reply: the backend
# reports "model returned no text for this turn"). Every attempt can hit this on
# pure model variance (a refused or blank completion), so it is retryable and
# accepted as variance, not a coder regression. Checked AFTER the provider
# classes so a provider-side error that happens to mention an empty response
# still keeps its real class (ci#938).
sandbox_provider_empty_reply_pattern='model returned no text for this turn'

# An outage, a rate limit or a connection-level failure: transient provider
# infrastructure, not something the coder can influence.
sandbox_provider_unreachable_pattern='HTTP (408|429|500|502|503|504)'
sandbox_provider_unreachable_pattern+='|rate limit|too many requests'
sandbox_provider_unreachable_pattern+='|cannot reach|can'"'"'t reach|could not reach'
sandbox_provider_unreachable_pattern+='|temporary network problem'
sandbox_provider_unreachable_pattern+='|connection reset|connection refused|connection timed out'
sandbox_provider_unreachable_pattern+='|connection aborted|broken pipe|unexpected eof'
sandbox_provider_unreachable_pattern+='|tls handshake timeout|server closed idle connection'
sandbox_provider_unreachable_pattern+='|proxyconnect tcp|i/o timeout'
sandbox_provider_unreachable_pattern+='|no such host|temporary failure in name resolution|dial tcp'

# sandbox_provider_failure_class <results-dir>
sandbox_provider_failure_class() {
  local dir="${1:-}"
  [ -n "$dir" ] && [ -d "$dir" ] || return 0

  local file files=0 account_hits=0 unreachable_hits=0 empty_reply_hits=0
  for file in "$dir"/*.last_bot.json; do
    [ -f "$file" ] || continue
    files=$(( files + 1 ))
    # An in-flight-credit 402 is the transient class, so it must be tested before
    # the account pattern: the generic "HTTP 402" arm of that pattern would
    # otherwise freeze a self-clearing concurrency rejection as a drained
    # balance, and the workflow skips the retry for account failures (ci#769).
    if grep -qiE "$sandbox_provider_inflight_pattern" "$file"; then
      unreachable_hits=$(( unreachable_hits + 1 ))
    # Account wins over unreachable: a drained balance is sometimes surfaced as
    # a 429, which must not be retried as a transient outage. Both matchings are
    # case-insensitive because the backend lowercases the provider error before
    # comparing it against the same markers.
    elif grep -qiE "$sandbox_provider_account_pattern" "$file"; then
      account_hits=$(( account_hits + 1 ))
    elif grep -qiE "$sandbox_provider_unreachable_pattern" "$file"; then
      unreachable_hits=$(( unreachable_hits + 1 ))
    elif grep -qiE "$sandbox_provider_empty_reply_pattern" "$file"; then
      empty_reply_hits=$(( empty_reply_hits + 1 ))
    fi
  done

  [ "$files" -gt 0 ] || return 0
  if [ "$account_hits" -eq "$files" ]; then
    printf 'account\n'
  elif [ "$unreachable_hits" -eq "$files" ]; then
    printf 'unreachable\n'
  elif [ "$empty_reply_hits" -eq "$files" ]; then
    printf 'empty_reply\n'
  fi
}

# A turn that never started. The probe gives up after its full window on a chat
# that is still `state=active`: the user message is stored, no bot message ever
# appeared, so there is no reply at all. That is the ci#1172 fingerprint - a
# relay of N private-sandbox jobs of the same run, one identical probe timeout
# each, and no failed test name anywhere in the logs.
#
# Note what this is NOT: no bot reply was ever recorded, so the provider
# classifiers (`account`/`unreachable`/`empty_reply`) have nothing to look at,
# and a retry just buys a second identical probe window without new evidence.
sandbox_turn_stall_pattern='interaction timed out after [0-9]+s'
sandbox_turn_stall_pattern+='.*state=active'
sandbox_turn_stall_pattern+='.*(last_message=false|no bot message|bot_messages=0)'

# sandbox_turn_stall_class <attempt-log-or-results-dir>
#
#   turn_stall  every attempt log that shows a probe timeout shows the ci#1172
#               stall (chat stayed active, no bot turn) and no other failure
#               marker is present, so the turn provably never started.
#   (empty)     anything else - a passing run, a real assertion failure, or a
#               timeout of a chat that had already produced a bot turn - so the
#               ordinary verdict and retry behaviour stand.
sandbox_turn_stall_class() {
  local target="${1:-}"
  [ -n "$target" ] || return 0

  local files=() file
  if [ -d "$target" ]; then
    for file in "$target"/*.attempt*.log "$target"/*.out; do
      [ -f "$file" ] && files+=( "$file" )
    done
  elif [ -f "$target" ]; then
    files=( "$target" )
  fi
  [ "${#files[@]}" -gt 0 ] || return 0

  # Count per line, not per file: the probe appends every attempt of a test to
  # the same `<test>.out`, so one file can hold a stall of attempt 1 and a real
  # assertion failure of attempt 2. Classifying per file would let the earlier
  # stall swallow the later failure and turn a code regression into "the turn
  # never started". The stall lines themselves carry the harness's `::error`
  # annotation, so they are removed before looking for other failure markers.
  local stalls=0 other_failures=0 found
  for file in "${files[@]}"; do
    found="$(grep -ciE "$sandbox_turn_stall_pattern" "$file")"
    stalls=$(( stalls + found ))
    # Echoed workflow/harness *source* is not a failure marker. A probe log
    # carries the `run:` block it executed (ANSI-coloured), so lines such as
    # `  echo "::error::bot token cannot read ${REPO}@${REF}: ..."` or
    # `printf '::error::...'` would otherwise be counted as N real failures and
    # a genuine stall would never classify - exactly what made the ci#1171 relay
    # of eleven identical probes look like eleven unrelated regressions
    # (stalls=2, other_failures=9, all nine of them echoed script text).
    # Strip ANSI colour first (a real ESC byte, so no sed-specific \xHH escape is
    # needed), then drop lines whose annotation marker is quoted inside an
    # echo/printf/warn instead of being a rendered `##[error]`. The two derived
    # annotations the runner and the harness emit *because* the step failed
    # ("Process completed with exit code", "sandbox test ... failed") are not
    # independent evidence either - they are present in every failing job.
    found="$(sed -E $'s/\033\\[[0-9;]*[A-Za-z]//g' "$file" \
      | grep -iE '^FAIL|^not ok|##\[error\]|::error|assertion failed|AssertionError' \
      | grep -viE "$sandbox_turn_stall_pattern" \
      | grep -viE '(^|[[:space:]])(echo|printf|warn|say|log)[[:space:]]+["'"'"']?.*::(error|warning)' \
      | grep -viE '(^|[[:space:]])["'"'"']::(error|warning)' \
      | grep -viE '##\[error\](sandbox test .* failed|Process completed with exit code)' \
      | wc -l)"
    other_failures=$(( other_failures + found ))
  done

  if [ "$stalls" -gt 0 ] && [ "$other_failures" -eq 0 ]; then
    printf 'turn_stall\n'
  fi
}
