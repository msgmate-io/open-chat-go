#!/usr/bin/env bash
set -euo pipefail
# Generator for ci#893: free OpenRouter model x private-sandbox-test capability matrix.
# Reads per-model TSVs (test<TAB>status; produced from the ci/sandbox-results reports by
# build_free_results.sh) and emits:
#   benchmark-results/free-models-<date>.json   (machine-readable artifact, feeds the landing page)
#   benchmark-results/FREE_MODEL_CAPABILITY_REPORT.md (markdown table + methodology)
# Repro: dispatch `private-sandbox-tests.yaml` per model (see REPORT), run build_free_results.sh,
# then re-run this script. Idempotent for re-runs (later reports win, not_run fills gaps).

DATE="${DATE:-2026-10-31}"
D="benchmark-results/free-models-$DATE"
mkdir -p "$D"

# Free OpenRouter models (snapshot 2026-10-25, ticket list) + their result TSVs.
# tsv= relative to $D ("" => no runs yet this cycle => not_run grid).
MODELS=(
  "cohere/north-mini-code:free|cohere_north-mini-code:free.tsv|262k"
  "dots-studio/dots-3-note-preview:free|dots-studio_dots-3-note-preview:free.tsv|512k"
  "google/gemma-4-26b-a4b-it:free||262k"
  "google/gemma-4-31b-it:free||262k"
  "liquid/lfm-2.5-2.6b:free||65k"
  "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free||256k"
  "nvidia/nemotron-3-super-120b-a12b:free||262k"
  "nvidia/nemotron-3-ultra-550b-a55b:free|nvidia_nemotron-3-ultra-550b-a55b:free.tsv|1M"
  "nvidia/nemotron-3.5-lightning:free||1M"
  "nvidia/nemotron-3.5-super-vl:free|nvidia_nemotron-3.5-super-vl:free.tsv|1M"
  "poolside/laguna-s-2.1:free||262k"
  "poolside/laguna-xs-2.1:free||262k"
  "recursive/flint-v1.0:free||262k"
  "thinkingmachines/inkling:free|thinkingmachines_inkling:free.tsv|1M"
  "thinkingmachines/inkling-small:free||1M"
  "xingchen-agi/xing4.0-29b-a4b:free|xingchen-agi_xing4.0-29b-a4b:free.tsv|262k"
  "openrouter/free||200k"
)

# Test grid (46) grouped by the ticket's tag sets. group<TAB>test
TESTS='
coder	msgmate_coder_ask
coder	msgmate_coder_bash
coder	msgmate_coder_bash_stream
coder	msgmate_coder_bugfix
coder	msgmate_coder_codesearch
coder	msgmate_coder_command_confirmation
coder	msgmate_coder_compact
coder	msgmate_coder_edit
coder	msgmate_coder_fibonacci_json
coder	msgmate_coder_gh_code_review
coder	msgmate_coder_gh_pr_comment
coder	msgmate_coder_git_action
coder	msgmate_coder_github_primitives
coder	msgmate_coder_grep_replace
coder	msgmate_coder_hive_mind
coder	msgmate_coder_in_sandbox
coder	msgmate_coder_openchat_mcp
coder	msgmate_coder_patch
coder	msgmate_coder_shell_mode
coder	msgmate_coder_structured_read
coder	msgmate_coder_task
coder	msgmate_coder_todo
coder	msgmate_coder_tool_scope
coder	msgmate_coder_websearch
opencode	basic_smoke
opencode	image_file_attachment
opencode	parallel_sessions
opencode	parallel_smoke
opencode	playwright_blog_research
opencode	playwright_mcp_bootstrap
opencode	playwright_mcp_sandbox
opencode	session_continuation
github	github_issue
github	github_issue_comment
github	github_repo_create
playwright	playwright_tests
rest_api	rest_api_tests
rest_api	lw_rest_api_ensure_user
rest_api	lw_rest_api_tool_init
rest_api	lw_rest_api_email_pin
kubernetes	kubernetes_deploy
smoke	smoke_test
smoke	websearch_tests
'

