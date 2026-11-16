#!/usr/bin/env bash
# ci#1038: prove the ticket's acceptance line instead of narrating it.
#
#   "in a fresh `repo:open-chat-go` sandbox, `go version && go build ./...`
#    succeeds offline after checkout"
#
# The readiness probe (development/scripts/check_coder_sandbox_readiness.sh)
# answers "is a toolchain present"; this script answers the stronger question an
# implementer actually needs before pushing: *does the repo compile here, with no
# network*. The two failures it separates are the ones that used to be reported as
# a vague "cannot compile" claim:
#
#   go: MISSING      - no usable toolchain in this shell (see the installer).
#   go_work: MISSING - the toolchain is fine, but the private integration modules
#                      are unresolved because the gitignored `backend/go.work`
#                      profile workspace was never materialised. `go build ./...`
#                      then dies on `missing go.sum entry for module providing
#                      package github.com/msgmate-io/go-integration-interface/...`
#                      and `no required module provides package
#                      github.com/msgmate-io/go-client-integration/goclient`.
#                      That is a provisioning gap, NOT a code defect, so no agent
#                      should "fix" it by editing go.mod/go.sum by hand.
#
# Usage:
#   development/scripts/check_sandbox_go_offline_build.sh [repo_root] [--strict-work]
#
# Exit codes: 0 offline build succeeded / 1 not proven / 2 usage error.
# Never touches the network: every go invocation runs with GOPROXY=off and
# GOFLAGS=-mod=readonly so a missing module cache fails instead of downloading.
set -uo pipefail

root=""
strict_work=0
for arg in "$@"; do
  case "$arg" in
    --strict-work) strict_work=1 ;;
    -h|--help)
      sed -n '2,29p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) root="$arg" ;;
  esac
done
[ -n "$root" ] || root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[ -d "$root" ] || { echo "repo root '$root' does not exist" >&2; exit 2; }

note() { printf '%s\n' "$*"; }
status=0

note "== coding sandbox offline Go build (ci#1038) =="

# The agent's shell, not the provisioning shell, is what runs the build. Resolve
# `go` from PATH first (that is the real test) and only fall back to the image
# location to explain *why* it is missing.
go_bin=""
if command -v go >/dev/null 2>&1; then
  go_bin="$(command -v go)"
  note "ok   go on PATH ($go_bin)"
elif [ -x /usr/local/go/bin/go ]; then
  note "missing_cli: go"
  note "MISS go exists at /usr/local/go/bin/go but not on PATH;"
  note "     run development/scripts/install_sandbox_go_toolchain.sh (it exports and"
  note "     persists PATH to ~/.bashrc and /etc/profile.d), then retry."
  status=1
else
  note "missing_cli: go"
  note "MISS this image ships no Go toolchain at all; the coder sandbox image must"
  note "     install it before local checks are attempted."
  status=1
fi

if [ "$status" -ne 0 ] || [ -z "$go_bin" ]; then
  note "go: MISSING"
  note "     Do not claim a local build/test run. Report 'go: MISSING' and rely on"
  note "     PR CI as the gate."
  exit 1
fi

note "ok   $(env GOTOOLCHAIN=local "$go_bin" version 2>/dev/null | head -n1)"

# Private integration modules come from the profile workspace, not from go.mod:
# `openchat-integrations prepare --profile <p>` writes the gitignored
# backend/go.work (see backend/full_build.sh, which exports GOWORK=$PWD/go.work).
# Without it the build cannot resolve msgmate-io/* packages.
work_dir="$root/backend"
if [ ! -f "$work_dir/go.work" ]; then
  note "go_work: MISSING"
  note "MISS backend/go.work is absent (gitignored, generated), so the private"
  note "     integration modules are unresolved and 'go build ./...' fails with"
  note "     'missing go.sum entry' / 'no required module provides package'."
  note "     Materialise the profile workspace first (default profile: core-only):"
  note "       openchat-integrations prepare --profile core-only"
  note "     or, if the tool is not installed: bash development/scripts/build_tools.sh"
  note "     (prints the path to it), or run backend/full_build.sh which does both."
  if [ "$strict_work" -eq 1 ]; then
    status=1
  else
    note "     (reported as guidance: '--strict-work' makes this a hard failure)"
  fi
fi

# The build itself. GOPROXY=off + -mod=readonly is the whole point: an offline
# sandbox must fail visibly instead of silently fetching.
build_log="$(mktemp)"
if ( cd "$work_dir" && env GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=readonly \
      timeout 600 "$go_bin" build ./... ) >"$build_log" 2>&1; then
  note "go_build: OK (offline, backend/...)"
else
  note "go_build: FAILED (offline, backend/...)"
  sed -n '1,20p' "$build_log" | sed 's/^/     /'
  if grep -q 'missing go.sum entry\|no required module provides package' "$build_log"; then
    note "     ^ this is the unresolved-profile-workspace case (go_work: MISSING),"
    note "       not a code defect: materialise the profile workspace"
    note "       ('openchat-integrations prepare --profile core-only'),"
    note "       do not hand-edit go.mod/go.sum."
  fi
  status=1
fi
rm -f "$build_log"

if [ "$status" -eq 0 ]; then
  note "== offline Go build verified =="
else
  note "== NOT verified: see the MISS/FAILED lines above =="
fi
exit "$status"
