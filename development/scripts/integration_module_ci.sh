#!/usr/bin/env bash
# Build one integration module inside the open-chat Go workspace (ci#600 item 1).
#
# Integration repos are not standalone Go modules: their `go.mod` uses relative
# `replace` directives into the monorepo layout
#
#     replace backend => ../../../backend
#     replace github.com/msgmate-io/go-integration-interface => ../../go_integration_interface
#
# so the module only compiles with the open-chat checkout around it. That is why
# the integration repos had no PR CI for years: a plain `go build ./...` in the
# integration checkout fails on the very first import. The result was the ci#514
# merge train landing a non-compiling `main` (`undefined: toolNameEnv`) that only
# a by-hand consumer build caught.
#
# This script is the missing safety net. Run from an open-chat-go checkout with
# the pull-request checkout of the integration next to it:
#
#     INTEGRATION_SRC=/path/to/pr-checkout \
#     WORKSPACE_PATH=clients/integrations/msgmate_coder_integration \
#     INTEGRATION_PROFILE=core-only \
#     bash development/scripts/integration_module_ci.sh
#
# Steps:
#   1. materialize the profile (public submodules + profile repos, then the
#      integration closure via `openchat-integrations prepare`);
#   2. replace the materialized integration checkout with the pull-request
#      checkout (so the PR head is what gets compiled, not the pinned ref);
#   3. gofmt check, `go vet ./...`, `go build ./...` and (optionally)
#      `go test ./...` in the integration module.
#
# The module is built in ordinary module mode (`GOWORK=off`): the integration
# `go.mod` relative replacements are the mechanism, exactly as a consumer builds
# it. A missing go.sum entry therefore fails the job instead of being papered
# over by a workspace-wide `-mod=mod`.
#
# Environment:
#   INTEGRATION_SRC      pull-request checkout to test (required)
#   WORKSPACE_PATH       workspace-relative path the integration is built at (required)
#   INTEGRATION_PROFILE  manifest profile to materialize (default: core-only)
#   RUN_TESTS            "true"/"false" - run `go test ./...` (default: true)
#   FROZEN               "true"/"false" - use the pinned lock (default: true)
#   GO_TEST_TIMEOUT      per-package test timeout (default: 10m)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

INTEGRATION_SRC="${INTEGRATION_SRC:?INTEGRATION_SRC must point at the integration checkout}"
WORKSPACE_PATH="${WORKSPACE_PATH:?WORKSPACE_PATH must be the workspace-relative build path}"
INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-core-only}"
RUN_TESTS="${RUN_TESTS:-true}"
FROZEN="${FROZEN:-true}"
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-10m}"

step() { printf '\n=== %s\n' "$*"; }

if [ ! -d "$INTEGRATION_SRC" ]; then
  echo "error: INTEGRATION_SRC=$INTEGRATION_SRC is not a directory" >&2
  exit 1
fi
if [ ! -f "$INTEGRATION_SRC/go.mod" ]; then
  echo "error: INTEGRATION_SRC=$INTEGRATION_SRC has no go.mod" >&2
  exit 1
fi
if [ -e "$WORKSPACE_PATH" ] && [ ! -d "$WORKSPACE_PATH" ]; then
  echo "error: WORKSPACE_PATH=$WORKSPACE_PATH exists and is not a directory" >&2
  exit 1
fi

step "Materialize workspace (profile: $INTEGRATION_PROFILE, frozen: $FROZEN)"
BUILD_TOOLS="$(bash "$REPO_ROOT/development/scripts/build_tools.sh")"
PREPARE_ARGS=(prepare --profile "$INTEGRATION_PROFILE")
if [ "$FROZEN" = "true" ]; then
  PREPARE_ARGS+=(--frozen)
else
  PREPARE_ARGS+=(--update)
fi
"$BUILD_TOOLS" "${PREPARE_ARGS[@]}"

step "Place the pull-request checkout at $WORKSPACE_PATH"
mkdir -p "$WORKSPACE_PATH"
# Replace only the source tree; keep any gitignored build leftovers harmless.
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete --exclude '.git' "$INTEGRATION_SRC"/ "$WORKSPACE_PATH"/
else
  find "$WORKSPACE_PATH" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  (cd "$INTEGRATION_SRC" && tar --exclude=.git -cf - .) | (cd "$WORKSPACE_PATH" && tar -xf -)
fi

step "Go module checks in $WORKSPACE_PATH"
export GOWORK=off
export GOFLAGS="${GOFLAGS:-}"

if [ ! -f "$WORKSPACE_PATH/go.sum" ]; then
  echo "::error::$WORKSPACE_PATH/go.sum is missing; a module must ship complete checksums" >&2
  exit 1
fi

cd "$WORKSPACE_PATH"

UNFORMATTED="$(gofmt -l . || true)"
if [ -n "$UNFORMATTED" ]; then
  echo "::error::gofmt found unformatted files:" >&2
  printf '%s\n' "$UNFORMATTED" >&2
  exit 1
fi
echo "gofmt: clean"

# `go mod verify`-lite: fail early with a clear message when go.sum/`go.mod` are
# out of sync, which is otherwise reported as a confusing import error.
if ! go list -deps -test ./... >/dev/null 2>&1; then
  echo "--- dependency resolution failed; showing go list diagnostics" >&2
  go list -deps -test ./... >&2 || true
  exit 1
fi

echo "--- go vet ./..."
go vet ./...

echo "--- go build ./..."
go build ./...

if [ "$RUN_TESTS" = "true" ]; then
  echo "--- go test ./..."
  go test -timeout "$GO_TEST_TIMEOUT" ./...
else
  echo "--- go test ./... skipped (RUN_TESTS=false)"
fi

echo
echo "Integration module CI passed for $WORKSPACE_PATH"
