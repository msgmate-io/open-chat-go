#!/usr/bin/env bash
# ci#1038: pin the acceptance line of the ticket instead of narrating it.
#
#   "in a fresh `repo:open-chat-go` sandbox, `go version && go build ./...`
#    succeeds offline after checkout"
#
# `check_sandbox_go_offline_build.sh` is the script an implementer runs before
# pushing, so its three verdicts must be impossible to confuse:
#
#   go: MISSING      -> no toolchain in this shell; the command is NOT run.
#   go_work: MISSING -> the toolchain works but the gitignored profile workspace
#                       (`backend/go.work`) was never materialised, which is a
#                       provisioning gap and NOT a code defect to "fix" by hand.
#   go_build: OK/FAILED -> the build really ran, offline, and its status is the
#                       script's status.
#
# The suite is hermetic: a stub `go` stands in for the toolchain, so the real
# Go version installed on the runner can never change the expectations.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/check_sandbox_go_offline_build.sh"
BASH_BIN="$(command -v bash)"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}
check_contains() {
  case "$2" in
    *"$3"*) : ;;
    *) fail "$1 (missing: $3); got: $2" ;;
  esac
}

[ -x "$SCRIPT" ] || fail "check_sandbox_go_offline_build.sh must exist and be executable"
bash -n "$SCRIPT" 2>/dev/null || fail "check_sandbox_go_offline_build.sh must be valid bash"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Hermetic `go` stub. It records the argv and the offline-relevant environment,
# then answers `version` / `build` from STUB_* variables so every verdict below
# is reproducible regardless of the runner's real toolchain.
stub_bin="$tmp/bin"
mkdir -p "$stub_bin"
cat >"$stub_bin/go" <<'STUB'
#!/bin/sh
{
  printf 'args=%s\n' "$*"
  env | grep -E '^(GOPROXY|GOFLAGS|GOTOOLCHAIN|GOWORK)=' || true
  printf 'cwd=%s\n' "$PWD"
} >>"$STUB_GO_LOG"
case "${1:-}" in
  version)
    printf 'go version go%s linux/amd64\n' "${STUB_GO_VERSION:-1.24.13}"
    exit 0
    ;;
  build)
    [ -n "${STUB_BUILD_MARKER:-}" ] && : >"$STUB_BUILD_MARKER"
    if [ "${STUB_BUILD_RC:-0}" -ne 0 ]; then
      printf 'api/msgmate/dynamic_rest_tools.go:8:2: %s\n' \
        "${STUB_BUILD_STDERR:-compile broke}" >&2
      exit "${STUB_BUILD_RC}"
    fi
    printf 'BUILD-OUTPUT-OK\n'
    exit 0
    ;;
esac
exit 0
STUB
chmod +x "$stub_bin/go"

# A hermetic bin holding only the utilities the script itself needs. Using the
# runner's PATH would make the "no toolchain" case depend on whether the runner
# happens to install Go, which is exactly the ambiguity this suite removes.
hermetic_bin="$tmp/hermetic-bin"
mkdir -p "$hermetic_bin"
for tool in bash sh env sed grep head tail cat cut tr sort dirname basename \
  mktemp rm mkdir timeout printf ls; do
  src="$(command -v "$tool" 2>/dev/null)" || continue
  [ -n "$src" ] && ln -sf "$src" "$hermetic_bin/$tool"
done

# A minimal checkout: backend/go.mod carries the `go` directive the toolchain
# has to satisfy; backend/go.work is the gitignored profile workspace.
fixture="$tmp/fixture"
mkdir -p "$fixture/backend"
printf 'module example.com/fixture\n\ngo 1.24.13\n' >"$fixture/backend/go.mod"

log="$tmp/stub-go.log"
run() {
  # run <path> <extra-args...>; STUB_* variables come from the caller's env.
  local path="$1"
  shift
  env PATH="$path" HOME="$tmp" TMPDIR="$tmp" STUB_GO_LOG="$log" \
    "$BASH_BIN" "$SCRIPT" "$@"
}

# 1. No toolchain in reach: the build must NOT be attempted, and the verdict has
#    to be the machine-readable pair an agent is required to quote.
echo "== ci#1038: no go on PATH -> missing_cli + go: MISSING, build not run =="
: >"$log"
out="$(run "$hermetic_bin" "$fixture" 2>&1)"
rc=$?
[ "$rc" -eq 1 ] || fail "a missing toolchain must exit 1 (got $rc); got: $out"
check_contains "missing toolchain must emit missing_cli: go" "$out" "missing_cli: go"
check_contains "missing toolchain must emit 'go: MISSING'" "$out" "go: MISSING"
check_contains "missing toolchain must tell the reader not to claim a run" \
  "$out" "Do not claim a local build/test run"
case "$out" in
  *"go_build: OK"*|*"go_build: FAILED"*)
    fail "a missing toolchain must not report any build verdict; got: $out" ;;
  *) : ;;
