#!/usr/bin/env bash
#
# Free OpenRouter model capability sweep for the private sandbox test suite.
#
# ci#893: dispatch the private-sandbox-tests workflow (msgmate-io/open-chat-go-ci)
# once per free OpenRouter model, collect the ci/sandbox-results reports, and
# aggregate them into a capability matrix (JSON + markdown) that can feed the
# /evaluations_and_benchmarks product landing page.
#
# Usage:
#   ./free_model_capability_sweep.sh --collect-only                # gather published results only
#   ./free_model_capability_sweep.sh --dispatch                    # dispatch the full 17-model matrix (serial)
#   ./free_model_capability_sweep.sh --dispatch --models 'a,b,c'    # subset, comma separated
#   ./free_model_capability_sweep.sh --dispatch --tags msgmate_coder  # tag-filtered runs
#
# Shared flags:
#   --target-ref REF      ref the workflow builds from (default: main)
#   --output-dir DIR      where artifacts land (default: benchmark-results)
#   --interaction-timeout seconds   passed to the workflow (default 900)
#   --test-timeout seconds          passed to the workflow (default 1800)
#   --poll-interval seconds         run polling interval (default 60)
#   --max-wait-minutes N            abort waiting after N minutes per model (default 240)
#   --parallel N                    dispatch N models before waiting (default 1 = serial)
#   --verbose
#
# Notes:
#   - Run is idempotent: --collect-only never dispatches; --dispatch skips a
#     model if it already has a successful report for today in the output dir.
#   - results statuses are taken from the ci/sandbox-results branch of
#     msgmate-io/open-chat-go-ci (per-run markdown report + per-test TSVs).
#   - provider_* / timeout are classified separately from capability fail
#     (ci#479 classifier) so free-tier rate limits are not counted as coder
#     regressions.

set -u

CI_REPO="msgmate-io/open-chat-go-ci"
WORKFLOW="private-sandbox-tests.yaml"

# Default free model list (snapshot 2026-10-25, ci#893).
MODELS=( \
  "cohere/north-mini-code:free" \
  "dots-studio/dots-3-note-preview:free" \
  "google/gemma-4-26b-a4b-it:free" \
  "google/gemma-4-31b-it:free" \
  "liquid/lfm-2.5-2.6b:free" \
  "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free" \
  "nvidia/nemotron-3-super-120b-a12b:free" \
  "nvidia/nemotron-3-ultra-550b-a55b:free" \
  "nvidia/nemotron-3.5-lightning:free" \
  "nvidia/nemotron-3.5-super-vl:free" \
  "poolside/laguna-s-2.1:free" \
  "poolside/laguna-xs-2.1:free" \
  "recursive/flint-v1.0:free" \
  "thinkingmachines/inkling:free" \
  "thinkingmachines/inkling-small:free" \
  "xingchen-agi/xing4.0-29b-a4b:free" \
  "openrouter/free" \
)

DISPATCH=0
COLLECT_ONLY=0
TARGET_REF="main"
OUTPUT_DIR="benchmark-results"
INTERACTION_TIMEOUT=900
TEST_TIMEOUT=1800
POLL_INTERVAL=60
MAX_WAIT_MINUTES=240
PARALLEL=1
VERBOSE=0
TAGS=""
MODEL_FILTER=""

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dispatch) DISPATCH=1 ;;
    --collect-only) COLLECT_ONLY=1 ;;
    --models) shift; MODEL_FILTER="${1:-}" ;;
    --tags) shift; TAGS="${1:-}" ;;
    --target-ref) shift; TARGET_REF="${1:-}" ;;
    --output-dir) shift; OUTPUT_DIR="${1:-}" ;;
    --interaction-timeout) shift; INTERACTION_TIMEOUT="${1:-}" ;;
    --test-timeout) shift; TEST_TIMEOUT="${1:-}" ;;
    --poll-interval) shift; POLL_INTERVAL="${1:-}" ;;
    --max-wait-minutes) shift; MAX_WAIT_MINUTES="${1:-}" ;;
    --parallel) shift; PARALLEL="${1:-}" ;;
    --verbose) VERBOSE=1 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
  shift
done

STAMP="$(date +%Y-%m-%d)"
mkdir -p "$OUTPUT_DIR"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

run_models=()
if [ -n "$MODEL_FILTER" ]; then
  IFS=',' read -r -a run_models <<< "$MODEL_FILTER"
else
  run_models=("${MODELS[@]}")
fi

log() { echo "[sweep] $*"; }
vlog() { [ "$VERBOSE" -eq 1 ] && echo "[sweep:debug] $*"; }

model_slug() { echo "$1" | tr '/:' '__'; }

