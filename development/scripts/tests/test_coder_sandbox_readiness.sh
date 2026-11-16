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

# 4. ci#1048: the same self-check finding that reported a misprovisioned coder
#    workspace (ci#687) also named a missing `gh` CLI. The probe must always
#    report on it, either as available or with the machine-readable
#    `missing_cli: gh` token, so a reader can tell it apart from an agent that
#    simply gave up.
case "$out" in
  *"github CLI (ci#1048)"*) : ;;
  *) fail "probe output must contain the github CLI section (ci#1048)" ;;
esac

fake_bin="$(mktemp -d)"
trap 'rm -rf "$empty_root" "$fake_bin"' EXIT
printf '#!/bin/sh\nexit 0\n' >"$fake_bin/gh"
chmod +x "$fake_bin/gh"
gh_out="$(PATH="$fake_bin:$PATH" bash "$SCRIPT" "$empty_root" 2>&1)"
case "$gh_out" in
  *"ok   gh CLI on PATH"*) : ;;
  *) fail "probe must report a resolvable gh CLI as ok" ;;
esac

if ! command -v gh >/dev/null 2>&1; then
  case "$out" in
    *"missing_cli: gh"*) : ;;
    *) fail "probe must emit missing_cli: gh when gh is absent" ;;
  esac
fi

# 5. ci#1038: the probe is what an implementer reads before writing the report, so
#    it must point at the offline-build gate whose verdicts are quotable
#    (`go: MISSING` / `go_work: MISSING` / `go_build: OK|FAILED`) instead of
#    leaving the reader to invent a verification story.
case "$out" in
  *"check_sandbox_go_offline_build.sh"*) : ;;
  *) fail "probe must name the offline Go build gate (ci#1038)" ;;
esac
case "$out" in
  *"go_work: MISSING"*) : ;;
  *) fail "probe must name the go_work: MISSING verdict an implementer has to quote" ;;
esac

if [ "$failures" -ne 0 ]; then
  printf '%d check(s) failed\n' "$failures" >&2
  exit 1
fi
echo "ok: coding-sandbox readiness probe contract (ci#739)"
