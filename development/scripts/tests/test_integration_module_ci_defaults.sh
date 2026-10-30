#!/usr/bin/env bash
# Self-test for the integration-module CI env-var defaults (ci#423).
#
# The decoupled integration build is the per-integration prebuild precondition
# (ci#345 design), and its decoupling guard must be a *hard gate*: a module
# whose `replace backend` cleanup landed must not silently re-couple to
# backend/... and keep building. The script pins that contract with
# REQUIRE_DECOUPLED defaulting to DECOUPLED.
#
# Hermetic: no Go toolchain and no network. The script is never fully executed;
# its environment parsing is pinned by running it with the required env vars set
# and a `go` stub on PATH (it exits at the INTEGRATION_SRC check, before any
# `go` invocation).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/development/scripts/integration_module_ci.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- stub `go` (never called: the parse exits before any go invocation) ------
mkdir -p "$TMP/bin"
cat > "$TMP/bin/go" <<'STUB'
#!/usr/bin/env bash
echo "stub go must never run" >&2
exit 99
STUB
chmod +x "$TMP/bin/go"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# parse_env <decoupled> <require_decoupled> - run the script's env parsing and
# print the resolved values. The script exits right after the parse because the
# required INTEGRATION_SRC/WORKSPACE_PATH point at absent directories.
parse_env() {
  local decoupled="$1" require_decoupled="$2"
  local out rc
  out="$(
    INTEGRATION_SRC="$TMP/absent-src" \
    WORKSPACE_PATH="$TMP/absent-workspace" \
    DECOUPLED="$decoupled" \
    REQUIRE_DECOUPLED="$require_decoupled" \
    PATH="$TMP/bin:$PATH" \
    bash "$SCRIPT" 2>&1
  )"
  rc=$?
  if [ "$rc" -ne 1 ]; then
    fail "parse must exit 1 (absent INTEGRATION_SRC), got $rc: $out"
  fi
  printf '%s\n' "$out"
}

# --- the mapping under test ---------------------------------------------------
# REQUIRE_DECOUPLED defaults to DECOUPLED, so a decoupled build is a hard gate
# while a non-decoupled build stays report-only (ci#423 incremental migration).
out="$(parse_env true "")"
grep -q 'REQUIRE_DECOUPLED=true' <<<"$out" || fail "DECOUPLED=true must default REQUIRE_DECOUPLED=true: $out"

out="$(parse_env false "")"
grep -q 'REQUIRE_DECOUPLED=false' <<<"$out" || fail "DECOUPLED=false must default REQUIRE_DECOUPLED=false: $out"

# An explicit REQUIRE_DECOUPLED always wins over the DECOUPLED default.
out="$(parse_env true false)"
grep -q 'REQUIRE_DECOUPLED=false' <<<"$out" || fail "explicit REQUIRE_DECOUPLED=false must win: $out"

out="$(parse_env false true)"
grep -q 'REQUIRE_DECOUPLED=true' <<<"$out" || fail "explicit REQUIRE_DECOUPLED=true must win: $out"

# The decoupled path is opt-in (DECOUPLED still defaults to false).
out="$(parse_env "" "")"
grep -q 'DECOUPLED=false' <<<"$out" || fail "DECOUPLED must default to false: $out"
grep -q 'REQUIRE_DECOUPLED=false' <<<"$out" || fail "REQUIRE_DECOUPLED must default to false for a non-decoupled build: $out"

printf 'integration_module_ci defaults self-test: ok\n'