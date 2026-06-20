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

  local file files=0 account_hits=0 unreachable_hits=0
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
    fi
  done

  [ "$files" -gt 0 ] || return 0
  if [ "$account_hits" -eq "$files" ]; then
    printf 'account\n'
  elif [ "$unreachable_hits" -eq "$files" ]; then
    printf 'unreachable\n'
  fi
}
