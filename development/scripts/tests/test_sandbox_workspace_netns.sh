#!/usr/bin/env bash
# ci#612: pin the contract of the per-workspace network namespace script that
# gives a *pooled* workspace its own netns (follow-up of the ci#546 shared
# sandbox networking design).
#
# The script is the enforcement point of the ticket's acceptance criteria, so its
# observable behaviour must not regress:
#
#   1. the namespace is derived deterministically from the workspace uuid and two
#      workspaces never share one;
#   2. `up` reports the exec prefix that joins the namespace plus the slirp
#      sidecar that keeps egress/DNS and disables the host loopback;
#   3. `verify` fails (exit 1) when a sibling slot's bound port is reachable from
#      the namespace - a shared pod netns must never be handed out silently;
#   4. `verify --print-only` always exits 0 so the caller can ingest the report;
#   5. the report is a single line of valid JSON with no credential material;
#   6. nothing here touches the host: every check runs with --dry-run.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/sandbox-workspace-netns.sh"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

[ -x "$SCRIPT" ] || fail "sandbox-workspace-netns.sh must exist and be executable"

# 1. `up` is deterministic per workspace, distinct across workspaces, and
#    reports everything the exec path needs.
out_a="$(SANDBOX_WORKSPACE_NETNS_DRY_RUN=1 bash "$SCRIPT" up --workspace ws-a 2>/dev/null)"
rc=$?
[ "$rc" -eq 0 ] || fail "up must succeed for ws-a, got exit $rc"
out_a2="$(SANDBOX_WORKSPACE_NETNS_DRY_RUN=1 bash "$SCRIPT" up --workspace ws-a 2>/dev/null)"
out_b="$(SANDBOX_WORKSPACE_NETNS_DRY_RUN=1 bash "$SCRIPT" up --workspace ws-b 2>/dev/null)"

[ "$out_a" = "$out_a2" ] || fail "the namespace name must be deterministic (re-attach)"
[ "$out_a" != "$out_b" ] || fail "two workspaces must not get the same namespace"

case "$out_a" in
  '{"action":"up"'*) ;;
  *) fail "up must emit a JSON report, got: $out_a" ;;
esac
case "$out_a" in
  *'"namespace":"wsc-'*) ;;
  *) fail "the namespace must carry the wsc- prefix, got: $out_a" ;;
esac
case "$out_a" in
  *'"exec_prefix":"ip netns exec wsc-'*' --"'*) ;;
  *) fail "up must report the exec prefix that joins the namespace, got: $out_a" ;;
esac
case "$out_a" in
  *'"shared_pod_netns":false'*) ;;
  *) fail "a per-workspace netns must never report shared_pod_netns:true" ;;
esac
case "$out_a" in
  *'"resolv_conf":"/etc/netns/'*) ;;
  *) fail "up must report the per-namespace resolv.conf so DNS keeps working" ;;
esac
case "$out_a" in
  *slirp4netns*) ;;
  *) fail "up must report the egress sidecar" ;;
esac

# 2. verify: a healthy namespace (egress ok, sibling port unreachable) exits 0.
out="$(SANDBOX_WORKSPACE_NETNS_DRY_RUN=1 bash "$SCRIPT" verify --workspace ws-a --sibling-port 3999 2>/dev/null)"
rc=$?
[ "$rc" -eq 0 ] || fail "a healthy namespace must pass verify, got exit $rc"
case "$out" in
  *'"ok":true'*) ;;
  *) fail "the healthy report must carry ok:true, got: $out" ;;
esac
case "$out" in
  *'"sibling_isolated":true'*) ;;
  *) fail "the healthy report must state the sibling isolation, got: $out" ;;
esac
case "$out" in
  *'"egress_ok":true'*) ;;
  *) fail "the healthy report must state egress health, got: $out" ;;
esac

# 3. verify: an unreachable egress endpoint fails the gate (exit 1) and carries a
#    remediation hint naming the namespace. PROBE_PREFIX overrides the context the
#    probe runs in, so the test needs no CAP_NET_ADMIN: `false` models "the probe
#    inside the namespace cannot reach the endpoint".
out="$(SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX=false SANDBOX_WORKSPACE_NETNS_PROBE_TIMEOUT=1 \
  bash "$SCRIPT" verify --workspace ws-a --egress-host 192.0.2.1 2>/dev/null)"