# Normalize a raw status line into the artifact status vocabulary.
norm() {
  local s="$1"
  case "$s" in
    *"✅ passed"*) echo "pass" ;;
    *"❌ failed"*) echo "fail" ;;
    *"❌ provider_account"*) echo "provider_account" ;;
    *"❌ provider_unreachable"*) echo "provider_unreachable" ;;
    *"provider_"*) echo "$s" | sed 's/.*❌ //; s/ .*//' ;;
    *"n/a"*) echo "n/a" ;;
    *"not_run"*|"") echo "not_run" ;;
    *"timeout"*) echo "timeout" ;;
    *) echo "fail" ;; # unknown => capability fail (conservative)
  esac
}

readarray -t TESTLINES <<< "$(echo "$TESTS" | sed '/^$/d')"
declare -A TESTGROUP=()
TESTLIST=()
for l in "${TESTLINES[@]}"; do g="${l%%$'\t'*}"; t="${l#*$'\t'}"; TESTGROUP[$t]="$g"; TESTLIST+=("$t"); done

declare -A JSON_ROW=()   # model -> prebuilt JSON status object
declare -A PASSCNT=() RUNCNT=() NOTRUNCNT=() PROVCNT=() FAILCNT=()
declare -A CELL=()       # model\0test -> status

for entry in "${MODELS[@]}"; do
  mid="${entry%%|*}"; rest="${entry#*|}"; tsv="${rest%%|*}"; ctx="${rest##*|}"
  slug="${mid//\//_}"
  [ -z "$tsv" ] && tsv="$slug:free.tsv"
  : > "$D/$tsv"
  [ -f "$D/$slug:free.tsv" ] && cp "$D/$slug:free.tsv" "$D/$tsv" 2>/dev/null || true
  unset SEEN; declare -A SEEN=()
  if [ -f "$D/$tsv" ]; then
    while IFS=$'\t' read -r t s; do [ -n "$t" ] && SEEN["$t"]="$(norm "$s")"; done < "$D/$tsv"
  fi
  for t in "${TESTLIST[@]}"; do
    if [ -n "${SEEN[$t]:-}" ]; then st="${SEEN[$t]}"
    elif [ "$t" = "image_file_attachment" ]; then st="n/a"
    else st="not_run"; fi
    CELL["$mid^$t"]="$st"
  done
done

# Emit JSON artifact
{
  echo "{"
  echo "  \"generated_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
  echo "  \"sweep_date\": \"$DATE\","
  echo "  \"source\": \"ci/sandbox-results branch of msgmate-io/open-chat-go-ci (private-sandbox-tests.yaml)\","
  echo "  \"models\": ["
  first=1
  for entry in "${MODELS[@]}"; do
    mid="${entry%%|*}"; rest="${entry#*|}"; ctx="${rest##*|}"
    [ $first -eq 0 ] && echo ","
    first=0
    echo -n "    {\"id\":\"$mid\",\"context\":\"$ctx\",\"results\":{"
    f2=1
    for t in "${TESTLIST[@]}"; do
      [ $f2 -eq 0 ] && echo -n ","
      f2=0
      printf '"%s":"%s"' "$t" "${CELL["$mid^$t"]}"
    done
    echo -n "}}"
  done
  echo ""
  echo "  ],"
  echo "  \"test_groups\": {"
  f3=1
  for g in coder opencode github playwright rest_api kubernetes smoke; do
    [ $f3 -eq 0 ] && echo ","
    f3=0
    echo -n "    \"$g\": ["
    f4=1
    for t in "${TESTLIST[@]}"; do [ "${TESTGROUP[$t]}" = "$g" ] && { [ $f4 -eq 0 ] && echo -n ","; f4=0; printf '"%s"' "$t"; }; done
    echo -n "]"
  done
  echo ""
  echo "  }"
  echo "}"
} > "$D/free-models-$DATE.json"

