#!/usr/bin/env bash
# ci#739: pin the contract of the coding-sandbox readiness probe.
#
# The readiness probe is the single place that tells a msgmate-coder agent
# whether the sandbox can run local Go checks at all, and it is also what the
# dev-setup script runs after provisioning. Two properties must not silently
# regress:
#
#   1. It is valid bash (it runs inside the generated set-up script).
#   2. In a workspace without `backend/go.work` it exits non-zero and points at
#      the *current* provisioning entry point (`development/scripts/build_tools.sh`,
#      which installs the CLI even when the build-tools submodule is absent)
#      rather than at a bare `.venv/bin/openchat-integrations` that a fresh
#      checkout may not have.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/check_coder_sandbox_readiness.sh"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

[ -x "$SCRIPT" ] || fail "check_coder_sandbox_readiness.sh must exist and be executable"

# 1. Valid bash.
if ! bash -n "$SCRIPT" 2>/dev/null; then
  fail "check_coder_sandbox_readiness.sh must be valid bash"
fi

# 2. Unprepared workspace: an empty root has no go.work and no CLI, so the probe
#    must fail and advertise the submodule-free provisioning command.
empty_root="$(mktemp -d)"
trap 'rm -rf "$empty_root"' EXIT

out="$(bash "$SCRIPT" "$empty_root" 2>&1)"
rc=$?
[ "$rc" -ne 0 ] || fail "probe must exit non-zero for an unprepared workspace (got $rc)"
case "$out" in
  *go.work*) : ;;
  *) fail "probe output must name the missing backend/go.work" ;;
esac
case "$out" in
  *development/scripts/build_tools.sh*) : ;;
  *) fail "probe recovery hint must use development/scripts/build_tools.sh" ;;
esac
case "$out" in
  *"prepare --profile full-ci"*) : ;;
  *) fail "probe recovery hint must use the full-ci profile" ;;
esac

# 2b. The verdict is read by an agent deciding whether to keep an unverified
#     change, so it must be plain text: `note` is `printf '%s\n' "$*"`, and
#     `printf %s` does NOT expand escapes inside its argument, so an embedded
#     `\n` in a note string reaches the reader as the two literal characters
#     backslash+n instead of a line break.
case "$out" in
  *'\n'*) fail "probe output must not contain a literal backslash-n sequence (use separate note calls)" ;;
  *) : ;;
esac
case "$out" in
  *"authoritative full-suite gate"*) : ;;
  *) fail "probe must tell the agent that PR CI is the authoritative gate" ;;
esac

# 3. A directory that does not exist is a usage error, not a readiness verdict.
bash "$SCRIPT" "$empty_root/nope" >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || fail "a missing repo root must exit 2 (got $rc)"

if [ "$failures" -ne 0 ]; then
  printf '%d check(s) failed\n' "$failures" >&2
  exit 1
fi
echo "ok: coding-sandbox readiness probe contract (ci#739)"
