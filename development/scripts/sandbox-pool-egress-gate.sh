#!/usr/bin/env bash
# ci#613 - pool-level DNS/egress readiness gate for pooled backing sandboxes.
#
# Follow-up of the ci#546 shared-sandbox networking design. A pooled sandbox is
# provisioned once and then accepts leases from many chats, so its DNS/egress
# has to be verified *before* it accepts leases. This script runs INSIDE the
# pool pod (the only place whose DNS and egress behaviour matters) and probes:
#
#   * cluster DNS resolution (CoreDNS of the namespace);
#   * TCP egress to the GitHub endpoints the credential flow of ci#497 and `gh`
#     need (github.com, api.github.com, uploads.github.com).
#
# It prints a JSON report to stdout (always, also on failure) that the pool
# start path / operator can feed back into the backend gate via the ingestion
# endpoint, and exits non-zero when the pool is not egress-ready, so a pool with
# broken egress is never handed out.
#
# The probe is cheap: it runs at pool start and on demand, nothing is installed
# or downloaded, and no credential, token or kubeconfig is ever read or printed
# (only endpoint names, resolved addresses and error texts).
#
# Usage:
#   sandbox-pool-egress-gate.sh [--pool NAME] [--source NAME] [--print-only]
#
#   --pool NAME    label the report with the pool/sandbox name (default: $HOSTNAME)
#   --source NAME  where the probe ran (default: pool-pod)
#   --print-only   do not exit non-zero on failure (report is still printed)
#
# Environment:
#   SANDBOX_POOL_EGRESS_TARGETS   comma-separated "dns:<name>" / "tcp:<host>:<port>"
#                                 overrides the probed endpoints
#   SANDBOX_POOL_EGRESS_TIMEOUT_MS  per-probe timeout in milliseconds (default 5000)
#
# Exit codes: 0 = pool is egress-ready, 1 = pool failed the gate,
#             2 = usage error.
set -uo pipefail

POOL="${HOSTNAME:-pool}"
SOURCE="pool-pod"
PRINT_ONLY="0"
TIMEOUT_MS="${SANDBOX_POOL_EGRESS_TIMEOUT_MS:-5000}"

# The endpoints a pooled sandbox must reach to behave exactly like a dedicated
# pod: cluster DNS plus the GitHub endpoints of the credential flow (ci#497) and
# `gh`.
DEFAULT_TARGETS="dns:kubernetes.default.svc.cluster.local,tcp:github.com:443,tcp:api.github.com:443,tcp:uploads.github.com:443"
TARGETS="${SANDBOX_POOL_EGRESS_TARGETS:-$DEFAULT_TARGETS}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --pool)
      POOL="${2:-}"
      shift 2
      ;;
    --source)
      SOURCE="${2:-}"
      shift 2
      ;;
    --print-only)
      PRINT_ONLY="1"
      shift
      ;;
    -h|--help)
      sed -n '2,34p' "$0"
      exit 0
      ;;
    *)
      printf 'sandbox-pool-egress-gate: unknown argument %s\n' "$1" >&2
      exit 2
      ;;
  esac
done

timeout_seconds() {
  # bash arithmetic is integer-only; keep sub-second precision out of scope.
  awk -v ms="$TIMEOUT_MS" 'BEGIN { printf "%.3f", (ms <= 0 ? 5 : ms) / 1000 }'
}

json_escape() {
  # Minimal JSON string escaping for the values this script emits (endpoint
  # names, resolved addresses, error texts). No credential material is emitted.
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr -d '\000-\010\013\014\016-\037'
}

now_ms() {
  # Millisecond timestamps where the platform supports them, seconds*1000
  # otherwise (BusyBox date prints a literal %3N, hence the numeric guard).
  local value
  value="$(date +%s%3N 2>/dev/null)"
  case "$value" in
    "" | *[!0-9]*) value="$(date +%s 2>/dev/null)000" ;;
  esac
  printf '%s' "$value"
}

checked_at() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

PER_TIMEOUT="$(timeout_seconds)"
report_entries=""
failure_count=0
target_count=0
started_ms="$(now_ms)"

dns_resolve() {
  local host="$1"
  local raw=""
  if command -v getent >/dev/null 2>&1; then
    raw="$(getent ahosts "$host" 2>/dev/null | awk '{print $1}' | sort -u)"
  elif command -v nslookup >/dev/null 2>&1; then
    raw="$(nslookup "$host" 2>/dev/null | awk '/^Address: /{print $2}' | sort -u)"
  else
    # Last resort: a Python resolver, still without touching any credential.
    raw="$(python3 - "$host" <<'PY' 2>/dev/null
import socket, sys
try:
    infos = socket.getaddrinfo(sys.argv[1], None)
except OSError:
    sys.exit(1)
print("\n".join(sorted({i[4][0] for i in infos})))
PY
)"
  fi
  # Comma-separated, as a single line (the report is JSON on one line).
  printf '%s' "$raw" | awk 'NF' | paste -sd, -
}

