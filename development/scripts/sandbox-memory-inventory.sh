#!/usr/bin/env bash
# sandbox-memory-inventory.sh — ci#662 measurement step for the sandbox pool.
#
# Prints the numbers the memory budget of the sandbox pool is derived from, so
# they land in the CI record *before* anybody changes a limit:
#
#   * total CPU requests/limits and total memory requests/limits of the sandbox
#     pods in the namespace, plus the largest per-sandbox request/limit,
#   * the pod count by age bucket (live / idle > IDLE_TTL_SECONDS / orphan
#     > ORPHAN_TTL_SECONDS) and running vs stopped,
#   * every node's capacity/allocatable memory, the memory already requested on
#     it and how many extra per-sandbox requests still fit on the smallest node,
#   * the resulting "agents per pool" suggestion including the headroom sandbox
#     and the 10% default reserve used by `ResolveSandboxMemoryBudget`
#     (backend/api/msgmate/sandbox_memory_budget.go),
#   * the credential lifecycle of the same pool (ci#614): the effective scope
#     (per-agent, the ci#546 rule), whether a pool pod restart replays
#     provisioning for every live workspace bound to the pool, and the credential
#     TTL relative to the reaper idle TTL — reported as
#     `sandbox_credentials key=value`, never a credential value,
#   * best-effort `kubectl top nodes` / `top pods` readings when a metrics
#     server is installed (their absence is reported, never fatal).
#
# The output is deliberately shell/easily-greppable (`sandbox_memory key=value`
# lines) so a workflow can append it to the job summary and an operator can
# paste the measured values into the deployment env:
#
#   OCI_MSGMATE_CODER_SANDBOX_MEMORY_REQUEST
#   OCI_MSGMATE_CODER_SANDBOX_MEMORY_LIMIT
#   OCI_MSGMATE_CODER_NODE_ALLOCATABLE_MEMORY
#   OCI_MSGMATE_CODER_SANDBOX_MEMORY_RESERVE
#
# It only *reads* the cluster: no pod, node or limit is modified. Requires
# kubectl (override with KUBECTL) plus a KUBECONFIG that can list pods in
# SANDBOX_NAMESPACE and, ideally, nodes.
#
# Usage:
#   SANDBOX_NAMESPACE=open-chat-sandboxes development/scripts/sandbox-memory-inventory.sh
#
# Environment:
#   SANDBOX_NAMESPACE    namespace holding the per-chat sandbox pods (default
#                        open-chat-sandboxes)
#   POD_SELECTOR         label selector of the sandbox pods (default: all pods
#                        in the namespace)
#   AGENTS_PER_POOL      the ci#602 configured bound, echoed for the report
#                        (default 1: one sandbox per chat)
#   IDLE_TTL_SECONDS     age above which a live pod counts as idle (default
#                        2700 = 45 min, the ci#662 reaper idle TTL)
#   ORPHAN_TTL_SECONDS   age above which a pod counts as an orphan (default
#                        86400 = 24 h, the reaper age gate)
#   SANDBOX_MEMORY_RESERVE  explicit reserve (a Kubernetes quantity)
#   KUBECTL              kubectl binary to use (default kubectl; the cluster-free
#                        test passes a stub here)
#   GITHUB_STEP_SUMMARY  if set, the report is also appended there
set -euo pipefail

SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-open-chat-sandboxes}"
POD_SELECTOR="${POD_SELECTOR:-}"
AGENTS_PER_POOL="${AGENTS_PER_POOL:-1}"
IDLE_TTL_SECONDS="${IDLE_TTL_SECONDS:-2700}"
ORPHAN_TTL_SECONDS="${ORPHAN_TTL_SECONDS:-86400}"
KUBECTL="${KUBECTL:-kubectl}"

command -v "$KUBECTL" >/dev/null 2>&1 || {
  echo "::error::kubectl is required to measure the sandbox pool memory budget." >&2
  exit 1
}