# ---------------------------------------------------------------------------
# Collection: fetch ci/sandbox-results and pull model-specific reports/TSVs.
# ---------------------------------------------------------------------------
collect_one() {
  local model="$1"
  local slug; slug="$(model_slug "$model")"
  local dst="$OUTPUT_DIR/$slug"
  mkdir -p "$dst"

  if [ -z "$(ls -A "$dst" 2>/dev/null)" ] || [ "$COLLECT_ONLY" -eq 1 ]; then
    ( cd "$TMP" && gh repo clone "$CI_REPO" ci-repo -- --depth 1 --branch ci/sandbox-results --single-branch >/dev/null 2>&1 ) || {
      # Retry with a plain fetch of the branch in case of a transient failure.
      ( cd "$TMP" && gh repo clone "$CI_REPO" ci-repo >/dev/null 2>&1 \
        && cd ci-repo && git fetch --depth 1 origin ci/sandbox-results >/dev/null 2>&1 \
        && git checkout FETCH_HEAD >/dev/null 2>&1 )
    }
    if [ -d "$TMP/ci-repo" ]; then
      local found=0
      while IFS= read -r f; do
        case "$f" in
          *.tsv|*.md|*.json)
            if grep -q -i -- "model[^a-z]*\(id\|=[^a-z]*\)\?$slug\|$model\|$slug" "$TMP/ci-repo/$f" 2>/dev/null; then
              cp "$TMP/ci-repo/$f" "$dst/$(basename "$(dirname "$f")")_$(basename "$f")"
              found=1
            fi
            ;;
        esac
      done < <(cd "$TMP/ci-repo" && git ls-tree -r --name-only HEAD 2>/dev/null)
      [ "$found" -eq 0 ] && log "collect: no ci/sandbox-results files matched $model"
    else
      log "collect: could not fetch ci/sandbox-results (is gh authenticated for $CI_REPO?)"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Dispatch + poll one model.