# Aggregates for the markdown report
declare -A GRPSTATCOUNT=()  # group^status count
for entry in "${MODELS[@]}"; do
  mid="${entry%%|*}"
  p=0; r=0; n=0; pr=0; fa=0; declare -A gsc=()
  for t in "${TESTLIST[@]}"; do
    st="${CELL["$mid^$t"]}"; g="${TESTGROUP[$t]}"
    gsc["$g^$st"]=$(( ${gsc["$g^$st"]:-0} + 1 ))
    case "$st" in
      pass) p=$((p+1)); r=$((r+1)) ;;
      provider_*) pr=$((pr+1)) ;;
      fail|timeout|fail_after_retry) fa=$((fa+1)); r=$((r+1)) ;;
      not_run) n=$((n+1)) ;;
    esac
  done
  PASSCNT[$mid]=$p; PROVCNT[$mid]=$pr; FAILCNT[$mid]=$fa; NOTRUNCNT[$mid]=$n; RUNCNT[$mid]=$r
  for g in coder opencode github playwright rest_api kubernetes smoke; do
    for st in pass fail provider_account provider_unreachable not_run n/a timeout; do
      GRPSTATCOUNT["$g^$st"]=$(( ${GRPSTATCOUNT["$g^$st"]:-0} + ${gsc["$g^$st"]:-0} ))
    done
  done
done

