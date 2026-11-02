#!/usr/bin/env bash
set -euo pipefail
# Rebuild the dry-run copy of the results tree on the ci/sandbox-results branch
# (the branch is fetched into /tmp/srclone by the prep step). The runner's
# .tsv convention is "<test>\t<status>" per line, last line per test wins.

SRCLONE="${SRCLONE:-/tmp/srclone}"
OUT="${OUT:-benchmark-results/free-models-2026-10-31}"
mkdir -p "$OUT"

# Expected test set (the 40-tag / all-tests set the sweep targets). Marking a
# test "not_run" proves the grid is complete; n/a rows are vision-only.
TESTS=(msgmate_coder_ask msgmate_coder_bash msgmate_coder_bash_stream msgmate_coder_bugfix msgmate_coder_codesearch msgmate_coder_command_confirmation msgmate_coder_compact msgmate_coder_edit msgmate_coder_fibonacci_json msgmate_coder_gh_code_review msgmate_coder_gh_pr_comment msgmate_coder_git_action msgmate_coder_github_primitives msgmate_coder_grep_replace msgmate_coder_hive_mind msgmate_coder_in_sandbox msgmate_coder_openchat_mcp msgmate_coder_patch msgmate_coder_shell_mode msgmate_coder_structured_read msgmate_coder_task msgmate_coder_todo msgmate_coder_tool_scope msgmate_coder_websearch basic_smoke image_file_attachment parallel_sessions parallel_smoke playwright_blog_research playwright_mcp_bootstrap playwright_mcp_sandbox session_continuation github_issue github_issue_comment github_repo_create playwright_tests rest_api_tests lw_rest_api_email_pin lw_rest_api_ensure_user lw_rest_api_tool_init kubernetes_deploy smoke_test websearch_tests)
declare -A VISION_ONLY=( [image_file_attachment]=1 )

# model dir slug => openrouter model id (reverse of modelIDFromSlug)
declare -A MODEL_DIRS=(
  [dots-studio_dots-3-note-preview:free]="dots-studio/dots-3-note-preview:free"
  [thinkingmachines_inkling:free]="thinkingmachines/inkling:free"
  [thinkingmachines_inkling-small:free]="thinkingmachines/inkling-small:free"
  [xingchen-agi_xing4.0-29b-a4b:free]="xingchen-agi/xing4.0-29b-a4b:free"
  [recursive_flint-v1.0:free]="recursive/flint-v1.0:free"
  [nvidia_nemotron-3.5-super-vl:free]="nvidia/nemotron-3.5-super-vl:free"
)

: > "$OUT/errors.log"
: > "$OUT/rowlog.tsv"

for slug in "${!MODEL_DIRS[@]}"; do
  mid="${MODEL_DIRS[$slug]}"
  # Locate the latest committed report markdown for this model (Provider/model line `openrouter/<mid>`).
  report="$(grep -lF "$mid" "$SRCLONE"/sandbox_tests/results/*.md 2>/dev/null | sort | tail -1 || true)"
  if [ -z "$report" ]; then
    echo "WARN no report for $mid in $OUT/errors.log"
    continue
  fi
  : > "$OUT/$slug.tsv"
  # Parse rows: "| `test` | status |" -> test<TAB>status (strip backticks/spaces).
  grep -E '^\| `' "$report" | awk -F'|' '{gsub(/[` ]/,"",$2); gsub(/^ /,"",$3); gsub(/ $/,"",$3); print $2 "\t" $3}' >> "$OUT/$slug.tsv"
  # Fill absent tests with not_run; vision-only with n/a when absent.
  declare -A seen=()
  while IFS=$'\t' read -r t s; do seen[$t]=1; done < "$OUT/$slug.tsv"
  : > "$OUT/$slug.tsv.tmp"
  for t in "${TESTS[@]}"; do
    if [ -n "${seen[$t]:-}" ]; then
      echo "$t	$(grep -P "^$t\t" "$OUT/$slug.tsv")" >> /dev/null
      # preserve original status for tests that ran
      orig="$(awk -F'\t' -v t="$t" '$1==t{print $2}' "$OUT/$slug.tsv" | tail -1)"
      echo "$t	$orig" >> "$OUT/$slug.tsv.tmp"
    elif [ -n "${VISION_ONLY[$t]:-}" ]; then
      echo "$t	n/a" >> "$OUT/$slug.tsv.tmp"
    else
      echo "$t	not_run" >> "$OUT/$slug.tsv.tmp"
    fi
  done
  mv "$OUT/$slug.tsv.tmp" "$OUT/$slug.tsv"
  echo "ok $mid <- $(basename "$report") ($(wc -l < "$OUT/$slug.tsv") rows)"
done

echo "done; errors:"; cat "$OUT/errors.log"