# to_bytes converts a Kubernetes memory quantity ("2Gi", "1536Mi", "3221225472")
# to an integer number of bytes using the same binary multipliers as
# ParseMemoryQuantity in the backend.
to_bytes() {
  local raw="${1// /}"
  [[ -n "$raw" ]] || { echo 0; return; }
  awk -v v="$raw" '
    BEGIN {
      mult = 1
      suffix = substr(v, length(v), 1)
      if (suffix == "B") { v = substr(v, 1, length(v) - 1); suffix = substr(v, length(v), 1) }
      if (suffix == "i") { v = substr(v, 1, length(v) - 1); suffix = substr(v, length(v), 1) }
      if (suffix ~ /[0-9]/) { mult = 1 }
      else if (suffix == "K" || suffix == "k") { mult = 1024 }
      else if (suffix == "M") { mult = 1024 * 1024 }
      else if (suffix == "G") { mult = 1024 * 1024 * 1024 }
      else if (suffix == "T") { mult = 1024 * 1024 * 1024 * 1024 }
      else if (suffix == "P") { mult = 1024 * 1024 * 1024 * 1024 * 1024 }
      else if (suffix == "E") { mult = 1024 * 1024 * 1024 * 1024 * 1024 * 1024 }
      else { v = substr(v, 1, length(v) - 1) }
      if (mult != 1) { v = substr(v, 1, length(v) - 1) }
      printf "%.0f", v * mult
    }'
}

# cpu_millis converts a Kubernetes CPU quantity ("250m", "2", "0.5") to
# millicores; CPU is only reported, never used for the memory bound.
cpu_millis() {
  local raw="${1// /}"
  [[ -n "$raw" ]] || { echo 0; return; }
  awk -v v="$raw" '
    BEGIN {
      if (v ~ /m$/) { printf "%d", substr(v, 1, length(v) - 1) }
      else { printf "%d", v * 1000 }
    }'
}

format_bytes() {
  local bytes="$1"
  awk -v b="$bytes" 'BEGIN {
    if (b <= 0) { print "unset"; exit }
    if (b % (1024*1024*1024) == 0) { printf "%dGi\n", b / (1024*1024*1024); exit }
    if (b % (1024*1024) == 0) { printf "%dMi\n", b / (1024*1024); exit }
    if (b % 1024 == 0) { printf "%dKi\n", b / 1024; exit }
    printf "%.0f\n", b
  }'
}

# age_seconds turns kubectl's humanized AGE column ("45s", "12m", "3h5m", "4d",
# "2d7h") into seconds, so the age buckets need no date parsing. An empty or
# unrecognised age yields -1 ("unknown"), which is counted separately instead of
# silently landing in the live bucket.
age_seconds() {
  awk -v a="${1:-}" 'BEGIN {
    if (a == "" || a == "<invalid>") { print -1; exit }
    total = 0
    while (match(a, /[0-9]+[smhdw]/)) {
      tok = substr(a, RSTART, RLENGTH)
      n = substr(tok, 1, length(tok) - 1) + 0
      u = substr(tok, length(tok), 1)
      if (u == "s") total += n
      else if (u == "m") total += n * 60
      else if (u == "h") total += n * 3600
      else if (u == "d") total += n * 86400
      else if (u == "w") total += n * 604800
      a = substr(a, RSTART + RLENGTH)
    }
    if (total == 0) { print -1 } else { print total }
  }'
}

report() {
  printf '%s\n' "$1"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf '%s\n' "$1" >>"$GITHUB_STEP_SUMMARY"
  fi
}

