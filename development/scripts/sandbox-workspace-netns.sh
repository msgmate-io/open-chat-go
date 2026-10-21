#!/usr/bin/env bash
# ci#612 - per-workspace network namespace for the pooled sandbox path.
#
# Follow-up of the ci#546 shared-sandbox networking design (and of the ci#603
# per-agent OS identity / ci#613 pool egress gate). A pooled pod still shares ONE
# network namespace between its slots, so an agent can reach a sibling's
# listeners and `localhost` is pod-wide. This script gives one workspace its own
# namespace instead:
#
#   * `ip netns add` a namespace whose name is derived from the workspace uuid
#     (deterministic, so a provider can re-attach after a restart);
#   * start a slirp4netns sidecar that configures the tap and keeps outbound
#     egress + DNS, while disabling the *host* loopback so pod-local listeners of
#     a sibling stay unreachable;
#   * give the namespace its own resolv.conf (cluster DNS) so the ci#497
#     credential flow and `gh` resolve exactly like they do in the pod;
#   * print the exec prefix that joins the namespace, which the coder exec target
#     prepends while keeping its ci#603 per-agent UID/HOME;
#   * verify the boundary from *inside* the namespace: egress to the GitHub
#     endpoints works and a sibling's bound port does not.
#
# Nothing here reads or prints credential material: the report carries names,
# paths, argv and boolean probe results only.
#
# Usage:
#   sandbox-workspace-netns.sh up      --workspace UUID [--mtu N] [--print-only]
#   sandbox-workspace-netns.sh down    --workspace UUID [--print-only]
#   sandbox-workspace-netns.sh exec    --workspace UUID -- COMMAND [ARG...]
#   sandbox-workspace-netns.sh verify  --workspace UUID [--sibling-port PORT]
#                                      [--egress-host HOST] [--print-only]
#
#   --workspace UUID  the workspace (lease) the namespace belongs to (required)
#   --mtu N           tap MTU (default 65520)
#   --sibling-port P  a port bound by another slot in the same pod; the boundary
#                     is only healthy when it is NOT reachable from the namespace
#   --egress-host H   an endpoint that must be reachable (repeatable, default:
#                     github.com api.github.com) - resolved *and* dialed on 443
#   --print-only      never exit non-zero (the report is still printed)
#   --dry-run         print the plan/commands without touching the host; also
#                     enabled by SANDBOX_WORKSPACE_NETNS_DRY_RUN=1
#
# Environment:
#   SANDBOX_WORKSPACE_NETNS_PREFIX     namespace name prefix (default wsc-)
#   SANDBOX_WORKSPACE_NETNS_MTU        tap MTU (default 65520)
#   SANDBOX_WORKSPACE_NETNS_NETNS_BIN  `ip` binary (default ip)
#   SANDBOX_WORKSPACE_NETNS_SLIRP_BIN  sidecar binary (default slirp4netns)
#   SANDBOX_WORKSPACE_NETNS_RESOLV_DIR per-namespace resolv.conf dir (/etc/netns)
#
# Exit codes: 0 = ok, 1 = the boundary/egress gate failed, 2 = usage error.
set -uo pipefail

PROG="sandbox-workspace-netns.sh"
ACTION=""
WORKSPACE=""
MTU="${SANDBOX_WORKSPACE_NETNS_MTU:-65520}"
PREFIX="${SANDBOX_WORKSPACE_NETNS_PREFIX:-wsc-}"
NETNS_BIN="${SANDBOX_WORKSPACE_NETNS_NETNS_BIN:-ip}"
SLIRP_BIN="${SANDBOX_WORKSPACE_NETNS_SLIRP_BIN:-slirp4netns}"
RESOLV_DIR="${SANDBOX_WORKSPACE_NETNS_RESOLV_DIR:-/etc/netns}"
READY_FD=3
SIBLING_PORT=""
PRINT_ONLY="0"
DRY_RUN="${SANDBOX_WORKSPACE_NETNS_DRY_RUN:-0}"
EGRESS_HOSTS=""
COMMAND=()

usage() {
  sed -n '2,52p' "$0"
}

die_usage() {
  printf '%s: %s\n' "$PROG" "$1" >&2
  exit 2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    up | down | exec | verify)
      [ -z "$ACTION" ] || die_usage "only one action may be given"
      ACTION="$1"
      shift
      ;;
    --workspace)
      WORKSPACE="${2:-}"
      shift 2 || die_usage "--workspace needs a value"
      ;;
    --workspace=*)
      WORKSPACE="${1#*=}"
      shift
      ;;
    --mtu)
      MTU="${2:-}"
      shift 2 || die_usage "--mtu needs a value"
      ;;
    --sibling-port)
      SIBLING_PORT="${2:-}"
      shift 2 || die_usage "--sibling-port needs a value"
      ;;
    --egress-host)
      EGRESS_HOSTS="${EGRESS_HOSTS},${2:-}"
      shift 2 || die_usage "--egress-host needs a value"
      ;;
    --print-only)
      PRINT_ONLY="1"
      shift
      ;;
    --dry-run)
      DRY_RUN="1"
      shift
      ;;
    --)
      shift
      COMMAND=("$@")
      set --
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die_usage "unknown argument $1"
      ;;
  esac
