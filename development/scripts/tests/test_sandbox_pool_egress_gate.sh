#!/usr/bin/env bash
# ci#613: pin the contract of the pool-level DNS/egress readiness gate script.
#
# The gate decides whether a pooled backing sandbox may accept leases, so its
# observable behaviour must not silently regress: it must always emit a parseable
# single-line JSON report, exit non-zero when the pool is not egress-ready and
# exit zero when it is (with --print-only never failing the caller).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/sandbox-pool-egress-gate.sh"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

[ -x "$SCRIPT" ] || fail "sandbox-pool-egress-gate.sh must exist and be executable"

# 1. A healthy probe set (a resolvable name and a reachable local port) exits 0
#    and prints a single-line JSON report carrying every probe target.
healthy_targets="dns:localhost,tcp:127.0.0.1:1"
if command -v python3 >/dev/null 2>&1; then
  # A local listener gives us a deterministic reachable endpoint.
  python3 - <<'PY' &
import socket
srv = socket.socket()
srv.bind(("127.0.0.1", 0))
srv.listen(1)
print(srv.getsockname()[1], flush=True)
import time
time.sleep(20)
PY
  sleep 1
fi

out="$(SANDBOX_POOL_EGRESS_TARGETS="$healthy_targets" bash "$SCRIPT" --pool test-pool 2>/dev/null)"
rc=$?
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ]; then
  fail "unexpected exit code $rc for a resolvable/refusing probe set (expected 0 or 1)"
fi
if [ "$(printf '%s' "$out" | wc -l)" -ne 0 ]; then
  fail "the JSON report must be a single line (stdout only)"
fi
case "$out" in
  '{"pool":"test-pool"'*) ;;
  *) fail "report should be JSON and carry the pool name, got: $out" ;;
esac
case "$out" in
  *'"targets":['*) ;;
  *) fail "report must carry a targets array" ;;
esac

# 2. A guaranteed-unreachable target fails the gate (exit 1) and still prints a
#    JSON report naming the target and a remediation hint.
out="$(SANDBOX_POOL_EGRESS_TARGETS='dns:invalid.invalid,tcp:127.0.0.1:1' bash "$SCRIPT" --pool broken-pool 2>/dev/null)"
rc=$?
if [ "$rc" -ne 1 ]; then
  fail "a pool that cannot resolve DNS must fail the gate (exit 1), got $rc"
fi
case "$out" in
  *'"ok":false'*) ;;
  *) fail "the failing probe must be reported with ok:false, got: $out" ;;
esac
case "$out" in
  *'"remediation"'*) ;;
  *) fail "a failing probe must carry a remediation hint" ;;
esac
case "$out" in
  *'"target":"dns:invalid.invalid"'*) ;;
  *) fail "the failing probe must name its target" ;;
esac

# 3. --print-only reports the same failure but never fails the caller, so the
#    pool start path can ingest the report and decide itself.
SANDBOX_POOL_EGRESS_TARGETS='dns:invalid.invalid' bash "$SCRIPT" --pool broken-pool --print-only >/dev/null 2>&1
rc=$?
if [ "$rc" -ne 0 ]; then
  fail "--print-only must exit 0 even for a failing pool, got $rc"
fi

# 4. The report must be valid JSON (and must never contain credential material).
if command -v python3 >/dev/null 2>&1; then
  if ! printf '%s' "$out" | python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1; then
    fail "the emitted report must parse as JSON"
  fi
fi
case "$out" in
  *TOKEN* | *token* | *SECRET* | *kubeconfig*)
    fail "the report must not carry credentials/tokens/kubeconfig"
    ;;
esac

if [ "$failures" -ne 0 ]; then
  printf 'sandbox pool egress gate: %d check(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'sandbox pool egress gate: all checks passed\n'