# --- 1. per-sandbox request/limit and the namespace totals ------------------
# The resource numbers come from jsonpath (exact quantities), the phase/age from
# the humanized table (no date math, works with any kubectl).
pod_res_json="$("$KUBECTL" -n "$SANDBOX_NAMESPACE" get pods ${POD_SELECTOR:+-l "$POD_SELECTOR"} \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].resources.requests.memory}{"\t"}{.spec.containers[0].resources.limits.memory}{"\t"}{.spec.containers[0].resources.requests.cpu}{"\t"}{.spec.containers[0].resources.limits.cpu}{"\t"}{.spec.nodeName}{"\n"}{end}' 2>/dev/null || true)"
pod_table="$("$KUBECTL" -n "$SANDBOX_NAMESPACE" get pods ${POD_SELECTOR:+-l "$POD_SELECTOR"} --no-headers 2>/dev/null || true)"

declare -A pod_req_mem=() pod_lim_mem=() pod_req_cpu=() pod_lim_cpu=() pod_node=()
while IFS=$'\t' read -r name rmem lmem rcpu lcpu node; do
  [[ -n "${name:-}" ]] || continue
  pod_req_mem["$name"]="${rmem:-}"
  pod_lim_mem["$name"]="${lmem:-}"
  pod_req_cpu["$name"]="${rcpu:-}"
  pod_lim_cpu["$name"]="${lcpu:-}"
  pod_node["$name"]="${node:-}"
done <<<"$pod_res_json"

pod_count=0
request_bytes=0
limit_bytes=0
total_request_bytes=0
total_limit_bytes=0
total_cpu_request_millis=0
total_cpu_limit_millis=0
running=0
stopped=0
live=0
idle=0
orphan=0
unknown_age=0

# The pod list is the source of truth for the count (it comes from jsonpath, so
# it cannot pick up the table header); the table only supplies phase and age.
for name in "${!pod_req_mem[@]}"; do
  [[ -n "${name:-}" ]] || continue
  pod_count=$((pod_count + 1))
  status=""
  age=""
  while read -r t_name _t_ready t_status _t_restarts t_age _t_rest; do
    [[ "$t_name" == "NAME" ]] && continue
    if [[ "$t_name" == "$name" ]]; then
      status="$t_status"
      age="$t_age"
      break
    fi
  done <<<"$pod_table"

  rmem="${pod_req_mem[$name]:-}"
  lmem="${pod_lim_mem[$name]:-}"
  rcpu="${pod_req_cpu[$name]:-}"
  lcpu="${pod_lim_cpu[$name]:-}"

  if [[ -n "$rmem" ]]; then
    bytes="$(to_bytes "$rmem")"
    total_request_bytes=$((total_request_bytes + bytes))
    if ((bytes > request_bytes)); then request_bytes="$bytes"; fi
  fi
  if [[ -n "$lmem" ]]; then
    bytes="$(to_bytes "$lmem")"
    total_limit_bytes=$((total_limit_bytes + bytes))
    if ((bytes > limit_bytes)); then limit_bytes="$bytes"; fi
  fi
  if [[ -n "$rcpu" ]]; then total_cpu_request_millis=$((total_cpu_request_millis + $(cpu_millis "$rcpu"))); fi
  if [[ -n "$lcpu" ]]; then total_cpu_limit_millis=$((total_cpu_limit_millis + $(cpu_millis "$lcpu"))); fi

  if [[ "$status" == "Running" ]]; then running=$((running + 1)); else stopped=$((stopped + 1)); fi

  age_s="$(age_seconds "${age:-}")"
  if ((age_s < 0)); then
    unknown_age=$((unknown_age + 1))
  elif ((age_s > ORPHAN_TTL_SECONDS)); then
    orphan=$((orphan + 1))
  elif ((age_s > IDLE_TTL_SECONDS)); then
    idle=$((idle + 1))
  else
    live=$((live + 1))
  fi
done <<<"$pod_table"

