#!/usr/bin/env bash
# ci#1038: pin the contract of the coding-sandbox Go toolchain provisioning.
#
# The ticket: the per-chat sandbox had no `go`, so implementation dispatches could
# not run a single local build or test, and the red check
# (TestTruncateToolResultForToolReboundsGitEnvelope) was only ever found in CI.
# `9e35c5b` made the *probe* shout about a missing toolchain; this test pins the
# half that actually provisions it, because the whole point of the ticket is that
# nothing here can be verified by narrative:
#
#   1. the installer is valid bash and idempotent about the ~/.bashrc block;
#   2. the pinned version comes from backend/go.mod's `go` directive, never from a
#      hard-coded constant that can drift;
#   3. GOTOOLCHAIN=local and the module cache are part of the contract (a
#      toolchain that silently downloads another one is not offline-capable);
#   4. `--check` is read-only and says `missing_cli: go` + `go: MISSING` when no
#      toolchain is visible, so an agent can never honestly claim a test run;
#   5. a bare sandbox that has Go off-PATH is repaired via PATH, not installed
#      twice (the ci#739 failure mode).
#
# Runs offline: the download branch is deliberately not exercised (the sandbox and
# PR CI both lack a guarantee of network access to go.dev); the PATH-repair and
# missing-toolchain branches cover the interesting logic.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/install_sandbox_go_toolchain.sh"
PROBE="$ROOT/development/scripts/check_coder_sandbox_readiness.sh"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

[ -x "$SCRIPT" ] || fail "install_sandbox_go_toolchain.sh must exist and be executable"
[ -f "$SCRIPT" ] || { echo "cannot continue without $SCRIPT" >&2; exit 1; }

# 1. Valid bash.
if ! bash -n "$SCRIPT" 2>/dev/null; then
  fail "install_sandbox_go_toolchain.sh must be valid bash"
fi
if ! bash -n "$PROBE" 2>/dev/null; then
  fail "check_coder_sandbox_readiness.sh must stay valid bash"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# 2. The toolchain version must be derived from the module, not hard-coded. A
#    synthetic repo with a distinctive directive proves the wiring.
fake_repo="$tmp/repo"
mkdir -p "$fake_repo/backend" "$fake_repo/development/scripts"
cp "$SCRIPT" "$fake_repo/development/scripts/install_sandbox_go_toolchain.sh"
printf 'module example.com/fake\n\ngo 1.99.7\n' > "$fake_repo/backend/go.mod"
out="$(bash "$fake_repo/development/scripts/install_sandbox_go_toolchain.sh" --check "$fake_repo" 2>&1)"
case "$out" in
  *"go 1.99.7"*) : ;;
  *) fail "installer must report the backend/go.mod 'go' directive (go 1.99.7); got: $out" ;;
esac
grep -q 'required (backend/go.mod)' <<<"$out" \
  || fail "installer must report which module file pinned the toolchain"

# 3. A missing toolchain is reported machine-readably and exits non-zero. The
#    hint directory is empty so a runner that happens to have Go in the default
#    location still simulates a bare sandbox.
out="$(PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
       OPENCHAT_SANDBOX_GO_HINT_DIR="$tmp/nope" \
       bash "$fake_repo/development/scripts/install_sandbox_go_toolchain.sh" --check "$fake_repo" 2>&1)"
rc=$?
[ "$rc" -ne 0 ] || fail "--check must exit non-zero when no Go toolchain is present (got $rc)"
case "$out" in
  *"missing_cli: go"*) : ;;
  *) fail "--check must emit the machine-readable 'missing_cli: go' token" ;;
esac
case "$out" in
  *"go: MISSING"*) : ;;
  *) fail "--check must emit 'go: MISSING' so a claim of a test run is impossible" ;;
esac
case "$out" in
  *"PR CI"*) : ;;
  *) fail "--check must name PR CI as the fallback gate" ;;
esac
# --check must stay read-only: it may not write a ~/.bashrc block.
[ -f "$HOME/.bashrc" ] && before="$(grep -c 'openchat sandbox environment' "$HOME/.bashrc" 2>/dev/null || true)" || before=""
out="$(PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
       OPENCHAT_SANDBOX_GO_HINT_DIR="$tmp/nope" \
       bash "$fake_repo/development/scripts/install_sandbox_go_toolchain.sh" --check "$fake_repo" 2>&1)" || true
if [ -f "$HOME/.bashrc" ]; then
  after="$(grep -c 'openchat sandbox environment' "$HOME/.bashrc" 2>/dev/null || true)"
  [ "$before" = "$after" ] || fail "--check must not mutate ~/.bashrc"
fi

# 4. Usage errors are usage errors, not readiness verdicts.
bash "$SCRIPT" --not-a-flag >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "an unknown option must exit 2 (got $rc)"
bash "$SCRIPT" --check "$tmp/does-not-exist" >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "a missing repo root must exit 2 (got $rc)"

# 5. PATH repair: a toolchain that exists off-PATH must be adopted, not reinstalled.
if command -v go >/dev/null 2>&1; then
  real_go="$(command -v go)"
  fake_prefix="$(dirname "$(dirname "$real_go")")"
  out="$(PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
         OPENCHAT_SANDBOX_GO_HINT_DIR="$fake_prefix/bin" \
         OPENCHAT_SANDBOX_GO_INSTALL_ROOT="$tmp/install" \
         HOME="$tmp/home" \
         bash "$fake_repo/development/scripts/install_sandbox_go_toolchain.sh" --check "$fake_repo" 2>&1)"
  case "$out" in
    *"not on PATH"*) : ;;
    *) fail "installer must repair an off-PATH toolchain instead of reinstalling it; got: $out" ;;
  esac
  case "$out" in
    *"downloading go"*) fail "installer must not download a toolchain that is already present" ;;
    *) : ;;
  esac
else
  printf 'skip: PATH-repair branch (no go on this runner)\n'
fi

# 6. The probe and the installer agree on the token the agent is told to use.
if ! grep -q 'missing_cli' "$PROBE"; then
  fail "the readiness probe must also emit 'missing_cli' (ci#1038)"
fi
if ! grep -q 'go: MISSING' "$PROBE"; then
  fail "the readiness probe must also emit 'go: MISSING' (ci#1038)"
fi

if [ "$failures" -ne 0 ]; then
  printf '%d check(s) failed\n' "$failures" >&2
  exit 1
fi
echo "ok: coding-sandbox Go toolchain provisioning contract (ci#1038)"
