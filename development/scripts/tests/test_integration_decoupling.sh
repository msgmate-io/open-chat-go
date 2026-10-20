#!/usr/bin/env bash
# Self-test for development/scripts/check_integration_decoupling.py (ci#423).
#
# The guard is the *falsifiable* part of the ticket: "the integration no longer
# imports backend/...". This test pins its three outcomes without needing a Go
# toolchain, by putting a stub `go` on PATH:
#
#   - go.mod with `replace backend => ...`              -> exit 1 (coupled)
#   - no replace, `go list` reports backend/... packages -> exit 1 (coupled)
#   - no replace, `go list` reports no backend/...       -> exit 0 (decoupled)
#   - not a Go module                                    -> exit 2 (usage error)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
GUARD="$REPO_ROOT/development/scripts/check_integration_decoupling.py"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- stub `go` ------------------------------------------------------------
STUB_BIN="$TMP/bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
# Emits the package list configured via GO_STUB_PACKAGES (one per line).
if [ -n "${GO_STUB_FAIL:-}" ]; then
  echo "stub go: forced failure" >&2
  exit 1
fi
printf '%s\n' ${GO_STUB_PACKAGES:-}
STUB
chmod +x "$STUB_BIN/go"
export PATH="$STUB_BIN:$PATH"
export GOWORK=off

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

run_guard() { # run_guard <dir> [extra args...]
  local dir="$1"; shift
  set +e
  OUT="$(python3 "$GUARD" --module-dir "$dir" "$@" 2>&1)"
  RC=$?
  set -e
}

# --- case 1: replace backend directive is enough to be coupled -------------
CASE1="$TMP/replace"
mkdir -p "$CASE1"
printf 'module example.com/one\n\ngo 1.24\n\nreplace backend => ../../../backend\n' > "$CASE1/go.mod"
export GO_STUB_PACKAGES="example.com/one fmt"
run_guard "$CASE1"
[ "$RC" -eq 1 ] || fail "replace backend must exit 1 (got $RC): $OUT"
grep -q "go.mod still replaces" <<<"$OUT" || fail "case 1 message missing: $OUT"

# --- case 2: no replace but backend/... in the shipped graph ---------------
CASE2="$TMP/imports"
mkdir -p "$CASE2"
printf 'module example.com/two\n\ngo 1.24\n' > "$CASE2/go.mod"
export GO_STUB_PACKAGES="example.com/two backend/database backend/server/util"
run_guard "$CASE2"
[ "$RC" -eq 1 ] || fail "backend/... import must exit 1 (got $RC): $OUT"
grep -q "backend/database" <<<"$OUT" || fail "case 2 must name backend/database: $OUT"
grep -q "backend/server/util" <<<"$OUT" || fail "case 2 must name backend/server/util: $OUT"

# --- case 3: decoupled module ---------------------------------------------
CASE3="$TMP/clean"
mkdir -p "$CASE3"
printf 'module example.com/three\n\ngo 1.24\n' > "$CASE3/go.mod"
export GO_STUB_PACKAGES="example.com/three github.com/msgmate-io/go-integration-interface/integrationinterface"
run_guard "$CASE3"
[ "$RC" -eq 0 ] || fail "decoupled module must exit 0 (got $RC): $OUT"

# --- case 3b: decoupled module, machine-readable report -------------------
run_guard "$CASE3" --json
[ "$RC" -eq 0 ] || fail "decoupled --json must exit 0 (got $RC): $OUT"
grep -q '"coupled": false' <<<"$OUT" || fail "case 3b json missing coupled:false: $OUT"

# --- case 4: not a module --------------------------------------------------
CASE4="$TMP/notamodule"
mkdir -p "$CASE4"
run_guard "$CASE4"
[ "$RC" -eq 2 ] || fail "non-module dir must exit 2 (got $RC): $OUT"

# --- case 5: unmeasurable graph without a replace is an error -------------
CASE5="$TMP/broken"
mkdir -p "$CASE5"
printf 'module example.com/five\n\ngo 1.24\n' > "$CASE5/go.mod"
export GO_STUB_FAIL=1
run_guard "$CASE5"
[ "$RC" -eq 2 ] || fail "unmeasurable graph must exit 2 (got $RC): $OUT"
unset GO_STUB_FAIL

# --- case 6: the real mcp-style layout is coupled -------------------------
# A `replace backend` line with a version on the left hand side is still a
# backend coupling (paranoid check on the regex).
CASE6="$TMP/versioned"
mkdir -p "$CASE6"
printf 'module example.com/six\n\ngo 1.24\n\nreplace backend v0.0.0 => ../../../backend\n' > "$CASE6/go.mod"
export GO_STUB_PACKAGES="example.com/six"
run_guard "$CASE6"
[ "$RC" -eq 1 ] || fail "versioned replace backend must exit 1 (got $RC): $OUT"

printf 'check_integration_decoupling self-test: ok\n'