rc=$?
if [ "$rc" -ne 1 ]; then
  fail "a namespace without egress must fail verify (exit 1), got $rc"
fi
case "$out" in
  *'"ok":false'*) ;;
  *) fail "the failing report must carry ok:false, got: $out" ;;
esac
case "$out" in
  *'"remediation"'*) ;;
  *) fail "a failing check must carry a remediation hint, got: $out" ;;
esac

# 4. verify: a reachable sibling port is a refusal (the workspace is still in the
#    pod netns). Bind a local listener and point --sibling-port at it.
sibling_pid=""
if command -v python3 >/dev/null 2>&1; then
  port_file="$(mktemp)"
  python3 - "$port_file" <<'PY' &
import socket, sys, time
srv = socket.socket()
srv.bind(("127.0.0.1", 0))
srv.listen(4)
with open(sys.argv[1], "w") as fh:
    fh.write(str(srv.getsockname()[1]))
    fh.flush()
time.sleep(20)
PY
  sibling_pid=$!
  for _ in $(seq 1 50); do
    [ -s "$port_file" ] && break
    sleep 0.1
  done
  sibling_port="$(cat "$port_file" 2>/dev/null || true)"
  rm -f "$port_file"
  if [ -n "${sibling_port:-}" ]; then
    out="$(SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX=true SANDBOX_WORKSPACE_NETNS_PROBE_TIMEOUT=1 \
      bash "$SCRIPT" verify --workspace ws-a --sibling-port "$sibling_port" --egress-host 192.0.2.1 2>/dev/null)"
    rc=$?
    [ "$rc" -eq 1 ] || fail "a reachable sibling port must fail verify (exit 1), got $rc"
    case "$out" in
      *'"kind":"sibling-port"'*) ;;
      *) fail "the report must carry a sibling-port check, got: $out" ;;
    esac
    case "$out" in
      *'"reachable":true'*) ;;
      *) fail "the reachable sibling must be reported explicitly, got: $out" ;;
    esac
    case "$out" in
      *'"sibling_isolated":false'*) ;;
      *) fail "the report must state the isolation failure, got: $out" ;;
    esac

    # 5. --print-only never fails the caller, even for a broken boundary.
    SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX=true SANDBOX_WORKSPACE_NETNS_PROBE_TIMEOUT=1 \
      bash "$SCRIPT" verify --workspace ws-a --sibling-port "$sibling_port" \
        --egress-host 192.0.2.1 --print-only >/dev/null 2>&1
    rc=$?
    [ "$rc" -eq 0 ] || fail "--print-only must exit 0 even for a broken boundary, got $rc"
  else
    fail "could not determine a sibling test port"
  fi
fi
[ -n "$sibling_pid" ] && kill "$sibling_pid" 2>/dev/null

# 6. usage errors are exit code 2, never a silent success.
bash "$SCRIPT" >/dev/null 2>&1
[ "$?" -eq 2 ] || fail "a missing action must exit 2"
bash "$SCRIPT" verify >/dev/null 2>&1
[ "$?" -eq 2 ] || fail "a missing --workspace must exit 2"
bash "$SCRIPT" exec --workspace ws-a >/dev/null 2>&1
[ "$?" -eq 2 ] || fail "exec without a command must exit 2"

# 7. The report is a single line of valid JSON without credential material.
if command -v python3 >/dev/null 2>&1; then
  if ! printf '%s' "$out_a" | python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1; then
    fail "the up report must parse as JSON, got: $out_a"
  fi
fi
if [ "$(printf '%s' "$out_a" | wc -l)" -ne 0 ]; then
  fail "the report must be a single line (stdout only)"
fi
case "$out_a" in
  *TOKEN* | *token* | *SECRET* | *secret* | *kubeconfig* | *ghp_*)
    fail "the report must not carry credentials/tokens/kubeconfig"
    ;;
esac

if [ "$failures" -ne 0 ]; then
  printf 'sandbox workspace netns: %d check(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'sandbox workspace netns: all checks passed\n'
