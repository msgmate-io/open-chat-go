#!/usr/bin/env bash
# Runs one sandbox benchmark test end-to-end against an isolated backend:
#   1. sandbox container up (repo mounted at /workspace)
#   2. backend+redis started with the test's own config (fresh DB via -v)
#   3. `open-chat run --bot <bot> --message <prompt>` inside the backend
#   4. the test's verify.sh post-completion hook
# Emits "<name>\t<passed|failed>\t<detail>\n" and writes a result tsv.
#
# Usage:  lib/run_test.sh <test-name>
# Env:    BACKEND_IMAGE (default dev-backend), SANDBOX_RESULTS_DIR, SANDBOX_INTERACTION_TIMEOUT
set -euo pipefail

TEST_NAME="${1:-}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"                 # .../sandbox_tests/lib
SANDBOX_TESTS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"                          # .../sandbox_tests
REPO_ROOT="$(cd "$SANDBOX_TESTS_DIR/../../.." && pwd)"                     # repo root
TEST_DIR="$SANDBOX_TESTS_DIR/$TEST_NAME"
# Transient per-test runtime state (env file, session cookies) is written to
# the gitignored results dir - NEVER inside the repository.
RESULTS_DIR="${SANDBOX_RESULTS_DIR:-$REPO_ROOT/.sandbox-results}"
# Isolation: run the harness stack under its own compose project and on
# non-default host ports so `down -v` can NEVER touch a running dev stack
# (its backend keeps its DB inside the container, so removing that container
# destroys local chat data). CI runners are unaffected: they set nothing, so
# the same defaults apply and simply bind different (free) ports.
COMPOSE_PROJECT_NAME="open-chat-sandbox-${TEST_NAME}"
BACKEND_PORT="${BACKEND_PORT:-19984}"
REDIS_PORT="${REDIS_PORT:-16380}"
BACKEND_URL="${BACKEND_HOST:-http://127.0.0.1:${BACKEND_PORT}}"
BACKEND_IMAGE="${BACKEND_IMAGE:-dev-backend}"
# Pin the sandbox workspace host path so host-side pre-staging (the repo clone
# written into the mounted scratch dir) lands inside the container's /workspace.
# CI sets SANDBOX_WORKSPACE_HOST_PATH in the job env; locally default to the
# docker-compose overlay's own relative default (resolved against the
# docker-sandbox compose file dir) so pre-staging and the mount agree.
# IMPORTANT: the scratch dir must exist (created by the runner user) BEFORE the
# sandbox container starts - docker creates missing bind-mount sources as root,
# which would leave the runner user unable to write the pre-staged clone.
SANDBOX_WORKSPACE_HOST_PATH="${SANDBOX_WORKSPACE_HOST_PATH:-$REPO_ROOT/clients/integrations/docker_sandbox_integration/.sandbox-workspace}"
mkdir -p "$SANDBOX_WORKSPACE_HOST_PATH" 2>/dev/null || true
export COMPOSE_PROJECT_NAME BACKEND_PORT REDIS_PORT SANDBOX_WORKSPACE_HOST_PATH
mkdir -p "$RESULTS_DIR"
CONTENT_FILE="$RESULTS_DIR/.ci-sandbox-$TEST_NAME.env"
COOKIE_FILE="$RESULTS_DIR/.ci-sandbox-$TEST_NAME.cookies"
INTERACTION_TIMEOUT_DEFAULT="${SANDBOX_INTERACTION_TIMEOUT:-600}"
# Per-test override: config.json "interaction_timeout" (repo_changes_summary
# involves many slow free-tier model turns, so it gets a bigger budget).
INTERACTION_CONFIG_TIMEOUT="$(jq -r '.interaction_timeout // empty' "$TEST_DIR/config.json" 2>/dev/null || true)"
INTERACTION_TIMEOUT="${INTERACTION_CONFIG_TIMEOUT:-$INTERACTION_TIMEOUT_DEFAULT}"
# Hard wall-clock budgets for the per-test hooks. A wedged setup/verify script
# (eg docker exec blocking on an unresponsive k3s API server) must never be
# able to consume the whole CI job budget; kill it and fail the test instead.
SETUP_TIMEOUT="${SANDBOX_SETUP_TIMEOUT:-420}"
VERIFY_TIMEOUT="${SANDBOX_VERIFY_TIMEOUT:-300}"
# Wall-clock timer for the benchmark summary table.
RUN_START="$(date +%s)"

