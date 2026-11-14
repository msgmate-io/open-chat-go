#!/usr/bin/env bash
# Self-test for the workspace-path resolution of
# development/scripts/integration_module_ci.sh (ci#1075).
#
# The bug it pins: the reusable integration-module CI collapsed the caller's
# workspace path to its basename, so `clients/integrations/<name>` was built at
# `<repo>/<name>`. Integration modules carry relative `replace` directives
# against the monorepo layout (`backend => ../../../backend`,
# `<interface> => ../../go_integration_interface`), so building them at a
# different depth makes every standalone integration build fail with
#
#     backend@v0.0.0: replacement directory ../../../backend does not exist
#
# which left `msgmate-io/open-chat-go-msgmate-coder` (and every sibling
# integration repo) without a green gate at all. The fix keeps the directory
# structure of the workspace path; this file pins it hermetically (no Go
# toolchain, no network, no profile materialization) by running the REAL script
# and asserting where the PR checkout actually landed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/development/scripts/integration_module_ci.sh"
LIB="$REPO_ROOT/development/scripts/integration_workspace_path.sh"

TMP="$(mktemp -d)"
REL_WS="clients/integrations/ci1075_tmp_$$"
cleanup() {
  rm -rf "$TMP"
  rm -rf "$REPO_ROOT/$REL_WS"
  rm -rf "$REPO_ROOT/$(basename "$REL_WS")"
}
trap cleanup EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# --- part 1: the helper keeps the structure ---------------------------------
# shellcheck source=development/scripts/integration_workspace_path.sh
. "$LIB"

want="$REPO_ROOT/clients/integrations/msgmate_coder_integration"
got="$(integration_workspace_path "$REPO_ROOT" clients/integrations/msgmate_coder_integration)"
[ "$got" = "$want" ] || fail "relative path lost its structure: $got (want $want)"

got="$(integration_workspace_path "$REPO_ROOT" "$want")"
[ "$got" = "$want" ] || fail "absolute path was rewritten: $got (want $want)"

got="$(integration_workspace_path "$REPO_ROOT" ./clients//integrations/./msgmate_coder_integration)"
[ "$got" = "$want" ] || fail "redundant segments were not collapsed: $got (want $want)"

got="$(integration_workspace_path "$REPO_ROOT" clients/integrations/../integrations/msgmate_coder_integration)"
[ "$got" = "$want" ] || fail ".. was not collapsed: $got (want $want)"

got="$(integration_workspace_path "$REPO_ROOT" "$TMP/elsewhere/module")"
[ "$got" = "$TMP/elsewhere/module" ] || fail "out-of-tree absolute path was rewritten: $got"

# --- part 2: the real script places the checkout at the full path ----------
# A stub toolchain answers the formatting/resolution probes so the script runs
# to completion without a Go install; the assertions are about placement.
STUB_BIN="$TMP/bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  version) echo "go version go1.24 linux/amd64" ;;
  # The guard forks `go list`: emitting the module itself only means decoupled.
  list) printf '%s\n' "example.com/stub" ;;
  *) exit 0 ;;
esac
STUB
cat > "$STUB_BIN/gofmt" <<'STUB'
#!/usr/bin/env bash
# `gofmt -l .` must report nothing (clean tree).
exit 0
STUB
chmod +x "$STUB_BIN/go" "$STUB_BIN/gofmt"

# The minimal decoupled module the script copies into the workspace path.
mkdir -p "$TMP/src/ping"
cat > "$TMP/src/go.mod" <<'MOD'
module example.com/stub

go 1.24
MOD
printf '# module example.com/stub - self-contained\n' > "$TMP/src/go.sum"
cat > "$TMP/src/ping/ping.go" <<'GO'
package ping

func Ping() int { return 42 }
GO

run() { # run <env VAR=value ...>
  set +e
  OUT="$(cd "$TMP" && env "$@" \
    PATH="$STUB_BIN:$PATH" \
    INTEGRATION_SRC="$TMP/src" \
    DECOUPLED=true \
    RUN_TESTS=false \
    bash "$SCRIPT" 2>&1)"
  RC=$?
  set -e
}

# -- decoupled build: no materialization, placement is what is measured ------
run WORKSPACE_PATH="$REL_WS"
[ "$RC" -eq 0 ] || fail "script failed for a repo-relative workspace path (rc=$RC):
$OUT"

placed="$REPO_ROOT/$REL_WS"
[ -f "$placed/go.mod" ] || fail "PR checkout was not copied to $placed (ci#1075)"
[ -f "$placed/ping/ping.go" ] || fail "PR checkout is incomplete at $placed"
[ -e "$REPO_ROOT/$(basename "$REL_WS")" ] &&
  fail "PR checkout was placed at the repo root ($REPO_ROOT/$(basename "$REL_WS")); the relative replace depth is broken (ci#1075)"
grep -q "=== Place the pull-request checkout at $placed" <<< "$OUT" ||
  fail "script did not announce the structured workspace path: $OUT"
rm -rf "$placed"

# -- absolute workspace path is honoured verbatim ---------------------------
ABS_WS="$TMP/out/abs_module"
run WORKSPACE_PATH="$ABS_WS"
[ "$RC" -eq 0 ] || fail "script failed for an absolute workspace path (rc=$RC):
$OUT"
[ -f "$ABS_WS/go.mod" ] || fail "PR checkout was not copied to the absolute path $ABS_WS"

printf 'integration_module_ci workspace path (ci#1075) self-test: ok\n'