report "sandbox_memory namespace=$SANDBOX_NAMESPACE pods_measured=$pod_count pod_selector=${POD_SELECTOR:-<all>}"
report "sandbox_memory running=$running stopped=$stopped live=$live idle_gt_${IDLE_TTL_SECONDS}s=$idle orphan_gt_${ORPHAN_TTL_SECONDS}s=$orphan unknown_age=$unknown_age"
report "sandbox_memory total_cpu_request_millicores=$total_cpu_request_millis total_cpu_limit_millicores=$total_cpu_limit_millis"
report "sandbox_memory total_request=$(format_bytes "$total_request_bytes") total_limit=$(format_bytes "$total_limit_bytes")"
report "sandbox_memory per_sandbox_request=$(format_bytes "$request_bytes") per_sandbox_limit=$(format_bytes "$limit_bytes")"

if ((request_bytes == 0)); then
  report "sandbox_memory note=no per-sandbox memory request visible (namespace empty, or the sandbox template sets no request): the memory bound stays OFF until a request is configured"
  report "sandbox_memory source=unmeasured"
  exit 0
fi

# --- 2. node allocatable/capacity memory and memory already requested --------
nodes_json="$("$KUBECTL" get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.memory}{"\t"}{.status.capacity.memory}{"\n"}{end}' 2>/dev/null || true)"

# Cluster-wide memory requests per node (best effort: needs cluster-wide pod
# list rights). This is what the scheduler actually counts, so it is the truthful
# "already requested" number next to the allocatable one.
declare -A node_requested=()
all_pods_json="$("$KUBECTL" get pods -A -o jsonpath='{range .items[*]}{.spec.nodeName}{"\t"}{.spec.containers[*].resources.requests.memory}{"\n"}{end}' 2>/dev/null || true)"
while IFS=$'\t' read -r node mems; do
  [[ -n "${node:-}" ]] || continue
  sum=0
  for m in $mems; do sum=$((sum + $(to_bytes "$m"))); done
  node_requested["$node"]=$(( ${node_requested[$node]:-0} + sum ))
done <<<"$all_pods_json"

# The smallest allocatable node is what the pool can rely on: a pod may land on
# any node of the group, so sizing against the average would still hit
# "Insufficient memory" on the small one.
min_allocatable=0
min_node=""
while IFS=$'\t' read -r node_name allocatable capacity; do
  [[ -n "${node_name:-}" ]] || continue
  bytes="$(to_bytes "$allocatable")"
  if ((bytes > 0)) && { ((min_allocatable == 0)) || ((bytes < min_allocatable)); }; then
    min_allocatable="$bytes"
    min_node="$node_name"
  fi
  report "sandbox_memory node=$node_name capacity=$(format_bytes "$(to_bytes "$capacity")") allocatable=$(format_bytes "$bytes") requested_by_all_pods=$(format_bytes "${node_requested[$node_name]:-0}")"
done <<<"$nodes_json"

if ((min_allocatable == 0)); then
  report "sandbox_memory note=no node allocatable memory visible (RBAC may not allow listing nodes)"
  report "sandbox_memory source=unmeasured"
  exit 0
fi

report "sandbox_memory smallest_node=$min_node smallest_allocatable=$(format_bytes "$min_allocatable")"

# --- 3. reserve, usable memory, sandboxes that fit --------------------------
# Same 10% default reserve as the backend, overridable for the report with
# SANDBOX_MEMORY_RESERVE (a Kubernetes quantity).
if [[ -n "${SANDBOX_MEMORY_RESERVE:-}" ]]; then
  reserve_bytes="$(to_bytes "$SANDBOX_MEMORY_RESERVE")"
  reserve_source="measured:$SANDBOX_MEMORY_RESERVE"
else
  reserve_bytes=$((min_allocatable / 10))
  reserve_source="default:10%"
fi

usable_bytes=$((min_allocatable - reserve_bytes))
((usable_bytes > 0)) || usable_bytes=0

fits=$((usable_bytes / request_bytes))
max_agents="$fits"
if ((max_agents > 1)); then
  max_agents=$((max_agents - 1))