if [ -z "$TEST_NAME" ]; then
  echo "usage: run_test.sh <test-name>" >&2; exit 2
fi
for f in config.json prompt.txt verify.sh; do
  [ -f "$TEST_DIR/$f" ] || { echo "::error::sandbox test '$TEST_NAME' missing $f" >&2; exit 1; }
done

# shellcheck source=lib/api.sh
source "$SCRIPT_DIR/api.sh"

export REPO_ROOT CONTENT_FILE COOKIE_FILE BACKEND_URL BACKEND_IMAGE BOT_USER RESULTS_DIR TEST_DIR INTERACTION_TIMEOUT SERVER_UUID GIT_TOKEN_UUID TEST_NAME
# Runtime identity of the bot under test: "opencode" only when the test config
# actually selects the legacy opencode chat backend, otherwise "native" (the
# native msgmate coder). Keeps artifacts, log probes and token collection
# runtime-neutral instead of always reporting opencode (ci#779).
TEST_RUNTIME="$(detect_test_runtime "$TEST_DIR/config.json")"
export TEST_RUNTIME

BOT_USER="$(jq -r '.bootstrap.bots[0].bot.username' "$TEST_DIR/config.json")"
if [ -z "$BOT_USER" ] || [ "$BOT_USER" = "null" ]; then
  echo "::error::cannot determine bot username for $TEST_NAME" >&2; exit 1
fi

cd "$REPO_ROOT"
STATUS="failed"
OUT_FILE="$RESULTS_DIR/$TEST_NAME.out"
mkdir -p "$RESULTS_DIR"

# Reclaims every resource this test started. The CI job also runs an
# independent always() reaper (lib/reap_ci_resources.sh) because the workflow
# wraps this script in `timeout --kill-after`, and a SIGKILLed script never
# reaches its EXIT trap; this trap is the fast path for the normal case.
# A cleanup failure is escalated to a job failure (never swallowed) so leaked
# containers/volumes surface instead of quietly overloading the runner/CPU.
cleanup() {
  local rc=$?
  set +e
  local cleanup_rc=0
  teardown_backend || cleanup_rc=1
  # Optional per-test teardown hook (eg stop an auxiliary little-world stack).
  if [ -f "$TEST_DIR/teardown.sh" ]; then
    bash "$TEST_DIR/teardown.sh" >/dev/null 2>&1 || cleanup_rc=1
  fi
  # Only touch the shared `dev-sandbox` container when this test actually used
  # the sandbox stack (it has a fixed, project-independent name).
  if [ "${NEEDS_SANDBOX:-false}" = "true" ] \
     && [ -f "$REPO_ROOT/clients/integrations/docker_sandbox_integration/docker-compose.yml" ]; then
    teardown_sandbox || cleanup_rc=1
  fi
  if [ "$cleanup_rc" -ne 0 ]; then
    echo "::error::resource cleanup failed for sandbox test '$TEST_NAME' - containers or volumes may have leaked on the runner" >&2
    [ "$rc" -eq 0 ] && rc=1
  fi
  exit "$rc"
}
trap cleanup EXIT

# Backend binary path differs between the local dev image and CI prod-alpine.
BACKEND_BIN="backend"
[ "$BACKEND_IMAGE" = "dev-backend" ] && BACKEND_BIN="/backend/.devbin/backend"

# The harness compose overlay attaches the backend to the shared external
# network "open-chat-lw-shared"; it must exist for EVERY test (including tests
# that skip the sandbox VM entirely), so create it here - not only inside
# start_sandbox.
docker network create open-chat-lw-shared 2>/dev/null || true

# Sandbox VM: only needed by tests that bootstrap SSH servers / run bots on
# the sandbox (opencode-backed tests). Tests without an ssh bootstrap section
# (eg plain-chat-backend tool benchmarks) skip the container entirely.
NEEDS_SANDBOX="$(jq -r 'if (.bootstrap.ssh // null) != null then "true" else "false" end' "$TEST_DIR/config.json" 2>/dev/null || echo true)"
SANDBOX_READY=false
if [ "$NEEDS_SANDBOX" = "true" ]; then
  if start_sandbox; then
    SANDBOX_READY=true
  fi
