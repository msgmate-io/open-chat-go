#!/usr/bin/env bash
# Unit test for development/scripts/sandbox-pool-network-preflight.sh (ci#546).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/../sandbox-pool-network-preflight.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
checks=0
fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { checks=$((checks + 1)); }

"$script" > "$tmp/out" || fail "default run should succeed"
ok
head -1 "$tmp/out" | grep -q 'ports=20000-20063' || fail "first block should be 20000-20063: $(head -1 "$tmp/out")"
ok
head -1 "$tmp/out" | grep -q 'localhost=127.64.0.2' || fail "first alias should be 127.64.0.2"
ok
[ "$(wc -l < "$tmp/out")" -eq 8 ] || fail "default max-agents is 8"
ok
tail -1 "$tmp/out" | grep -q 'ports=20448-20511' || fail "8th block should end at 20511"
ok
awk '{print $3}' "$tmp/out" | sort -n | uniq -d | grep -q . && fail "blocks must not overlap"
ok

"$script" --json > "$tmp/json" || fail "--json should succeed"
grep -q '"port_start":20000,"port_end":20063,"loopback_host":"127.64.0.2"' "$tmp/json" || fail "json shape"
ok

"$script" --check > "$tmp/check" || fail "--check should succeed"
grep -q '^sandbox-pool-network: ok ' "$tmp/check" || fail "--check verdict line"
ok
grep -Eqi 'password|token|secret|api[_-]?key|kubeconfig' "$tmp/check" "$tmp/out" && fail "output must carry no credential material"
ok

"$script" --mode netns > "$tmp/netns" || fail "netns mode should succeed"
grep -q 'localhost=127.0.0.1' "$tmp/netns" || fail "netns workspace keeps 127.0.0.1"
ok

refuse() {
  local want="$1"; shift
  if "$script" "$@" > "$tmp/refuse" 2>&1; then fail "expected refusal for: $*"; fi
  grep -qi "$want" "$tmp/refuse" || fail "refusal message for '$*' should mention '$want': $(cat "$tmp/refuse")"
  ok
}
refuse privileged --port-base 80
refuse minimum --block-size 8
refuse "at least 1" --max-agents 0
refuse "unknown network mode" --mode host
refuse room --port-base 65500
refuse overflow --port-base 20000 --max-agents 900
refuse "two-octet" --loopback-prefix 127.64.1
refuse "unknown argument" --bogus

"$script" --aliases 0 > "$tmp/noalias" || fail "--aliases 0 should succeed"
grep -q 'localhost=127.0.0.1' "$tmp/noalias" || fail "aliases off keeps 127.0.0.1"
ok

echo "test_sandbox_pool_network_preflight.sh: $checks checks passed"