# ---------------------------------------------------------------------------
dispatch_one() {
  local model="$1"
  local slug; slug="$(model_slug "$model")"
  local dst="$OUTPUT_DIR/$slug"

  # Skip if a report for today already exists (idempotency).
  if ls "$dst"/*"$STAMP"* >/dev/null 2>&1 || [ -f "$dst/summary.md" ]; then
    log "skip $model: results already collected (rerun with --collect-only to refresh)"
    return 0
  fi

  log "dispatch $model (provider=openrouter, tags='${TAGS}', ref=$TARGET_REF)"
  gh workflow run "$WORKFLOW" -R "$CI_REPO" \
    -f target_ref="$TARGET_REF" \
    -f tags="$TAGS" \
    -f opencode_provider=openrouter \
    -f opencode_model="$model" \
    -f sandbox_interaction_timeout="$INTERACTION_TIMEOUT" \
    -f sandbox_test_timeout="$TEST_TIMEOUT" || { log "dispatch failed for $model"; return 1; }
  log "dispatched $model; waiting for the run to appear..."
}

wait_one() {
  local model="$1"
  local deadline=$(( $(date +%s) + MAX_WAIT_MINUTES * 60 ))
  local before after
  before="$(date +%s)"
  while [ "$(date +%s)" -lt "$deadline" ]; do
    local run
    run="$(gh run list -R "$CI_REPO" --workflow "$WORKFLOW" --limit 15 \
      --json databaseId,displayTitle,status,event,createdAt,headSha \
      --jq ".[] | select(.event==\"workflow_dispatch\") | select(.displayTitle|test(\"$model\";\"i\")) | select(.createdAt > \"$(date -u -d "@$before" +%Y-%m-%dT%H:%M:%SZ)\") | select(.status!=\"completed\") | .databaseId" 2>/dev/null | head -1)"
    if [ -n "$run" ]; then
      log "run $run for $model is $(
        gh run view "$run" -R "$CI_REPO" --json status --jq .status 2>/dev/null)"
      sleep "$POLL_INTERVAL"
      continue
    fi
    # No pending run found: check the newest completed run for this model.
    local done_run
    done_run="$(gh run list -R "$CI_REPO" --workflow "$WORKFLOW" --limit 15 \
      --json databaseId,displayTitle,status,conclusion,headSha \
      --jq ".[] | select(.event==\"workflow_dispatch\") | select(.displayTitle|test(\"$model\";\"i\")) | select(.status==\"completed\") | \"\(.databaseId) \(.conclusion)\"" 2>/dev/null | head -1)"
    if [ -n "$done_run" ]; then
      log "completed run for $model: $done_run"
      collect_one "$model"
      return 0
    fi
    after="$(date +%s)"
    [ $(( after - before )) -gt 600 ] && { before="$after"; vlog "still waiting for $model to appear in run list"; }
    sleep "$POLL_INTERVAL"
  done
  log "timeout waiting for $model (max ${MAX_WAIT_MINUTES}m)"
  return 1
}

# ---------------------------------------------------------------------------
# Aggregation into JSON + markdown capability table.
# ---------------------------------------------------------------------------
aggregate() {
  local json="$OUTPUT_DIR/free-models-$STAMP.json"
  local md="$OUTPUT_DIR/free-models-$STAMP.md"
  local payload="{}" rows=""
  local first=1

  for model in "${run_models[@]}"; do
    local slug; slug="$(model_slug "$model")"
    local dst="$OUTPUT_DIR/$slug"
    [ -d "$dst" ] || continue
    [ -n "$(ls -A "$dst" 2>/dev/null)" ] || continue

    # Collect the union of test names from the TSVs, with their final status.
    local model_json="{}"
    while IFS= read -r status; do
      model_json="$(printf '%s' "$model_json" | \
        jq --arg st "${status%%|*}" --arg test "${status#*|}" \
           --arg m "$model" \
           '.[$m] = (.[$m] // {}) | .[$m][$test] = $st')"
    done < <(grep -h -E -o '^[^[:space:]]+[[:space:]]+(pass|fail|fail_after_retry|timeout|provider_[a-z_]+|error)' "$dst"/*.tsv 2>/dev/null | sed 's/^\([^[:space:]]*\)[[:space:]]\+\(.*\)$/\2|\1/')
    [ "$model_json" = "{}" ] && model_json="$(printf '{"%s":{}}' "$model")"

    rows="$rows$model_json"
    if [ "$first" -eq 1 ]; then payload="$model_json"; first=0; else
      payload="$(jq -n --argjson a "$payload" --argjson b "$model_json" '$a + $b')"
    fi
  done

  [ -z "$payload" ] && { log "aggregate: nothing collected yet; nothing written"; return 1; }
  payload="$(printf '%s' "$payload" | jq --arg s "$STAMP" '{snapshot: $s, generated_at: (now|todateiso8601), rows: .}')"
  printf '%s\n' "$payload" > "$json"
  log "wrote $json"

  # Markdown capability table (columns: model, tests run, pass rate, provider/error).
  {
    echo "# Free model capability matrix ($STAMP) — ci#893"
    echo
    echo "| Model | Tests | Pass | Pass rate | Provider/error | Timeout |"
    echo "|---|---|---|---|---|---|"
    printf '%s' "$payload" | jq -r '.rows | to_entries[] as $m |
      ($m.value | to_entries) as $tests |
      ([$tests[] | select(.value=="pass")] | length) as $pass |
      ([$tests[] | select(.value|test("provider_|error"))] | length) as $prov |
      ([$tests[] | select(.value=="timeout")] | length) as $to |
      (($tests|length)) as $total |
      "\($m.key) | \($total) | \($pass) | \((100*$pass)/$total) | \($prov) | \($to)"'
  } > "$md"
  log "wrote $md"

  printf '{"json":"%s","md":"%s"}\n' "$json" "$md"
}

# ---------------------------------------------------------------------------
# Main entry. Guarded so the file can be safely sourced (e.g. by unit tests
# exercising aggregate() without dispatching anything).
# ---------------------------------------------------------------------------
[ "$COLLECT_ONLY" -eq 1 ] && { DISPATCH=0; }

# When sourced for testing, the caller provides OUTPUT_DIR/STAMP/run_models and
# executes only the functions it needs; skip the top-level flow entirely.
[ "${FREE_MODEL_SWEEP_FUNCTIONS_ONLY:-0}" -eq 1 ] && return 0 2>/dev/null || true

log "ci#893 free-model sweep: models=${#run_models[@]} tags='${TAGS}' ref=$TARGET_REF"
log "output dir: $OUTPUT_DIR (stamp $STAMP)"

if [ "$DISPATCH" -eq 1 ]; then
  i=0
  for model in "${run_models[@]}"; do
    i=$((i + 1))
    dispatch_one "$model"
    [ $(( i % PARALLEL )) -eq 0 ] || [ "$i" -eq "${#run_models[@]}" ] || continue
    # With serial dispatch we wait for each model before dispatching the next.
    if [ "$PARALLEL" -le 1 ]; then
      wait_one "$model"
    fi
  done
else
  log "collection-only mode: fetching existing ci/sandbox-results reports"
fi

for model in "${run_models[@]}"; do collect_one "$model"; done
aggregate || exit 1
log "done. Review the markdown table and, once runs finish, commit $OUTPUT_DIR/free-models-$STAMP.json"