else
  SANDBOX_READY=true
fi

if [ "$SANDBOX_READY" = "true" ]; then
  # Build the effective config: pin the opencode provider/model via
  # SANDBOX_OPENCODE_PROVIDER/SANDBOX_OPENCODE_MODEL before starting the backend.
  EFFECTIVE_CONFIG="$RESULTS_DIR/$TEST_NAME.effective-config.json"
  if ! build_effective_config "$TEST_DIR/config.json" "$EFFECTIVE_CONFIG"; then
    echo "::error::failed to inject opencode model/provider into $TEST_NAME config" >&2
    exit 1
  fi
  if ! start_backend "$EFFECTIVE_CONFIG"; then
    echo "::error::backend failed to start for $TEST_NAME" >&2
    exit 1
  fi
  if ! login_admin; then
    exit 1
  fi
  SERVER_UUID="$(ssh_server_uuid "docker-sandbox")"
  if [ "$SANDBOX_READY" = "true" ] && [ -n "$SERVER_UUID" ]; then
    # Pre-flight: /workspace must be a clean, writable clone target. The bot
    # clones the repo itself per the prompt, so remove stale clones and verify
    # git is available inside the sandbox.
    # Pre-stage the repository into /workspace from the runner's checkout so
    # the benchmark bot does not need network access: the frontend sub-repo is
    # PRIVATE (anonymous clone fails with a credential prompt) and cloning on
    # the runner is faster and more reliable. The bots then work with the
    # pre-staged clone (git log / file writes). The container may hold stale
    # root-owned leftovers from previous runs - clean via the container.
    PLANET_DIR="/workspace/open-chat-go"
    PRECLONE_DIR="${SANDBOX_WORKSPACE_HOST_PATH:-$REPO_ROOT/clients/integrations/docker_sandbox_integration/.sandbox-workspace}/open-chat-go"
    PRECLONE_ERR_FILE="$RESULTS_DIR/$TEST_NAME.preclone.err"
    sandbox_exec "rm -rf /workspace/open-chat-go; mkdir -p /workspace" >/dev/null || {
      echo "::error::/workspace is not writable in the sandbox" >&2
      exit 1
    }
    # The container may have left root-owned leftovers in the scratch dir; CI
    # runners have passwordless sudo, so prefer a plain rm and fall back to it.
    rm -rf "$PRECLONE_DIR" 2>/dev/null || sudo rm -rf "$PRECLONE_DIR" || true
    mkdir -p "$PRECLONE_DIR" || sudo mkdir -p "$PRECLONE_DIR"
    pre_staged=false
    if git clone --no-hardlinks "$REPO_ROOT" "$PRECLONE_DIR" >"$PRECLONE_ERR_FILE" 2>&1; then
      pre_staged=true
    fi
    if [ "$pre_staged" = false ]; then
      # Fallback: plain checkout copy (tar pipe). Works for any checkout state
      # (shallow, detached, partial filters) where a local `git clone` is
      # refused. Excludes harness scratch dirs to avoid a recursive copy.
      rm -rf "$PRECLONE_DIR" 2>/dev/null || sudo rm -rf "$PRECLONE_DIR" || true
      mkdir -p "$PRECLONE_DIR" || sudo mkdir -p "$PRECLONE_DIR"
      echo "[sandbox-test] git clone pre-stage failed; falling back to checkout copy" >&2
      if (cd "$REPO_ROOT" && tar -cf - \
            --exclude='./.sandbox-scratch' \
            --exclude='./.sandbox-results' \
            --exclude='./.sandbox-workspace' \
            --exclude='./frontend/dist' \
            --exclude='./frontend/node_modules' \
            --exclude='./backend/.tools' \
            --exclude='./backend/.devbin' \
            .) | (cd "$PRECLONE_DIR" && tar -xf -) 2>>"$PRECLONE_ERR_FILE"; then
        pre_staged=true
      fi
    fi
    if [ "$pre_staged" != true ]; then
      echo "::error::failed to pre-stage the repository clone for $TEST_NAME (source: $REPO_ROOT -> $PRECLONE_DIR)" >&2
      echo "--- error output ---" >&2
      tail -15 "$PRECLONE_ERR_FILE" >&2 || true
      echo "--- git: $(git --version 2>&1); disk: $(df -h "$PRECLONE_DIR" 2>/dev/null | tail -1)" >&2
      exit 1
    fi
    if [ -d "$REPO_ROOT/frontend/.git" ] || [ -f "$REPO_ROOT/frontend/.git" ]; then
      rm -rf "$PRECLONE_DIR/frontend"
      if ! git clone --no-hardlinks "$REPO_ROOT/frontend" "$PRECLONE_DIR/frontend" >"$PRECLONE_ERR_FILE" 2>&1; then
        echo "::error::failed to pre-stage the frontend sub-repository for $TEST_NAME" >&2
        echo "--- git clone error output ---" >&2
        tail -15 "$PRECLONE_ERR_FILE" >&2 || true
        exit 1
      fi
    fi
    sandbox_exec "git -C /workspace/open-chat-go log --oneline -n 1 >/dev/null && git -C /workspace/open-chat-go/frontend log --oneline -n 1 >/dev/null && test -f /workspace/open-chat-go/frontend/package.json" >/dev/null || {
      echo "::error::pre-staged repository clone is not usable in the sandbox" >&2
      exit 1
    }
    # The repo_changes_summary benchmark summarizes the last 3 commits, so the
    # clone must carry real history: a shallow clone surfaces a single grafted
    # root commit (full tree diff) and produces garbage summaries. CI must
    # checkout with fetch-depth: 0 (workflow checkout step).
    if ! sandbox_exec 'test "$(git -C /workspace/open-chat-go rev-parse --is-shallow-repository)" = "false" && test "$(git -C /workspace/open-chat-go/frontend rev-parse --is-shallow-repository)" = "false"'; then
      echo "::error::pre-staged clone is shallow - 'git log' history is not usable for repo_changes_summary (CI checkout must use fetch-depth: 0, submodules full depth)" >&2
      exit 1
    fi
  fi

  # Optional per-test setup hook: runs after the backend is up, before the
  # interaction. It may create resources via the API (eg a git integration
  # token + gh authentication for the gh_pr_comment test) and write an
  # effective prompt override to $RESULTS_DIR/$TEST_NAME.effective-prompt.txt
  # plus a per-run marker file for deterministic verification.
  if [ -f "$TEST_DIR/setup.sh" ]; then
    echo "[sandbox-test] running setup hook for $TEST_NAME (timeout=${SETUP_TIMEOUT}s)..."
    if ! timeout --foreground --kill-after=15 "$SETUP_TIMEOUT" bash "$TEST_DIR/setup.sh"; then
      echo "::error::setup hook failed (or timed out after ${SETUP_TIMEOUT}s) for $TEST_NAME" >&2
      exit 1
    fi
  fi

  PROMPT="$(tr -d '\n\r' < "$TEST_DIR/prompt.txt")"
  # setup.sh may replace the static prompt.txt with a rendered effective
  # prompt (placeholder substitution for PR number/repo/marker).
  if [ -f "$RESULTS_DIR/$TEST_NAME.effective-prompt.txt" ]; then
    PROMPT="$(tr -d '\n\r' < "$RESULTS_DIR/$TEST_NAME.effective-prompt.txt")"
  fi
  # setup.sh may upload attachments and record them as a JSON array of
  # {"file_id",...} objects; pass them through to the interaction endpoint.
  ATTACHMENTS_JSON=""
  if [ -f "$RESULTS_DIR/$TEST_NAME.attachments.json" ]; then
    ATTACHMENTS_JSON="$(cat "$RESULTS_DIR/$TEST_NAME.attachments.json")"
  fi
  # setup.sh may write per-run interaction overrides (top-level request fields,
  # eg {"config_overrides":{"tools":[...]},"tool_init":{...}}) to exercise
  # dynamic rest_api tools the way the little-world integration does; pass them
  # through to the interaction.
  CONFIG_OVERRIDES_JSON=""
  if [ -f "$RESULTS_DIR/$TEST_NAME.config-overrides.json" ]; then
    CONFIG_OVERRIDES_JSON="$(cat "$RESULTS_DIR/$TEST_NAME.config-overrides.json")"
  fi
  # setup.sh may pre-create the interaction chat (two-phase): it writes the chat
  # uuid to $TEST_NAME.prechat after binding runtime state the bot reads at reply
  # time. The prompt is then sent into that existing chat instead of creating a
  # new one. Used by the native-coder MCP/hive tests, whose tools the coder
  # integration only attaches at workspace-bind time (never mid-turn).
  EXISTING_CHAT=""
  if [ -f "$RESULTS_DIR/$TEST_NAME.prechat" ]; then
    EXISTING_CHAT="$(tr -d '\r\n' < "$RESULTS_DIR/$TEST_NAME.prechat")"
  fi
  echo "[sandbox-test] running interaction for $TEST_NAME (bot=${BOT_USER}, timeout=${INTERACTION_TIMEOUT}s, existing_chat=${EXISTING_CHAT:-none})..."
  set +e
  interaction_run_and_fetch "$BOT_USER" "$PROMPT" "$INTERACTION_TIMEOUT" "$OUT_FILE" "${ATTACHMENTS_JSON:-}" "${CONFIG_OVERRIDES_JSON:-}" "${EXISTING_CHAT:-}"
  INTERACTION_RC=$?
  set -e
  STATUS_VERIFY="failed"
  # Capture the verify.sh transcript (including the `[verify] ✗ ...` assertion
  # lines emitted by lib/assert.sh) into a result artifact. The private
  # regression guard (ci#479) excerpts the failed assertions from this file;
  # it is uploaded alongside the other per-test artifacts.
  VERIFY_LOG="$RESULTS_DIR/$TEST_NAME.verify.txt"
  : > "$VERIFY_LOG"
  if [ "$INTERACTION_RC" -eq 0 ]; then
    # tee the transcript to the job log *and* the artifact while preserving
    # verify.sh's real exit status (pipefail would otherwise report tee's).
    set +e
    timeout --foreground --kill-after=15 "$VERIFY_TIMEOUT" bash "$TEST_DIR/verify.sh" 2>&1 | tee -a "$VERIFY_LOG"
    VERIFY_RC="${PIPESTATUS[0]}"
    set -e
    if [ "$VERIFY_RC" -eq 0 ]; then
      STATUS_VERIFY="passed"
    fi
  fi
  # Token usage (best effort): aggregate the opencode session token counts
  # from the server-stored session export while the stack is still running.
  fetch_session_tokens "$TEST_NAME" >/dev/null 2>&1 || true
  if [ "$STATUS_VERIFY" = "passed" ]; then
    STATUS="passed"
  fi
  capture_backend_logs "$RESULTS_DIR/$TEST_NAME.backend-logs.txt"
  if grep -qi "no injectable model provider configuration" "$RESULTS_DIR/$TEST_NAME.backend-logs.txt" 2>/dev/null; then
    echo "::warning::opencode runtime had no injectable model providers (check LITELLM_API_HOST/KEY env)" >&2
  fi
