#!/usr/bin/env bash
# ci#1038 / ci#1044: pin the contract of the coding-sandbox Go toolchain provisioning.
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
# Hermetic by construction (ci#1044). The contract is about what the installer
# does with one *particular* toolchain state, so every invocation runs with
#   * a PATH assembled from symlinks to an allowlisted set of coreutils, so a Go
#     toolchain present on the runner can never leak in. This is the actual bug
#     that blocked two consolidation batches: PR CI runs actions/setup-go, so the
#     "no toolchain at all" case found a real `go` and every `missing_cli`
#     assertion failed, while the off-PATH repair case took the on-PATH branch.
#     The assertions were right; the harness was reading the environment.
#   * a stub `go` we control (`go version`, `go env GOVERSION/GOTOOLCHAIN/
#     GOMODCACHE`) living in a mktemp dir and reachable only through
#     OPENCHAT_SANDBOX_GO_HINT_DIR, exactly like a real off-PATH install;
#   * an isolated HOME, GOPATH and GOMODCACHE under that temp dir;
#   * fixture repos with their own backend/go.mod, addressed by
#     OPENCHAT_SANDBOX_GO_TEST_FIXTURE_ROOT - the real backend/go.mod is never read.
# The verdict is therefore identical with and without a Go toolchain on the host;
# no assertion is relaxed or skipped to get there.
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

# --- hermetic environment ----------------------------------------------------
# A PATH of symlinks to the tools the installer legitimately needs. `go` is not
# on it: whatever the runner happens to ship must not change a single verdict.
hermetic_bin="$tmp/path"
mkdir -p "$hermetic_bin"
for tool in bash sh awk sed sort uname mktemp rm cat mkdir dirname basename \
            grep tr head tail cut date curl tar env ln chmod; do
  resolved="$(command -v "$tool" 2>/dev/null || true)"
  if [ -n "$resolved" ]; then
    ln -sf "$resolved" "$hermetic_bin/$tool"
  fi
done
if [ -e "$hermetic_bin/go" ]; then
  fail "the hermetic PATH must not resolve a go toolchain"
fi

# The stub toolchain. It reports controlled versions so the suite never depends on
# (nor is confused by) the Go a runner ships; it is NOT on the hermetic PATH, so
# only the off-PATH repair path can find it.
stub_root="$tmp/stub-go"
mkdir -p "$stub_root/bin"
cat > "$stub_root/bin/go" <<'STUB'
#!/bin/sh
# Controlled stand-in for an installed Go toolchain (test fixture only).
stub_version="${STUB_GO_VERSION:-1.24.13}"
stub_gotoolchain="${STUB_GO_GOTOOLCHAIN:-local}"
stub_modcache="${STUB_GO_GOMODCACHE:-${GOMODCACHE:-${GOPATH:-$HOME/go}/pkg/mod}}"
case "${1:-}" in
  version) printf 'go version go%s linux/amd64\n' "$stub_version" ;;
  env)
    case "${2:-}" in
      GOVERSION) printf 'go%s\n' "$stub_version" ;;
      GOTOOLCHAIN) printf '%s\n' "$stub_gotoolchain" ;;
      GOMODCACHE) printf '%s\n' "$stub_modcache" ;;
      GOPATH) printf '%s\n' "${GOPATH:-$HOME/go}" ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
STUB
chmod +x "$stub_root/bin/go"
if [ -e "$hermetic_bin/go" ]; then
  fail "the stub toolchain must stay off the hermetic PATH"
fi

# Isolated HOME / GOPATH / GOMODCACHE: --check must not touch the runner's.
sandbox_home="$tmp/home"
sandbox_gopath="$tmp/gopath"
sandbox_modcache="$tmp/gomodcache"
mkdir -p "$sandbox_home" "$sandbox_gopath" "$sandbox_modcache/cache/download"
# A pre-existing ~/.bashrc makes the "--check is read-only" assertion meaningful.
printf '# pre-existing sandbox bashrc\n' > "$sandbox_home/.bashrc"
original_bashrc="$(cat "$sandbox_home/.bashrc")"