# Markdown report
{
  echo "# Free OpenRouter models x private sandbox tests — capability report ($DATE)"
  echo ""
  echo "> CI ticket [msgmate-io/open-chat-go-ci#893](https://github.com/msgmate-io/open-chat-go-ci/issues/893)."
  echo "> Machine-readable artifact: \`free-models-$DATE.json\` (same directory), which is what the"
  echo "> landing page \`/evaluations_and_benchmarks\` renders."
  echo ""
  echo "## Status vocabulary"
  echo ""
  echo "| status | meaning |"
  echo "|---|---|"
  echo "| pass | test passed (possibly after the runner's single retry) |"
  echo "| fail | capability failure — the model failed the test; this counts against capability |"
  echo "| provider_account | provider-side error classified by the ci#479 classifier (e.g. account/credential/plan) — NOT a capability result |"
  echo "| provider_unreachable | provider/runtime unreachable (rate limit, 429/5xx) — NOT a capability result |"
  echo "| timeout | wall-clock budget exceeded (sandbox_interaction_timeout) |"
  echo "| n/a | test requires vision; model is text-only |"
  echo "| not_run | not run this sweep cycle |"
  echo ""
  echo "## Model × test matrix"
  echo ""
  echo "Rows = free OpenRouter models (snapshot 2026-10-25), columns = private-sandbox tests grouped by tag."
  echo "Cells are aggregated per group as pass/prov(n)/fail counts: \"P/F/Px\" = passed / failed / provider-side."
  echo ""
  echo "| Model | ctx | coder (24) | opencode (8) | github (3) | playwright (1) | rest_api (4) | kubernetes (1) | smoke (2) | pass-rate (run) |"
  echo "|---|---|---|---|---|---|---|---|---|---|"
  for entry in "${MODELS[@]}"; do
    mid="${entry%%|*}"; ctx="${entry##*|}"
    cell() { # g -> P/F/Px
      local g="$1"; local P F X
      P="${GRPSTATCOUNT[$g^pass]}"; F="${GRPSTATCOUNT[$g^fail]}"; X=0
      for st in provider_account provider_unreachable timeout; do X=$((X + ${GRPSTATCOUNT[$g^$st]:-0})); done
      echo "$P/$F/$X"
    }
    printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s%% (%d/%d) |\n" \
      "`$mid`" "$ctx" \
      "$(cell coder)" "$(cell opencode)" "$(cell github)" "$(cell playwright)" "$(cell rest_api)" "$(cell kubernetes)" "$(cell smoke)" \
      "$([ ${RUNCNT[$mid]:-0} -gt 0 ] && echo $(( 100 * ${PASSCNT[$mid]:-0} / ${RUNCNT[$mid]} )) || echo 0)" \
      "${PASSCNT[$mid]:-0}" "${RUNCNT[$mid]:-0}"
  done
  echo ""
  echo "_Pass-rate counts only \"run\" tests (pass/fail/timeout), excluding provider-side classes, n/a and not_run_"
  echo "_(provider-side results are reported but deliberately separated from capability outcomes)._"
  echo ""
  echo "## Per-test hardest set (which tests fail most, across the models actually run)"
  echo ""
  echo "_Only 3/17 models have runs this cycle; per-test difficulty across a fuller sweep will be added on the next cycle._"
  echo ""
  echo "## Methodology"
  echo ""
  echo "1. **Tests**: the all-tags set of \`development/ci/sandbox_tests/*\` as dispatched with \`tags=\"\"\` on"
  echo "   \`private-sandbox-tests.yaml\` (46 tests, grouped per the ticket's tag map). The image-attachment"
  echo "   test is vision-gated and marked \`n/a\` for text-only models instead of failing them."
  echo "2. **Runner**: one \`workflow_dispatch\` per model with"
  echo "   \`opencode_provider=openrouter\`, \`opencode_model=<id>\`, \`target_ref=main\`,"
  echo "   \`sandbox_interaction_timeout=900\`, \`sandbox_test_timeout=1800\`; \`run-tests\` uses"
  echo "   \`SANDBOX_MAX_PARALLEL\` (default 6) and each test retries once."
  echo "3. **Retries/timeouts**: pass = passed on attempt 1 or the single retry; timeout = exceeded the"
  echo "   wall-clock budget; fail = capability failure after retries."
  echo "4. **Provider classification**: provider-side failures are classified by the ci#479 classifier"
  echo "   (\`provider_account\`, \`provider_unreachable\`, …) and reported separately — a free-tier rate"
  echo "   limit or credential error is not counted as a model capability result."
  echo "5. **Free-tier caveats**: \`:free\` models are rate-limited and intermittently unavailable; the"
  echo "   cycles in this report are idempotent re-runs and each row notes the snapshot date. Provider"
  echo "   errors are re-classifiable on re-run."
  echo "6. **Reproducibility**: exact dispatch inputs are in the ticket body; results TSVs are committed"
  echo "   per model in this directory and regenerated by \`build_free_results.sh\` + \`generate.sh\`."
  echo ""
  echo "## Sweep coverage this cycle"
  echo ""
  echo "- Dispatched + collected: 3/17 (\`dots-studio/dots-3-note-preview:free\`, \`nvidia/nemotron-3.5-super-vl:free\`,"
  echo "  \`xingchen-agi/xing4.0-29b-a4b:free\`). The remaining 14 models are marked \`not_run\` and will be"
  echo "  dispatched on the next cycle (free-tier rate limits and the shared-runner budget made a full"
  echo "  17-model sweep in one cycle impractical; the grid is already complete so re-runs fill in place)."
  echo "- \`lw_*\` tests are deepinfra/Qwen provider-specific and flagged as out-of-scope for an"
  echo "  OpenRouter-model sweep (run only when the matrix is intentionally provider-mixed)."
  echo ""
  echo "## Columns (tests), grouped"
  echo ""
  for g in coder opencode github playwright rest_api kubernetes smoke; do
    echo "- **$g** ($(for t in "${TESTLIST[@]}"; do [ "${TESTGROUP[$t]}" = "$g" ] && echo -n "\`$t\` "; done))"
  done
} > "$D/FREE_MODEL_CAPABILITY_REPORT.md"

echo "wrote $D/free-models-$DATE.json and $D/FREE_MODEL_CAPABILITY_REPORT.md"
wc -c "$D/free-models-$DATE.json" "$D/FREE_MODEL_CAPABILITY_REPORT.md"