fi

mkdir -p "$RESULTS_DIR"
# Note: OUT_FILE is a JSON dump; DETAIL falls back to the last line of the file.
# REPLY is only set when the interaction ran; guard for skipped runs (set -u).
DETAIL="${REPLY:-}"
MODEL_TAG="${SANDBOX_OPENCODE_PROVIDER:-openrouter}/${SANDBOX_OPENCODE_MODEL:-deepseek/deepseek-v4-flash-0731}"
RUN_ELAPSED=$(( $(date +%s) - RUN_START ))

# Opencode runtime/LLM diagnostics for the report: finish reason, model,
# provider and any error flag from the bot's last message metadata.
OPENCODE_FINISH=""
OPENCODE_COST=""
OPENCODE_ERROR="false"
OPENCODE_ERROR_DETAIL=""
if [ -f "${OUT_FILE%.out}.last_bot.json" ]; then
  OPENCODE_FINISH="$(jq -r '.last_bot.meta.opencode.finish // empty' "${OUT_FILE%.out}.last_bot.json" 2>/dev/null || true)"
  OPENCODE_COST="$(jq -r '.last_bot.meta.opencode.cost // empty' "${OUT_FILE%.out}.last_bot.json" 2>/dev/null || true)"
  OPENCODE_ERROR="$(jq -r '.last_bot.meta.error // false' "${OUT_FILE%.out}.last_bot.json" 2>/dev/null || true)"
  OPENCODE_ERROR_DETAIL="$(jq -r '.last_bot.text // empty' "${OUT_FILE%.out}.last_bot.json" 2>/dev/null | head -c 300 || true)"
  if [ "$OPENCODE_ERROR" = "true" ]; then
    echo "[sandbox-test] bot reported an error: $OPENCODE_ERROR_DETAIL" >&2
  fi