dial_tcp() {
  local host="$1"
  local port="$2"
  local timeout="$3"
  if command -v timeout >/dev/null 2>&1; then
    timeout "$timeout" bash -c "exec 3<>/dev/tcp/$host/$port" 2>/dev/null && return 0
    return 1
  fi
  python3 - "$host" "$port" "$timeout" <<'PY' 2>/dev/null
import socket, sys
host, port, timeout = sys.argv[1], int(sys.argv[2]), float(sys.argv[3])
sock = None
try:
    sock = socket.create_connection((host, port), timeout=timeout)
except OSError:
    sys.exit(1)
finally:
    if sock is not None:
        sock.close()
PY
}

append_entry() {
  local name="$1" kind="$2" target="$3" ok="$4" duration="$5" resolved="$6" err="$7" remediation="$8"
  target_count=$((target_count + 1))
  local entry
  entry="$(printf '{"name":"%s","kind":"%s","target":"%s","ok":%s,"duration_ms":%s' \
    "$(json_escape "$name")" "$(json_escape "$kind")" "$(json_escape "$target")" \
    "$ok" "$duration")"
  if [ -n "$resolved" ] && [ "$ok" = "true" ]; then
    entry="$entry,\"resolved\":[\"$(printf '%s' "$resolved" | sed 's/,/","/g')\"]"
  fi
  if [ -n "$err" ]; then
    entry="$entry,\"error\":\"$(json_escape "$err")\""
  fi
  if [ -n "$remediation" ]; then
    entry="$entry,\"remediation\":\"$(json_escape "$remediation")\""
  fi
  entry="$entry}"
  if [ -z "$report_entries" ]; then
    report_entries="$entry"
  else
    report_entries="$report_entries,$entry"
  fi
}

IFS=',' read -r -a target_list <<<"$TARGETS"
for raw in "${target_list[@]}"; do
  raw="$(printf '%s' "$raw" | tr -d '[:space:]')"
  [ -n "$raw" ] || continue
  kind="${raw%%:*}"
  rest="${raw#*:}"
  [ -n "$rest" ] || continue

  probe_started="$(now_ms)"
  case "$kind" in
    dns)
      resolved="$(dns_resolve "$rest")"
      if [ -n "$resolved" ]; then
        duration=$(( $(now_ms) - probe_started ))
        append_entry "$rest" "dns" "dns:$rest" "true" "$duration" "$resolved" "" ""
      else
        duration=$(( $(now_ms) - probe_started ))
        failure_count=$((failure_count + 1))
        append_entry "$rest" "dns" "dns:$rest" "false" "$duration" "" \
          "cannot resolve $rest from inside the pool pod" \
          "check the CoreDNS service and the pod/namespace egress DNS rules (ci#546), then recycle the pool"
      fi
      ;;
    tcp)
      host="$rest"
      port="443"
      case "$rest" in
        *:*)
          host="${rest%:*}"
          port="${rest##*:}"
          ;;
      esac
      if dial_tcp "$host" "$port" "$PER_TIMEOUT"; then
        duration=$(( $(now_ms) - probe_started ))
        append_entry "$host" "tcp" "tcp:$host:$port" "true" "$duration" "" "" ""
      else
        duration=$(( $(now_ms) - probe_started ))
        failure_count=$((failure_count + 1))
        append_entry "$host" "tcp" "tcp:$host:$port" "false" "$duration" "" \
          "no TCP egress to $host:$port from inside the pool pod" \
          "check the NAT/firewall rules of the pool namespace and the credential endpoints of ci#497 (which \`gh\` needs), then recycle the pool"
      fi
      ;;
    *)
      printf 'sandbox-pool-egress-gate: ignoring unknown target kind %s\n' "$kind" >&2
      ;;
  esac
done

total_ms=$(( $(now_ms) - started_ms ))
if [ "$failure_count" -eq 0 ] && [ "$target_count" -gt 0 ]; then
  healthy="true"
else
  healthy="false"
fi

report="$(printf '{"pool":"%s","source":"%s","checked_at":"%s","duration_ms":%s,"targets":[%s]}' \
  "$(json_escape "$POOL")" "$(json_escape "$SOURCE")" "$(checked_at)" "$total_ms" "$report_entries")"

printf '%s\n' "$report"

if [ "$healthy" != "true" ]; then
  printf 'sandbox-pool-egress-gate: pool %s is NOT egress-ready (%s of %s probes failed)\n' \
    "$POOL" "$failure_count" "$target_count" >&2
  if [ "$PRINT_ONLY" = "1" ]; then
    exit 0
  fi
  exit 1
fi

printf 'sandbox-pool-egress-gate: pool %s is egress-ready (%s probes passed in %sms)\n' \
  "$POOL" "$target_count" "$total_ms" >&2
exit 0