done

[ -n "$ACTION" ] || die_usage "an action is required (up|down|exec|verify)"
[ -n "$WORKSPACE" ] || die_usage "--workspace is required"

# --- helpers -----------------------------------------------------------------

json_escape() {
  # Minimal JSON string escaping; only names, paths and argv pass through here.
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr -d '\000-\010\013\014\016-\037'
}

# Derive the namespace name from the workspace uuid: deterministic (re-attach)
# and short (derived interface names must stay inside IFNAMSIZ).
namespace_name() {
  local digest
  if command -v sha256sum >/dev/null 2>&1; then
    digest="$(printf '%s' "$WORKSPACE" | sha256sum | awk '{print $1}')"
  elif command -v sha1sum >/dev/null 2>&1; then
    digest="$(printf '%s' "$WORKSPACE" | sha1sum | awk '{print $1}')"
  else
    # Last resort: a checksum of the uuid, still deterministic per workspace.
    digest="$(printf '%s' "$WORKSPACE" | cksum | awk '{print $1}')0000000000000"
  fi
  printf '%s%s' "$PREFIX" "$(printf '%s' "$digest" | cut -c1-12)"
}

resolv_conf_path() {
  printf '%s/%s/resolv.conf' "$RESOLV_DIR" "$1"
}

# The sidecar argv: configure the tap, keep DNS/egress, disable the *host*
# loopback (so a sibling's pod-local listener is unreachable), report readiness.
sidecar_argv() {
  local pid="$1"
  printf '%s --configure --mtu=%s --disable-host-loopback --enable-sandbox --ready-fd=%s %s' \
    "$SLIRP_BIN" "$MTU" "$READY_FD" "$pid"
}

exec_prefix() {
  printf '%s netns exec %s --' "$NETNS_BIN" "$1"
}

run() {
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY-RUN %s\n' "$*" >&2
    return 0
  fi
  "$@"
}

# probe_prefix renders the prefix used to run a probe from *inside* the workspace
# namespace: isolation claims measured from the pod netns are worthless.
# SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX overrides it entirely (used by the tests
# and by a provider that already runs inside an isolated context).
probe_prefix() {
  if [ -n "${SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX:-}" ]; then
    printf '%s\n' "$SANDBOX_WORKSPACE_NETNS_PROBE_PREFIX"
    return
  fi
  if [ "$DRY_RUN" = "1" ]; then
    # Never touch the host in dry-run mode; callers treat the empty prefix as
    # "probe skipped".
    printf '\n'
    return
  fi
  printf '%s netns exec %s --\n' "$NETNS_BIN" "$NS"
}

dial_tcp() {
  local host="$1" port="$2" timeout="${SANDBOX_WORKSPACE_NETNS_PROBE_TIMEOUT:-3}" prefix
  prefix="$(probe_prefix)"
  if [ -z "$prefix" ]; then
    return 1
  fi
  read -r -a prefix_argv <<<"$prefix"
  if command -v timeout >/dev/null 2>&1; then
    "${prefix_argv[@]}" timeout "$timeout" bash -c "exec 3<>/dev/tcp/$host/$port" >/dev/null 2>&1 && return 0
    return 1
  fi
  "${prefix_argv[@]}" python3 - "$host" "$port" "$timeout" <<'PY' >/dev/null 2>&1
import socket, sys
host, port, timeout = sys.argv[1], int(sys.argv[2]), float(sys.argv[3])
sock = None
try:
    sock = socket.create_connection((host, port), timeout=timeout)
finally:
    if sock is not None:
        sock.close()
PY
}

# --- actions -----------------------------------------------------------------