esac

# 2. Toolchain present, profile workspace absent: the build runs (offline, cwd =
#    backend/) and the missing go.work is reported as provisioning guidance, not
#    as a hard failure - an agent must keep the change and let PR CI gate it.
echo "== ci#1038: no backend/go.work -> go_work: MISSING guidance + build still runs =="
: >"$log"
marker="$tmp/built"
out="$(STUB_BUILD_MARKER="$marker" run "$stub_bin:$hermetic_bin" "$fixture" 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || fail "a missing go.work alone must not fail the check (got $rc); got: $out"
check_contains "the missing profile workspace must be named" "$out" "go_work: MISSING"
check_contains "the recovery hint must name the profile prepare command" \
  "$out" "openchat-integrations prepare --profile core-only"
check_contains "the recovery hint must say the workspace has to be materialised" \
  "$out" "Materialise the profile workspace"
check_contains "the recovery hint must offer the submodule-free bootstrapper" \
  "$out" "development/scripts/build_tools.sh"
check_contains "a successful offline build must be reported verbatim" "$out" "go_build: OK"
check_contains "the verdict must be explicit" "$out" "offline Go build verified"
[ -f "$marker" ] || fail "the build must actually run when the toolchain is present"

# 3. The build must be offline by construction, and must run in backend/ where
#    go.mod lives. A network-capable build would hide a cold module cache.
echo "== ci#1038: the build runs offline (GOPROXY=off, -mod=readonly, GOTOOLCHAIN=local) =="
check_contains "GOPROXY=off must be exported" "$(cat "$log")" "GOPROXY=off"
check_contains "GOFLAGS=-mod=readonly must be exported" "$(cat "$log")" "GOFLAGS=-mod=readonly"
check_contains "GOTOOLCHAIN=local must be exported" "$(cat "$log")" "GOTOOLCHAIN=local"
check_contains "the build must run in backend/" "$(cat "$log")" "cwd=$fixture/backend"

# 4. --strict-work turns the provisioning gap into a hard failure for the caller
#    that does want a fully materialised workspace.
echo "== ci#1038: --strict-work promotes go_work: MISSING to a failure =="
out="$(STUB_BUILD_MARKER="$tmp/built-strict" run "$stub_bin:$hermetic_bin" "$fixture" --strict-work 2>&1)"
rc=$?
[ "$rc" -eq 1 ] || fail "--strict-work must exit 1 without backend/go.work (got $rc); got: $out"

# 5. With the profile workspace present there is no go_work warning at all.
echo "== ci#1038: backend/go.work present -> no go_work warning =="
printf 'go 1.24.13\n\nuse .\n' >"$fixture/backend/go.work"
out="$(run "$stub_bin:$hermetic_bin" "$fixture" 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || fail "a prepared workspace must pass (got $rc); got: $out"
case "$out" in
  *"go_work: MISSING"*) fail "a prepared workspace must not report go_work: MISSING" ;;
  *) : ;;
esac
rm -f "$fixture/backend/go.work"

# 6. A red build is red: the status propagates, the compiler output is quoted,
#    and an unresolved-module failure is attributed to the profile workspace
#    instead of inviting a go.mod/go.sum hand-edit.
echo "== ci#1038: failing build -> go_build: FAILED + exit 1 + profile hint =="
out="$(STUB_BUILD_RC=1 \
  STUB_BUILD_STDERR='missing go.sum entry for module providing package example.com/x' \
  run "$stub_bin:$hermetic_bin" "$fixture" 2>&1)"
rc=$?
[ "$rc" -eq 1 ] || fail "a failing build must exit 1 (got $rc); got: $out"
check_contains "a failing build must be reported as FAILED" "$out" "go_build: FAILED"
check_contains "the compiler output must be quoted, not summarised" "$out" "dynamic_rest_tools.go:8:2"
check_contains "the missing-go.sum case must be attributed to the profile workspace" \
  "$out" "this is the unresolved-profile-workspace case (go_work: MISSING)"
check_contains "the red verdict must be explicit" "$out" "NOT verified"

# 7. Usage errors are usage errors, not readiness verdicts.
echo "== ci#1038: usage errors exit 2, --help documents the acceptance line =="
"$BASH_BIN" "$SCRIPT" --bogus "$fixture" >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "an unknown option must exit 2 (got $rc)"
"$BASH_BIN" "$SCRIPT" "$tmp/no-such-root" >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "a missing repo root must exit 2 (got $rc)"
help_out="$("$BASH_BIN" "$SCRIPT" --help 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || fail "--help must exit 0 (got $rc)"
check_contains "--help must quote the acceptance line" "$help_out" "go version && go build ./..."
check_contains "--help must document --strict-work" "$help_out" "--strict-work"

if [ "$failures" -ne 0 ]; then
  printf '%d check(s) failed\n' "$failures" >&2
  exit 1
fi
echo "ok: offline Go build acceptance check contract (ci#1038)"
