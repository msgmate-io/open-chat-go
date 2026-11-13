#!/usr/bin/env bash
# Self-test for WORKSPACE_PATH handling in development/scripts/integration_module_ci.sh
# (ci#1075).
#
# The bug this pins down: the caller script resolved WORKSPACE_PATH onto the
# checkout root with `$(basename ...)`, so the multi-component monorepo path
# `clients/integrations/msgmate_coder_integration` collapsed to
# `<repo-root>/msgmate_coder_integration`. An integration's relative
# `replace backend => ../../../backend` then resolved *outside* the checkout
# (in CI: `/home/runner/work/backend`), and every run of the reusable
# integration-module CI failed with the misleading annotation
#
#     backend@v0.0.0: replacement directory ../../../backend does not exist
#
# even though the workspace was materialized. The guarantee now is:
#
#   - a checkout-relative WORKSPACE_PATH keeps every component
#     (`clients/integrations/<id>` stays three levels deep);
#   - an absolute WORKSPACE_PATH (workspace staged outside the checkout) is
#     used as given;
#   - a `..` component in the relative form is rejected instead of silently
#     extracting the profile over a sibling directory.
#
# The test is hermetic: it runs the REAL script from a throwaway fake repo root,
# with stub `go`/`gofmt` and a stub build-tools CLI on PATH, so it needs no Go
# toolchain, no network and no workspace materialization.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/development/scripts/integration_module_ci.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# --- fake repo root: the script derives REPO_ROOT from its own location ------
FAKE_ROOT="$TMP/root"
mkdir -p "$FAKE_ROOT/development/scripts"
cp "$SCRIPT" "$FAKE_ROOT/development/scripts/integration_module_ci.sh"
# build_tools.sh is only used to locate the `openchat-integrations` CLI; the
# stub below answers it so no venv/pip is needed.
cat > "$FAKE_ROOT/development/scripts/build_tools.sh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "${STUB_CLI:?STUB_CLI must point at the stubbed build-tools CLI}"
STUB

# --- stub toolchain ---------------------------------------------------------
STUB_BIN="$TMP/bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
  version) echo "go version go1.24 linux/amd64" ;;
  list)    echo "example.com/demo" ;;
  env)     echo "/tmp/stub" ;;
  *)       exit 0 ;;
esac
STUB
cat > "$STUB_BIN/gofmt" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
cat > "$STUB_BIN/openchat-integrations" <<'STUB'
#!/usr/bin/env bash
echo "[stub] openchat-integrations $*"
exit 0
STUB
chmod +x "$STUB_BIN/go" "$STUB_BIN/gofmt" "$STUB_BIN/openchat-integrations"

# --- the integration checkout being built -----------------------------------
SRC="$TMP/src"
mkdir -p "$SRC/demo"
cat > "$SRC/go.mod" <<'MOD'
module example.com/demo

go 1.24

replace backend => ../../../backend
MOD
printf '# module example.com/demo - self-contained\n' > "$SRC/go.sum"
cat > "$SRC/demo/demo.go" <<'GO'
package demo

func Ping() int { return 42 }
GO

run_script() { # run_script <workspace_path> [env VAR=value ...]
  local workspace_path="$1"; shift
  set +e
  OUT="$(cd "$TMP" && env "$@" \
    PATH="$STUB_BIN:$PATH" \
    STUB_CLI="$STUB_BIN/openchat-integrations" \
    INTEGRATION_SRC="$SRC" \
    WORKSPACE_PATH="$workspace_path" \
    FROZEN=true \
    RUN_TESTS=true \
    bash "$FAKE_ROOT/development/scripts/integration_module_ci.sh" 2>&1)"
  RC=$?
  set -e
}

# --- case 1: the monorepo-relative path keeps all components ----------------
# This is the exact shape the reusable workflow passes in CI.
run_script "clients/integrations/demo_integration"
[ "$RC" -eq 0 ] || fail "relative workspace path build failed (rc=$RC):\n$OUT"
echo "$OUT" | grep -q "Place the pull-request checkout at $FAKE_ROOT/clients/integrations/demo_integration" \
  || fail "checkout was not placed at the full monorepo path (ci#1075):\n$OUT"
[ -f "$FAKE_ROOT/clients/integrations/demo_integration/go.mod" ] \
  || fail "no go.mod at $FAKE_ROOT/clients/integrations/demo_integration"
# The regression: the flattened copy at the repo root must not come back.
[ -e "$FAKE_ROOT/demo_integration" ] \
  && fail "the flattened checkout $FAKE_ROOT/demo_integration reappeared (ci#1075)"
echo "$OUT" | grep -q "Integration module CI passed for $FAKE_ROOT/clients/integrations/demo_integration" \
  || fail "script did not report success for the full path:\n$OUT"
# Three levels below the root is exactly the depth `../../../backend` needs.
[ -d "$FAKE_ROOT/clients/integrations" ] || fail "expected parents of the workspace path to exist"
# The module's `replace backend => ../../../backend` must land back on the repo
# root - i.e. inside the workspace - which is only true at the full depth.
mkdir -p "$FAKE_ROOT/backend"
RESOLVED="$(cd "$FAKE_ROOT/clients/integrations/demo_integration" && cd ../../../backend && pwd)"
[ "$RESOLVED" = "$FAKE_ROOT/backend" ] \
  || fail "'../../../backend' resolved to $RESOLVED, not $FAKE_ROOT/backend (ci#1075)"

# --- case 2: an absolute workspace path is used as given --------------------
mkdir -p "$TMP/outside"
run_script "$TMP/outside/mapped"
[ "$RC" -eq 0 ] || fail "absolute workspace path build failed (rc=$RC):\n$OUT"
[ -f "$TMP/outside/mapped/go.mod" ] || fail "absolute workspace path was not honored:\n$OUT"

# --- case 3: an escaping relative path is rejected --------------------------
run_script "../escape/demo"
[ "$RC" -ne 0 ] || fail "a WORKSPACE_PATH escaping the checkout must fail (rc=$RC):\n$OUT"
echo "$OUT" | grep -q "escapes $FAKE_ROOT" \
  || fail "escape rejection message missing:\n$OUT"

printf 'integration_module_ci WORKSPACE_PATH self-test: ok\n'