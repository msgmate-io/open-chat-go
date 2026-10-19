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
#                (HTTP 429/5xx, connection-level failures). Retrying burns
#                another full sandbox run per test, so this is reported as
#                transient provider infrastructure instead (ci#740 follow-up).
#   (empty)      not a pure provider failure: at least one reply is missing or
#                looks like an ordinary failure, so the test verdict stands.
#
# The patterns mirror the Go-side markers used by the backend when it decides
# whether a provider error is retryable
# (`backend/api/msgmate/provider_retry.go`) and the bot text it returns to the
# user when the provider is unreachable
# (`backend/api/msgmate/MsgmateBotAIHandler.go`).

# A drained balance or a bad key: never retry, always infrastructure.
sandbox_provider_account_pattern='HTTP (401|402)|positive balance|insufficient_quota|invalid_api_key'

# An outage, a rate limit or a connection-level failure: transient provider
# infrastructure, not something the coder can influence.
sandbox_provider_unreachable_pattern='HTTP (429|500|502|503|504)'
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
    # Account wins over unreachable: a drained balance is sometimes surfaced as
    # a 429, which must not be retried as a transient outage.
    if grep -qE "$sandbox_provider_account_pattern" "$file"; then
      account_hits=$(( account_hits + 1 ))
    elif grep -qE "$sandbox_provider_unreachable_pattern" "$file"; then
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