# Fixture repos (each with its own backend/go.mod). The root is addressable so a
# developer can point the suite at a different fixture, and it may never be the
# real checkout: the pin must be proven from a synthetic directive.
fixture_root="${OPENCHAT_SANDBOX_GO_TEST_FIXTURE_ROOT:-$tmp/fixture}"
case "$fixture_root" in
  "$ROOT"|"$ROOT"/*)
    fail "the fixture root must not be inside the repository checkout ($fixture_root)"
    fixture_root="$tmp/fixture"
    ;;
esac
for fixture in pinned repair; do
  mkdir -p "$fixture_root/$fixture/backend" "$fixture_root/$fixture/development/scripts"
  cp "$SCRIPT" "$fixture_root/$fixture/development/scripts/install_sandbox_go_toolchain.sh"
done
# `pinned`: a directive no toolchain satisfies, so the wiring is unambiguous and
# the "no toolchain" run cannot accidentally look ready. `repair`: a directive the
# stub satisfies, so the off-PATH repair must end in a ready sandbox.
printf 'module example.com/fake\n\ngo 1.99.7\n' > "$fixture_root/pinned/backend/go.mod"
printf 'module example.com/fake\n\ngo 1.24.0\n'  > "$fixture_root/repair/backend/go.mod"

# run_check <fixture> [VAR=VALUE...] - invoke the fixture's installer --check in a
# fully specified environment (env -i: nothing inherited, nothing to leak).
run_check() {
  local fixture="$1"
  shift
  env -i \
    PATH="$hermetic_bin" \
    HOME="$sandbox_home" \
    GOPATH="$sandbox_gopath" \
    GOMODCACHE="$sandbox_modcache" \
    TMPDIR="$tmp" \
    "$@" \
    bash "$fixture_root/$fixture/development/scripts/install_sandbox_go_toolchain.sh" \
      --check "$fixture_root/$fixture" 2>&1
}

# 2. The toolchain version must be derived from the module, not hard-coded.
#    Case 1 (of the three ci#1044 cases): no toolchain at all.
echo "== case 1/3: no toolchain visible -> machine-readable missing_cli =="
out="$(run_check pinned OPENCHAT_SANDBOX_GO_HINT_DIR="$tmp/no-such-toolchain")"
rc=$?
case "$out" in
  *"go 1.99.7"*) : ;;
  *) fail "installer must report the backend/go.mod 'go' directive (go 1.99.7); got: $out" ;;
esac
case "$out" in
  *"required (backend/go.mod)"*) : ;;
  *) fail "installer must report which module file pinned the toolchain; got: $out" ;;
esac
case "$out" in
  *"present:"*"<none>"*) : ;;
  *) fail "installer must report that no toolchain is present; got: $out" ;;
esac
[ "$rc" -ne 0 ] || fail "--check must exit non-zero when no Go toolchain is present (got $rc)"
case "$out" in
  *"missing_cli: go"*) : ;;
  *) fail "--check must emit the machine-readable 'missing_cli: go' token; got: $out" ;;
esac
case "$out" in
  *"go: MISSING"*) : ;;
  *) fail "--check must emit 'go: MISSING' so a claim of a test run is impossible; got: $out" ;;
esac

# 3. Case 2: the PR-CI fallback must be named, so the only honest gate is visible.
echo "== case 2/3: PR CI named as the fallback gate =="
case "$out" in
  *"PR CI"*) : ;;
  *) fail "--check must name PR CI as the fallback gate; got: $out" ;;
esac

# 4. Case 3: PATH repair. A toolchain that exists off-PATH must be adopted, not
#    reinstalled - and because the stub satisfies the fixture directive, the run
#    must end ready instead of falling into the download branch.
echo "== case 3/3: off-PATH toolchain is repaired, never re-downloaded =="
out="$(run_check repair \
        OPENCHAT_SANDBOX_GO_HINT_DIR="$stub_root/bin" \
        STUB_GO_VERSION=1.24.13)"
rc=$?
[ "$rc" -eq 0 ] || fail "a repaired off-PATH toolchain must end ready (exit 0, got $rc); got: $out"
case "$out" in
  *"not on PATH"*) : ;;
  *) fail "installer must repair an off-PATH toolchain instead of reinstalling it; got: $out" ;;
esac
case "$out" in
  *"downloading go"*) fail "installer must not download a toolchain that is already present" ;;
  *) : ;;
esac
case "$out" in
  *"present:"*1.24.13*) : ;;
  *) fail "installer must report the repaired stub toolchain version (1.24.13); got: $out" ;;
esac
case "$out" in
  *"GOTOOLCHAIN=local"*) : ;;
  *) fail "a repaired toolchain must be usable offline (GOTOOLCHAIN=local); got: $out" ;;
esac
case "$out" in
  *"ready: go 1.24.13"*) : ;;
  *) fail "a repaired toolchain that satisfies backend/go.mod must report ready; got: $out" ;;
esac

# 5. The GOTOOLCHAIN and module-cache legs of the contract are enforceable, which
#    is only possible because the harness - not the runner - owns the toolchain.
out="$(run_check repair \
        OPENCHAT_SANDBOX_GO_HINT_DIR="$stub_root/bin" \
        STUB_GO_VERSION=1.24.13 \
        STUB_GO_GOTOOLCHAIN=auto)"
rc=$?
[ "$rc" -ne 0 ] || fail "--check must reject a non-local GOTOOLCHAIN (got $rc)"
case "$out" in
  *"GOTOOLCHAIN=auto"*) : ;;
  *) fail "--check must report the offending GOTOOLCHAIN=auto; got: $out" ;;
esac

mkdir -p "$tmp/cold-cache"
out="$(run_check repair \
        OPENCHAT_SANDBOX_GO_HINT_DIR="$stub_root/bin" \
        STUB_GO_VERSION=1.24.13 \
        STUB_GO_GOMODCACHE="$tmp/cold-cache")"
rc=$?
[ "$rc" -ne 0 ] || fail "--check must reject an unwarmed module cache (got $rc)"
case "$out" in
  *"no warmed module cache"*) : ;;
  *) fail "--check must report the unwarmed module cache; got: $out" ;;
esac

# 6. --check must stay read-only: no ~/.bashrc block, no mutation, no deletion.
if [ ! -f "$sandbox_home/.bashrc" ]; then
  fail "--check must not remove ~/.bashrc"
elif [ "$(cat "$sandbox_home/.bashrc")" != "$original_bashrc" ]; then
  fail "--check must not mutate ~/.bashrc"
fi

# 7. Usage errors are usage errors, not readiness verdicts.
env -i PATH="$hermetic_bin" HOME="$sandbox_home" bash "$SCRIPT" --not-a-flag >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "an unknown option must exit 2 (got $rc)"
env -i PATH="$hermetic_bin" HOME="$sandbox_home" bash "$SCRIPT" --check "$tmp/does-not-exist" >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "a missing repo root must exit 2 (got $rc)"

# 8. The probe and the installer agree on the token the agent is told to use.
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
echo "ok: coding-sandbox Go toolchain provisioning contract (ci#1038, hermetic ci#1044)"
