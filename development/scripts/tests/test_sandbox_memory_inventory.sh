#!/usr/bin/env bash
# test_sandbox_memory_inventory.sh — cluster-free test for ci#662's measurement
# step (development/scripts/sandbox-memory-inventory.sh).
#
# The inventory only reads the cluster, so a stub `kubectl` on PATH is enough to
# pin the arithmetic (totals, age buckets, sandboxes-that-fit, agents-per-pool)
# without a cluster: the numbers the memory budget is derived from must be
# reproducible in CI, and a regression in them must fail a test, not a
# production scheduler.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="${here}/../sandbox-memory-inventory.sh"
[[ -x "$script" ]] || { echo "missing inventory script: $script" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# stub kubectl answering only the read-only calls the inventory makes. Four
# sandbox pods: one fresh, one idle (1h > 45m TTL), one orphan (2d > 24h), one
# failed-but-young. Requests 2Gi / limits 8Gi, 250m CPU each.
cat >"$work/kubectl" <<'STUB'
#!/usr/bin/env bash
args="$*"
case "$args" in
  *"get pods -A"*) printf 'node-a\t2Gi\nnode-a\t2Gi\nnode-a\t2Gi\nnode-a\t1Gi\nnode-b\t2Gi\n' ;;
  *"get nodes"*)   printf 'node-a\t16Gi\t16Gi\nnode-b\t32Gi\t32Gi\n' ;;
  *"get pods"*"-o jsonpath"*)
    printf 'pod-live\t2Gi\t8Gi\t250m\t250m\tnode-a\n'
    printf 'pod-idle\t2Gi\t8Gi\t250m\t250m\tnode-a\n'
    printf 'pod-orphan\t2Gi\t8Gi\t250m\t250m\tnode-a\n'
    printf 'pod-stopped\t2Gi\t8Gi\t250m\t250m\tnode-b\n'
    ;;
  *"get pods"*)
    printf 'NAME READY STATUS RESTARTS AGE\n'
    printf 'pod-live 1/1 Running 0 5m\n'
    printf 'pod-idle 1/1 Running 0 1h\n'
    printf 'pod-orphan 1/1 Running 0 2d\n'
    printf 'pod-stopped 0/1 Error 0 12m\n'
    ;;
  *"top nodes"*) exit 1 ;;
  *"top pods"*) exit 1 ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$work/kubectl"

out="$(KUBECTL="$work/kubectl" SANDBOX_NAMESPACE=open-chat-sandboxes bash "$script")"

fail=0
expect() {
  if ! grep -qF "$1" <<<"$out"; then
    echo "FAIL: expected line containing: $1" >&2
    fail=1
  fi
}

# inventory + age buckets
expect "sandbox_memory namespace=open-chat-sandboxes pods_measured=4 pod_selector=<all>"
expect "sandbox_memory running=3 stopped=1 live=2 idle_gt_2700s=1 orphan_gt_86400s=1 unknown_age=0"
expect "sandbox_memory total_cpu_request_millicores=1000 total_cpu_limit_millicores=1000"
expect "sandbox_memory total_request=8Gi total_limit=32Gi"
expect "sandbox_memory per_sandbox_request=2Gi per_sandbox_limit=8Gi"
# node headroom: smallest allocatable 16Gi, 10% reserve, 2Gi per sandbox -> 7 fit,
# last one left out as headroom -> 6 effective agents per pool.
expect "sandbox_memory node=node-a capacity=16Gi allocatable=16Gi requested_by_all_pods=7Gi"
expect "sandbox_memory smallest_node=node-a smallest_allocatable=16Gi"
expect "sandbox_memory sandboxes_that_fit=7 agents_per_pool_configured=1 effective_agents_per_pool=6"
expect "sandbox_memory source=measured"
expect "OCI_MSGMATE_CODER_SANDBOX_MEMORY_REQUEST=2Gi"
# credential lifecycle of the same pool (ci#614): reported, never a value
expect "sandbox_credentials scope=per_agent ttl_seconds=1800 refresh_ahead_seconds=300"
expect "sandbox_credentials reprovision_on_restart=true reaper_idle_ttl_seconds=2700"
expect "OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REPROVISION_ON_RESTART"
# a credential TTL that outlives the reaper idle TTL is called out, because a
# still-attached agent would then see an expired credential
cred_out="$(KUBECTL="$work/kubectl" SANDBOX_NAMESPACE=open-chat-sandboxes \
  OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_TTL_SECONDS=3600 bash "$script")"
grep -qF "sandbox_credentials note=credential_ttl_3600s_not_below_reaper_idle_ttl_2700s" <<<"$cred_out" || {
  echo "FAIL: a credential TTL above the reaper idle TTL must be reported" >&2
  fail=1
}
# the whole report must stay free of credential *values*
if grep -qiE 'gh[pousr]_[A-Za-z0-9]{16,}|token=[^ ]' <<<"$out$cred_out"; then
  echo "FAIL: the inventory report must never carry a credential value" >&2
  fail=1
fi
# no metrics server is reported as such, never fatal
expect "sandbox_memory top_node=unavailable"
expect "sandbox_memory top_pods=unavailable"

# an empty namespace must report "unmeasured" and keep the bound off
cat >"$work/kubectl-empty" <<'STUB'
#!/usr/bin/env bash
case "$*" in
  *"get nodes"*) printf 'node-a\t16Gi\t16Gi\n' ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$work/kubectl-empty"

empty_out="$(KUBECTL="$work/kubectl-empty" bash "$script")"
grep -qF "sandbox_memory source=unmeasured" <<<"$empty_out" || {
  echo "FAIL: an empty namespace must report source=unmeasured" >&2
  fail=1
}

# the report must also land in the CI job summary when pointed at one
summary="$work/summary.md"; : >"$summary"
KUBECTL="$work/kubectl" GITHUB_STEP_SUMMARY="$summary" bash "$script" >/dev/null
grep -qF "sandbox_memory sandboxes_that_fit=7" "$summary" || {
  echo "FAIL: report not appended to GITHUB_STEP_SUMMARY" >&2
  fail=1
}

if ((fail)); then exit 1; fi
echo "test_sandbox_memory_inventory: OK"