#!/usr/bin/env bash
# ci#546 - shared/pooled sandbox networking pre-flight (bounded shared-netns policy).
#
# A pooled sandbox pod hosts N coding agents that share ONE network namespace, so
# they share DNS/NAT/egress and they would collide on fixed dev-server ports
# (3000, 8080, ...). The policy pinned here is:
#   * one DISJOINT port block per live workspace of the pool pod (default 64 ports),
#   * a per-workspace loopback alias 127.<prefix>.0.<2+index> as the injected
#     CODER_LOCALHOST, so an agent's "localhost:<port>" is its own block,
#   * a bounded number of agents per pod (default 8) to cap resource contention.
#
# backend/sandboxpool (Go) is the normative implementation; this script is the
# pool-side pre-flight/report so an operator can see the allocation and the
# failure modes before handing out leases. It emits no credential material.
set -euo pipefail

MODE="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_MODE:-shared}"
PORT_BASE="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_PORT_BASE:-20000}"
BLOCK_SIZE="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_PORT_BLOCK_SIZE:-64}"
MAX_AGENTS="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_MAX_WORKSPACES:-8}"
LOOPBACK_PREFIX="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_LOOPBACK_PREFIX:-127.64}"
ALIASES="${OCI_MSGMATE_CODER_SANDBOX_NETWORK_LOOPBACK_ALIASES:-1}"
FORMAT="print"
CHECK=0

usage() {
  cat <<'EOF'
usage: sandbox-pool-network-preflight.sh [--print|--json|--check]
       [--mode shared|netns] [--port-base N] [--block-size N]
       [--max-agents N] [--loopback-prefix A.B] [--aliases 0|1]

Prints the per-workspace port block allocation of a pooled sandbox pod and
validates it: block size >= 64, base outside the privileged range, the whole
allocation inside 1..65535, a known mode and a two-octet loopback prefix.
--check prints a single verdict line; --json prints one JSON object per
workspace. Exits non-zero (never zero) on an unusable policy. No secrets.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --json) FORMAT=json ;;
    --print) FORMAT=print ;;
    --check) CHECK=1 ;;
    --mode) MODE="$2"; shift ;;
    --port-base) PORT_BASE="$2"; shift ;;
    --block-size) BLOCK_SIZE="$2"; shift ;;
    --max-agents) MAX_AGENTS="$2"; shift ;;
    --loopback-prefix) LOOPBACK_PREFIX="$2"; shift ;;
    --aliases) ALIASES="$2"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "sandbox-pool-network: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

fail() { echo "sandbox-pool-network: $*" >&2; exit 1; }
is_uint() { case "$1" in ''|*[!0-9]*) return 1 ;; esac; }
is_falsey() { case "$(printf '%s' "$1" | tr 'A-Z' 'a-z')" in 0|false|no|off) return 0 ;; *) return 1 ;; esac; }

case "$MODE" in
  shared|netns) ;;
  *) fail "unknown network mode '$MODE' (expected shared or netns)" ;;
esac
is_uint "$PORT_BASE" || fail "port base must be an integer"
is_uint "$BLOCK_SIZE" || fail "block size must be an integer"
is_uint "$MAX_AGENTS" || fail "max agents must be an integer"
[ "$MAX_AGENTS" -ge 1 ] || fail "max agents per pool pod must be at least 1"
[ "$BLOCK_SIZE" -ge 64 ] || fail "block size $BLOCK_SIZE is below the documented minimum of 64"
[ "$PORT_BASE" -ge 1024 ] || fail "port base $PORT_BASE is in the privileged range (<1024)"
[ "$PORT_BASE" -le $((65535 - BLOCK_SIZE)) ] || fail "port base $PORT_BASE leaves no room for a $BLOCK_SIZE port block below 65535"
LAST=$((PORT_BASE + MAX_AGENTS * BLOCK_SIZE - 1))
[ "$LAST" -le 65535 ] || fail "port allocation overflows: $MAX_AGENTS blocks of $BLOCK_SIZE from $PORT_BASE reach $LAST (>65535)"
case "$LOOPBACK_PREFIX" in *[!0-9.]*|.*|*.) fail "loopback prefix '$LOOPBACK_PREFIX' is not a two-octet IPv4 prefix" ;; esac
[ "$(printf '%s' "$LOOPBACK_PREFIX" | awk -F. 'NF == 2 { print "ok" }')" = "ok" ] \
  || fail "loopback prefix '$LOOPBACK_PREFIX' is not a two-octet IPv4 prefix"

ALIASES_ON=0
is_falsey "$ALIASES" || ALIASES_ON=1

if [ "$CHECK" = "1" ]; then
  echo "sandbox-pool-network: ok mode=$MODE agents=$MAX_AGENTS block=$BLOCK_SIZE range=$PORT_BASE-$LAST"
  if [ "$MODE" = "shared" ] && [ "$ALIASES_ON" = "1" ]; then
    echo "sandbox-pool-network: shared mode injects per-workspace loopback aliases from $LOOPBACK_PREFIX.0.0/16 (localhost stays per workspace)"
  else
    echo "sandbox-pool-network: netns/aliases-off: each workspace keeps a private 127.0.0.1, agents bind localhost:<port> directly"
  fi
  exit 0
fi

I=0
while [ "$I" -lt "$MAX_AGENTS" ]; do
  START=$((PORT_BASE + I * BLOCK_SIZE))
  END=$((START + BLOCK_SIZE - 1))
  if [ "$MODE" = "netns" ] || [ "$ALIASES_ON" = "0" ]; then
    HOST="127.0.0.1"
  else
    HOST="${LOOPBACK_PREFIX}.0.$((I + 2))"
  fi
  if [ "$FORMAT" = "json" ]; then
    printf '{"workspace_index":%d,"mode":"%s","port_start":%d,"port_end":%d,"loopback_host":"%s"}\n' \
      "$I" "$MODE" "$START" "$END" "$HOST"
  else
    echo "workspace=$I mode=$MODE ports=$START-$END localhost=$HOST"
  fi
  I=$((I + 1))
done