fi

report "sandbox_memory reserve=$(format_bytes "$reserve_bytes") reserve_source=$reserve_source usable=$(format_bytes "$usable_bytes")"
report "sandbox_memory sandboxes_that_fit=$fits agents_per_pool_configured=$AGENTS_PER_POOL effective_agents_per_pool=$max_agents headroom=$(format_bytes "$((usable_bytes - max_agents * request_bytes))")"
report "sandbox_memory source=measured"
report "sandbox_memory replay_env OCI_MSGMATE_CODER_SANDBOX_MEMORY_REQUEST=$(format_bytes "$request_bytes") OCI_MSGMATE_CODER_SANDBOX_MEMORY_LIMIT=$(format_bytes "$limit_bytes") OCI_MSGMATE_CODER_NODE_ALLOCATABLE_MEMORY=$(format_bytes "$min_allocatable") OCI_MSGMATE_CODER_SANDBOX_MEMORY_RESERVE=$(format_bytes "$reserve_bytes")"

# --- credential lifecycle of the same pool (ci#614) --------------------------
# Credentials are per-agent since ci#603, so every workspace bound to a pool owns
# its own HOME and must be re-provisioned when the pool pod (re)starts. This block
# reports the *configuration* of that replay: it is a report, not a probe, and it
# never carries a credential value.
cred_scope="${OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_SCOPE:-per_agent}"
cred_ttl="${OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_TTL_SECONDS:-1800}"
cred_refresh="${OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REFRESH_AHEAD_SECONDS:-300}"
cred_replay="${OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REPROVISION_ON_RESTART:-true}"
if [[ "$cred_ttl" =~ ^[0-9]+$ ]] && ((cred_ttl >= IDLE_TTL_SECONDS)); then
  report "sandbox_credentials note=credential_ttl_${cred_ttl}s_not_below_reaper_idle_ttl_${IDLE_TTL_SECONDS}s: a still-attached agent can see an expired credential"
fi
report "sandbox_credentials scope=$cred_scope ttl_seconds=$cred_ttl refresh_ahead_seconds=$cred_refresh"
report "sandbox_credentials reprovision_on_restart=$cred_replay reaper_idle_ttl_seconds=$IDLE_TTL_SECONDS per_agent_home=OCI_MSGMATE_CODER_WORKSPACE_HOME session_env=GH_TOKEN,GITHUB_TOKEN"
report "sandbox_credentials replay_env OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_SCOPE OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_TTL_SECONDS OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REFRESH_AHEAD_SECONDS OCI_MSGMATE_CODER_SANDBOX_CREDENTIAL_REPROVISION_ON_RESTART"

# --- 4. live usage, if a metrics server answers (never fatal) ----------------
top_nodes="$("$KUBECTL" top nodes --no-headers 2>/dev/null || true)"
top_pods="$("$KUBECTL" -n "$SANDBOX_NAMESPACE" top pods --no-headers 2>/dev/null || true)"
if [[ -n "$top_nodes" ]]; then
  while read -r name cpu mem _rest; do
    [[ -n "${name:-}" ]] || continue
    report "sandbox_memory top_node=$name cpu=$cpu memory=$mem"
  done <<<"$top_nodes"
else
  report "sandbox_memory top_node=unavailable (no metrics server, or RBAC denies nodes/metrics)"
fi
if [[ -n "$top_pods" ]]; then
  top_pods_total=0
  while read -r name cpu mem _rest; do
    [[ -n "${name:-}" ]] || continue
    top_pods_total=$((top_pods_total + $(to_bytes "$mem")))
  done <<<"$top_pods"
  report "sandbox_memory top_pods_measured=$(printf '%s\n' "$top_pods" | grep -c .) top_pods_total_memory=$(format_bytes "$top_pods_total")"
else
  report "sandbox_memory top_pods=unavailable (no metrics server, or RBAC denies pods/metrics)"
fi