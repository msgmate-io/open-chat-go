#!/usr/bin/env bash
# ci#1038 - hermetic guard for the sandbox image Go-toolchain gate.
#
# The failure this ticket describes was not a broken command: it was a sandbox
# image that shipped *without* `go` while `git`/`gh` worked, so an implementer
# could not run `go build ./...` and could only discover breakage from CI. The
# fix lives in two places that are easy to delete by accident during a workflow
# refactor:
#
#   1. .github/workflows/build-sandbox-images.yaml must verify the freshly
#      built image (offline, `--network none`) instead of trusting the build.
#   2. The expected toolchain version must come from backend/go.mod, not from a
#      hardcoded copy that silently rots away from the repo's real directive.
#
# This test asserts both, without docker, network or a built image.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORKFLOW="${REPO_ROOT}/.github/workflows/build-sandbox-images.yaml"
GO_MOD="${REPO_ROOT}/backend/go.mod"

failures=0
checks=0

pass() { checks=$((checks + 1)); printf 'ok   %s\n' "$1"; }
fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  printf 'FAIL %s\n' "$1" >&2
}

require_file() {
  if [ -f "$1" ]; then pass "exists: $2"; else fail "missing: $2"; fi
}

require_file "${WORKFLOW}" ".github/workflows/build-sandbox-images.yaml"
require_file "${GO_MOD}" "backend/go.mod"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d check(s), %d failure(s)\n' "${checks}" "${failures}" >&2
  exit 1
fi

# The verify step must exist and must be gated on a real (non-dry-run) build.
if grep -q 'name: Verify Go toolchain in the built image' "${WORKFLOW}"; then
  pass "image build verifies the Go toolchain"
else
  fail "no 'Verify Go toolchain in the built image' step in ${WORKFLOW}"
fi

# Offline verification is the whole point: a warm module cache is what makes
# `go build ./...` work in a fresh sandbox without network.
if grep -q -- '--network none' "${WORKFLOW}"; then
  pass "toolchain verification runs offline (--network none)"
else
  fail "toolchain verification does not run with --network none"
fi

# `missing_cli: go` is the machine-readable token the ticket asks for, so an
# agent (and the lead reading its summary) cannot mistake a missing binary for a
# passing test run.
if grep -q 'missing_cli: go' "${WORKFLOW}"; then
  pass "missing go is reported as 'missing_cli: go'"
else
  fail "the verify step does not emit 'missing_cli: go'"
fi

# GOTOOLCHAIN=local: with `auto` (the default) a newer directive makes the
# toolchain download itself at build time, which cannot work offline and hides
# exactly the version skew this gate is meant to catch.
if grep -q 'GOTOOLCHAIN' "${WORKFLOW}"; then
  pass "verify step pins/asserts GOTOOLCHAIN"
else
  fail "verify step does not assert GOTOOLCHAIN=local"
fi

# The warmed module cache must be checked, otherwise the toolchain can be present
# and every offline build still fails on a fetch.
if grep -q 'GOMODCACHE' "${WORKFLOW}"; then
  pass "verify step checks the warmed module cache (GOMODCACHE)"
else
  fail "verify step does not check the warmed module cache"
fi

# Version contract must be derived from backend/go.mod.
if grep -q "awk .*go\.mod" "${WORKFLOW}" && grep -q 'EXPECTED_GO' "${WORKFLOW}"; then
  pass "expected Go version is derived from backend/go.mod"
else
  fail "expected Go version is not derived from backend/go.mod"
fi

# ... and backend/go.mod must actually carry a directive to derive it from.
if awk '$1 == "go" && $2 ~ /^[0-9]+\.[0-9]+(\.[0-9]+)?$/ { found = 1 } END { exit(found ? 0 : 1) }' "${GO_MOD}"; then
  pass "backend/go.mod declares a numeric 'go' directive"
else
  fail "backend/go.mod has no numeric 'go' directive"
fi

# The gate is only worth anything if PR CI actually executes it: this file was
# added for ci#1038 precisely because nothing ran it while agents were the only
# consumers of a sandbox image. Pin the wiring too.
PR_CI="${REPO_ROOT}/.github/workflows/run-pr-ci-tests-and-deployments.yaml"
if [ -f "${PR_CI}" ] && grep -q 'test_sandbox_image_go_toolchain_gate.sh' "${PR_CI}"; then
  pass "PR CI runs this gate (test_sandbox_image_go_toolchain_gate.sh)"
else
  fail "PR CI does not run development/scripts/tests/test_sandbox_image_go_toolchain_gate.sh"
fi

# ... and the provisioning half (ci#1038 i-v) must stay wired next to it.
if [ -f "${PR_CI}" ] && grep -q 'test_sandbox_go_toolchain.sh' "${PR_CI}"; then
  pass "PR CI runs the provisioning contract (test_sandbox_go_toolchain.sh)"
else
  fail "PR CI does not run development/scripts/tests/test_sandbox_go_toolchain.sh"
fi

printf '\n%d check(s), %d failure(s)\n' "${checks}" "${failures}"
[ "${failures}" -eq 0 ]