fi

# Per-test run configuration manifest (no secrets: provider/model names, image
# refs, timings, statuses only). Uploaded alongside the test artifacts.
jq -n \
  --arg test "$TEST_NAME" \
  --arg provider "${SANDBOX_OPENCODE_PROVIDER:-openrouter}" \
  --arg model "${SANDBOX_OPENCODE_MODEL:-deepseek/deepseek-v4-flash-0731}" \
  --arg backend_image "${BACKEND_IMAGE:-}" \
  --arg sandbox_image "${SANDBOX_IMAGE_URL:-}" \
  --arg permission_mode "${OPENCODE_PERMISSION_MODE:-}" \
  --arg runtime "$TEST_RUNTIME" \
  --arg coder_permission_mode "${CODER_PERMISSION_MODE:-allow}" \
  --arg temperature "${OPENCODE_TEMPERATURE:-0}" \
  --arg no_progress_timeout "${OPENCODE_NO_PROGRESS_TIMEOUT_SECONDS:-240}" \
  --arg interaction_timeout "$INTERACTION_TIMEOUT" \
  --arg started_at "$(date -u -d "@${RUN_START}" +%FT%TZ)" \
  --arg finished_at "$(date -u +%FT%TZ)" \
  --arg status "$STATUS" \
  --arg model_tag "$MODEL_TAG" \
  --arg chat_uuid "${CHAT_UUID:-}" \
  --arg opencode_finish "$OPENCODE_FINISH" \
  --arg opencode_cost "$OPENCODE_COST" \
  --arg opencode_error "$OPENCODE_ERROR" \
  --arg opencode_error_detail "$OPENCODE_ERROR_DETAIL" \
  --arg tokens_in "${TOKENS_IN:-}" \
  --arg tokens_out "${TOKENS_OUT:-}" \
  --argjson duration "$RUN_ELAPSED" \
  '{test_name: $test, provider: $provider, model: $model, model_tag: $model_tag,
    backend_image: $backend_image, sandbox_image_url: $sandbox_image,
    runtime: $runtime,
    coder_permission_mode: $coder_permission_mode,
    opencode_permission_mode: $permission_mode,
    opencode_temperature: ($temperature | tonumber),
    no_progress_timeout_seconds: ($no_progress_timeout | tonumber),
    interaction_timeout_seconds: ($interaction_timeout | tonumber),
    started_at: $started_at, finished_at: $finished_at,
    chat_uuid: $chat_uuid, status: $status, duration_seconds: $duration,
    opencode_finish: $opencode_finish, opencode_cost: $opencode_cost,
    opencode_error: $opencode_error, opencode_error_detail: $opencode_error_detail,
    tokens_in: (if $tokens_in == "" then null else ($tokens_in | tonumber) end),
    tokens_out: (if $tokens_out == "" then null else ($tokens_out | tonumber) end)}' \
  > "$RESULTS_DIR/$TEST_NAME.run_config.json"

# The TSV must stay single-line: detail texts (e.g. the repo_changes_summary
# benchmark output) can be multi-line markdown, which would otherwise break
# the tabular summary parsing (cut/awk treat newlines as row separators).
# Column 6 carries the aggregated opencode token usage of the run
# ("in:<n> out:<n>", or "-" when the session export was unavailable).
TOKENS_TAG="-"
if [ -n "${TOKENS_IN:-}" ] && [ -n "${TOKENS_OUT:-}" ]; then
  TOKENS_TAG="in:${TOKENS_IN} out:${TOKENS_OUT}"
fi
printf '%s\t%s\t%s\t%ss\t%s\t%s\n' "$TEST_NAME" "$STATUS" \
  "$(printf '%s' "$DETAIL" | tr '\t\r\n' '   ')" "$RUN_ELAPSED" "$MODEL_TAG" "$TOKENS_TAG" | tee "$RESULTS_DIR/$TEST_NAME.tsv"

rm -f "$COOKIE_FILE"
[ "$STATUS" = "passed" ] || { echo "::error::sandbox test '$TEST_NAME' failed." >&2; exit 1; }
