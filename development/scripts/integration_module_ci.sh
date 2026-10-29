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
#      `go test ./...` in the integration module;
#   4. report whether the module is still coupled to `backend/...` (ci#423).
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
#   REQUIRE_DECOUPLED    "true"/"false" - fail when the module still imports
#                        backend/... in its shipped packages (defaults to
#                        DECOUPLED: a decoupled build enforces the decoupling
#                        guard as a hard gate; a non-decoupled build stays
#                        report-only while the ci#423 migration is incremental)
#   DECOUPLED            "true"/"false" - skip profile materialization and build
#                        the module against its minimal compile inputs, i.e. the
#                        per-integration prebuild precondition (ci#423)
#   FROZEN               "true"/"false" - use the pinned lock (default: true)
#   GO_TEST_TIMEOUT      per-package test timeout (default: 10m)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

# Normalize to absolute paths up front: the workspace is extracted at
# WORKSPACE_PATH (repo-relative) and the integration checkout at INTEGRATION_SRC.
# Working purely with absolute forms makes the `cd` below a no-op and keeps the
# "compile via the monorepo layout" contract independent of the caller's cwd.
INTEGRATION_SRC="$(cd "$(dirname "${INTEGRATION_SRC:?INTEGRATION_SRC must point at the integration checkout}")" && pwd)/$(basename "${INTEGRATION_SRC}")"
WORKSPACE_PATH="$(cd "$REPO_ROOT" && pwd)/$(basename "${WORKSPACE_PATH:?WORKSPACE_PATH must be the workspace-relative build path}")"
INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-core-only}"
RUN_TESTS="${RUN_TESTS:-true}"
FROZEN="${FROZEN:-true}"
# ci#423: once an integration's shipped packages are decoupled, its compile inputs
# no longer need the materialized profile - only the interface module next to it.
# DECOUPLED=true builds exactly those inputs and enforces the decoupling guard.
# A decoupled build must never silently re-couple: REQUIRE_DECOUPLED defaults to
# DECOUPLED, so the guard is a hard gate there, while non-decoupled builds stay
# report-only for the incremental migration.
DECOUPLED="${DECOUPLED:-false}"
REQUIRE_DECOUPLED="${REQUIRE_DECOUPLED:-$DECOUPLED}"
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

if [ "$DECOUPLED" = "true" ]; then
  # ci#423: nothing to materialize - a decoupled module compiles from the
  # interface module plus its own dependencies, so the profile repos (and their
  # transitive backend/... graph) stay out of the build entirely.
  step "Decoupled build: skipping profile materialization (ci#423)"
  echo "compile inputs: $WORKSPACE_PATH + the pinned interface module only"
else
  step "Materialize workspace (profile: $INTEGRATION_PROFILE, frozen: $FROZEN)"
  BUILD_TOOLS="$(bash "$REPO_ROOT/development/scripts/build_tools.sh")"
  PREPARE_ARGS=(prepare --profile "$INTEGRATION_PROFILE")
  if [ "$FROZEN" = "true" ]; then
    PREPARE_ARGS+=(--frozen)
  else
    PREPARE_ARGS+=(--update)
  fi
  "$BUILD_TOOLS" "${PREPARE_ARGS[@]}"
fi

step "Place the pull-request checkout at $WORKSPACE_PATH"
mkdir -p "$WORKSPACE_PATH"
# Replace only the source tree; keep any gitignored build leftovers harmless.
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete --exclude '.git' "$INTEGRATION_SRC"/ "$WORKSPACE_PATH"/
else
  find "$WORKSPACE_PATH" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  (cd "$INTEGRATION_SRC" && tar --exclude=.git -cf - .) | (cd "$WORKSPACE_PATH" && tar -xf -)
fi

if [ "$DECOUPLED" = "true" ]; then
  # ci#423: fail fast and with a clear message when a module that still replaces
  # or imports backend/... is pushed down the decoupled path; otherwise the
  # missing materialized profile only surfaces as a confusing import error.
  step "Decoupled preflight: module must not import backend/... (ci#423)"
  PREFLIGHT_GUARD="$REPO_ROOT/development/scripts/check_integration_decoupling.py"
  if [ -f "$PREFLIGHT_GUARD" ] && command -v python3 >/dev/null 2>&1; then
    if ! python3 "$PREFLIGHT_GUARD" --module-dir "$WORKSPACE_PATH"; then
      echo "::error::decoupled build requires a module that no longer imports backend/... (ci#423)" >&2
      exit 1
    fi
  else
    echo "skipped: guard script or python3 unavailable"
  fi
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

# --- ci#423: integration decoupling report ---------------------------------
# An integration is *decoupled* once its shipped artifact stops importing
# backend/... (see development/scripts/check_integration_decoupling.py and the
# ci#345 design). The migration is incremental, so non-decoupled builds report
# by default; a decoupled build defaults REQUIRE_DECOUPLED to DECOUPLED, so
# remaining coupling there is a hard failure.
#
# DECOUPLED=true already enforced the guard before the build, so there is nothing
# left to report (and the guard must not run twice).
if [ "$DECOUPLED" != "true" ]; then
  step "Integration decoupling report (ci#423)"
  GUARD="$REPO_ROOT/development/scripts/check_integration_decoupling.py"
  if [ -f "$GUARD" ] && command -v python3 >/dev/null 2>&1; then
    # Measure the materialized workspace: that layout has the relative `replace`
    # targets next to it, so `go list -deps` resolves exactly like the build above.
    if ! python3 "$GUARD" --module-dir "$WORKSPACE_PATH"; then
      if [ "$REQUIRE_DECOUPLED" = "true" ]; then
        echo "::error::integration is still coupled to backend/... (ci#423)" >&2
        exit 1
      fi
      echo "note: coupling is reported, not enforced (on a non-decoupled build " \
        "REQUIRE_DECOUPLED defaults to DECOUPLED=false; enable the " \
        "decoupled prebuild to make the guard a hard gate)"
    fi
  else
    echo "skipped: guard script or python3 unavailable"
  fi
fi

echo
echo "Integration module CI passed for $WORKSPACE_PATH"