if [ "$ACTION" = "up" ]; then
  NS="$(namespace_name)"
  RESOLV="$(resolv_conf_path "$NS")"
  # `ip netns add` creates the bind mount whose pid-1 anchor the sidecar attaches
  # to; re-running `up` must be a no-op so a provider can re-attach.
  run "$NETNS_BIN" netns add "$NS" 2>/dev/null || true
  if [ "$DRY_RUN" = "0" ]; then
    mkdir -p "$(dirname "$RESOLV")" 2>/dev/null || true
    # Carry the pod's resolver into the namespace: the ci#497 credential flow and
    # `gh` must resolve exactly like they do in the pod.
    if [ ! -s "$RESOLV" ]; then
      cp /etc/resolv.conf "$RESOLV" 2>/dev/null || true
    fi
    # The namespace's loopback is private to it; bring it up.
    "$NETNS_BIN" netns exec "$NS" "$NETNS_BIN" link set lo up 2>/dev/null || true
    # The anchor process of `ip netns` is its child; slirp joins that pid.
    anchor_pid="$(pgrep -f "ip netns exec $NS" 2>/dev/null | head -1 || true)"
    if [ -n "$anchor_pid" ]; then
      # Detached: the sidecar must outlive this script.
      sh -c "$(sidecar_argv "$anchor_pid")" >/dev/null 2>&1 &
    fi
  fi
  printf '{"action":"up","workspace":"%s","namespace":"%s","mtu":%s,"sidecar":"%s","ready_fd":%s,"exec_prefix":"%s","resolv_conf":"%s","shared_pod_netns":false}\n' \
    "$(json_escape "$WORKSPACE")" "$(json_escape "$NS")" "$MTU" \
    "$(json_escape "$SLIRP_BIN")" "$READY_FD" "$(json_escape "$(exec_prefix "$NS")")" \
    "$(json_escape "$RESOLV")"
  exit 0
fi

if [ "$ACTION" = "down" ]; then
  NS="$(namespace_name)"
  run "$NETNS_BIN" netns delete "$NS" 2>/dev/null || true
  printf '{"action":"down","workspace":"%s","namespace":"%s","ok":true}\n' \
    "$(json_escape "$WORKSPACE")" "$(json_escape "$NS")"
  exit 0
fi

if [ "$ACTION" = "exec" ]; then
  [ "${#COMMAND[@]}" -gt 0 ] || die_usage "exec needs a command after --"
  NS="$(namespace_name)"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY-RUN %s %s\n' "$(exec_prefix "$NS")" "$*" >&2
    exit 0
  fi
  exec "$NETNS_BIN" netns exec "$NS" -- "${COMMAND[@]}"
fi

# verify: the acceptance criteria of ci#612, probed from inside the namespace.
NS="$(namespace_name)"
if [ -z "$EGRESS_HOSTS" ]; then
  EGRESS_HOSTS=",github.com,api.github.com"
fi

entries=""
failures=0
egress_ok="true"
sibling_ok="true"

for host in $(printf '%s' "$EGRESS_HOSTS" | tr ',' ' '); do
  [ -n "$host" ] || continue
  if [ "$DRY_RUN" = "1" ]; then
    ok="true"
  elif dial_tcp "$host" 443; then
    ok="true"
  else
    ok="false"
    failures=$((failures + 1))
    egress_ok="false"
  fi
  entry="$(printf '{"kind":"egress","host":"%s","port":443,"ok":%s' "$(json_escape "$host")" "$ok")"
  if [ "$ok" = "false" ]; then
    entry="$entry,\"remediation\":\"$(json_escape "no TCP egress to $host:443 from inside ${NS}: check the slirp sidecar (--configure/--enable-sandbox) and the pool namespace NAT rules (ci#546), then recycle the workspace namespace")\""
  fi
  entries="${entries:+$entries,}$entry}"
done

if [ -n "$SIBLING_PORT" ]; then
  # The whole point of the per-workspace namespace: a sibling's bound port must
  # NOT be reachable. A reachable sibling means the workspace is still in the pod
  # netns, which is a refusal, not a warning.
  if [ "$DRY_RUN" = "1" ]; then
    reachable="false"
  elif dial_tcp 127.0.0.1 "$SIBLING_PORT"; then
    reachable="true"
  else
    reachable="false"
  fi
  if [ "$reachable" = "true" ]; then
    sibling_ok="false"
    failures=$((failures + 1))
  fi
  entries="$entries$(printf '%s' "${entries:+,}")$(printf '{"kind":"sibling-port","port":%s,"reachable":%s,"ok":%s' \
    "$SIBLING_PORT" "$reachable" "$([ "$reachable" = "true" ] && printf false || printf true)")"
  if [ "$reachable" = "true" ]; then
    entries="$entries,\"remediation\":\"$(json_escape "a sibling slot's port ${SIBLING_PORT} is reachable from ${NS}: the workspace is still in the pod network namespace, refusing to hand it out")\""
  fi
  entries="$entries}"
fi

ok="true"
[ "$failures" -eq 0 ] || ok="false"
printf '{"action":"verify","workspace":"%s","namespace":"%s","ok":%s,"egress_ok":%s,"sibling_isolated":%s,"checks":[%s]}\n' \
  "$(json_escape "$WORKSPACE")" "$(json_escape "$NS")" "$ok" "$egress_ok" "$sibling_ok" "$entries"

if [ "$ok" = "false" ] && [ "$PRINT_ONLY" != "1" ]; then
  exit 1
fi
exit 0
