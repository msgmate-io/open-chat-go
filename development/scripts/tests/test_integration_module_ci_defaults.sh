#!/usr/bin/env bash
# Self-test for the REQUIRE_DECOUPLED / DECOUPLED default mapping in
# development/scripts/integration_module_ci.sh (ci#423 step 6).
#
# The guarantee this pins down: a decoupled build (`DECOUPLED=true`) enforces
# the decoupling guard as a hard gate, while a non-decoupled build stays
# report-only while the ci#423 migration is incremental. The mapping is
#   REQUIRE_DECOUPLED defaults to DECOUPLED.
#
# The test is hermetic: it never installs a Go toolchain and makes no network
# calls, so the caller script is driven only up to the decision the mapping
# makes. The caller itself measures formatting and runs `go vet`/`go build`/
# `go test` after its decoupling report, so this file does THREE things to stay
# honest about the mapping without a real build:
#
#   - `stub-go` on PATH answers the guard's `go list` for the *preflight* that
#     decides whether the decoupled path is allowed to start; it emits
#     `backend/...` packages when the harness forces coupling.
#   - each case runs the REAL `integration_module_ci.sh` and asserts on the
#     point where the default mapping must have decided: the decoupled build
#     blows up at the preflight while the module is coupled, and reaches the
#     materialization/copy steps on a decoupled module. The toolchain steps
#     after that (gofmt, go vet/build/test) are only ever reached on the
#     decoupled-module case, where the absence of a real toolchain is accepted
#     by the harness (the script's job is decided before them).
#   - the guard itself must be REALLY coupled for the hard-gate case: it needs
#     `replace backend => X` (checked directly off go.mod) or `go list` output
#     containing `backend/...` (stubbed per run) - and a Go toolchain on PATH
#     for the latter, which the stub provides. Where the mapping must *pass*
#     report-only, the module is genuinely decoupled (no replace, stub emits no
#     backend/... on the report run).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/development/scripts/integration_module_ci.sh"
GUARD="$REPO_ROOT/development/scripts/check_integration_decoupling.py"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# --- stub Go toolchain ------------------------------------------------------
# The guard forks `go`. This stub answers `go version` and `go list` so the
# script's preflight (which only wants the guard's verdict) has a binary to
# call; it is parameterized by the environment of each run.
STUB_BIN="$TMP/bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  version)
    echo "go version go1.24 linux/amd64"
    ;;
  list)
    # The default payload is the call-site module itself (stub/src/go.mod sets
    # module "example.com/stub"): no backend/... => the graph is decoupled.
    # Force coupling per-run via GO_STUB_COUPLED=1.
    if [ -n "${GO_STUB_COUPLED:-}" ]; then
      printf '%s\n' "example.com/stub" "backend/database" "backend/server/util"
    else
      printf '%s\n' "example.com/stub"
    fi
    ;;
  *)
    echo "stub-go: preserved script past toolchain step ($*)"
    exit 0
    ;;
esac
STUB
chmod +x "$STUB_BIN/go"

run_script() { # run_script <workdir> [env VAR=value ...]
  local workdir="$1"; shift
  set +e
  OUT="$(cd "$workdir" && env "$@" \
    PATH="$STUB_BIN:$PATH" \
    INTEGRATION_SRC="$TMP/src" \
    WORKSPACE_PATH="$TMP/mapped" \
    DECOUPLED=true \
    bash "$SCRIPT" 2>&1)"
  RC=$?
  set -e
}

# --- the minimal, genuinely decoupled integration module --------------------
# The stub sits at $TMP/src with a real go.mod/package (and a go.sum) so the
# caller script's INTEGRATION_SRC validation and its "module must ship complete
# checksums" check pass, and the script's later `mkdir -p` + rsync copy make
# $TMP/mapped a module too.
mkdir -p "$TMP/src"
cat > "$TMP/src/go.mod" <<'MOD'
module example.com/stub

go 1.24
MOD
cat > "$TMP/src/go.sum" <<'SUM'
# module example.com/stub - self-contained, no external requirements
SUM
mkdir -p "$TMP/src/ping"
cat > "$TMP/src/ping/ping.go" <<'GO'
package ping

func Ping() int { return 42 }
GO

mkdir -p "$TMP/mapped"
mkdir -p "$TMP/bin" # re-created above; keep PATH deterministic

# --- case 1: decoupled build, coupled module -> hard gate trips -------------
# The module still carries `replace backend => ...`: the guard must fail the
# decoupled preflight and the script must exit non-zero even though the report
# would otherwise be tolerated - REQUIRE_DECOUPLED defaults to DECOUPLED=true.
mkdir -p "$TMP/src"
cat > "$TMP/src/go.mod" <<'MOD'
module example.com/stub

go 1.24

replace backend => ../../../backend
MOD
export PATH="$STUB_BIN:$PATH"
export GO_STUB_COUPLED=1
run_script "$TMP" GO_STUB_COUPLED=1
[ "$RC" -ne 0 ] || fail "decoupled build of a coupled module must fail (got rc=$RC):\n$OUT"
echo "$OUT" | grep -q "decoupled build requires a module that no longer imports backend" \
  || fail "hard-gate message missing:\n$OUT"
unset GO_STUB_COUPLED

# --- case 2: decoupled build of a truly decoupled module -> gate passes -----
mkdir -p "$TMP/src"
cat > "$TMP/src/go.mod" <<'MOD'
module example.com/stub

go 1.24
MOD
mkdir -p "$TMP/src/ping"
cat > "$TMP/src/ping/ping.go" <<'GO'
package ping

func Ping() int { return 42 }
GO
export GO_STUB_COUPLED=1 # stale; cleared below before the run
unset GO_STUB_COUPLED
run_script "$TMP"
[ "$RC" -eq 0 ] || fail "decoupled build of a decoupled module must pass (got rc=$RC):\n$OUT"
echo "$OUT" | grep -q "Decoupled build: skipping profile materialization" \
  || fail "decoupled mode not entered:\n$OUT"

# --- case 3: non-decoupled build, coupled module -> report, not fail --------
# DECOUPLED=false: the guard is report-only while the migration is incremental.
# The module is coupled via the stub's forced backend/... emission (no real
# replace needed), so the guard fails; REQUIRE_DECOUPLED defaults to DECOUPLED
# (= false here), so the script reports and continues instead of failing.
set +e
OUT3="$(cd "$TMP" && env \
  GO_STUB_COUPLED=1 \
  INTEGRATION_SRC="$TMP/src" \
  WORKSPACE_PATH="$TMP/mapped" \
  DECOUPLED=false \
  bash "$SCRIPT" 2>&1)"
RC3=$?
set -e
[ "$RC3" -eq 0 ] || fail "non-decoupled build must not fail on coupling (got rc=$RC3):\n$OUT3"
echo "$OUT3" | grep -q "coupling is reported, not enforced" \
  || fail "report-only note missing:\n$OUT3"

printf 'integration_module_ci REQUIRE_DECOUPLED defaults self-test: ok\n'