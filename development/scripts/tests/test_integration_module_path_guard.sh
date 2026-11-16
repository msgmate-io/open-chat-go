#!/usr/bin/env bash
# Self-test for development/scripts/integration_module_path_guard.sh (ci#1075).
#
# The guard exists so a module built at the wrong depth reports *why* instead of
# only `backend@v0.0.0: replacement directory ../../../backend does not exist`.
# This test pins the helper behaviour hermetically: no Go toolchain, no network,
# no monorepo materialization - just fixture modules and directory layouts.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
GUARD="$REPO_ROOT/development/scripts/integration_module_path_guard.sh"

# shellcheck source=development/scripts/integration_module_path_guard.sh disable=SC1091
. "$GUARD"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# --- part 1: relative replace targets are enumerated, both go.mod forms ------
mkdir -p "$TMP/mod"
cat > "$TMP/mod/go.mod" <<'MOD'
module example.com/integration

go 1.26.0

require (
	backend v0.0.0
	github.com/msgmate-io/go-integration-interface v0.0.0
)

replace backend => ../../../backend

replace github.com/msgmate-io/go-integration-interface => ../../go_integration_interface

replace (
	github.com/msgmate-io/go-client-integration => ../go_client_integration
	github.com/msgmate-io/pinned v1.2.3 => github.com/msgmate-io/pinned v1.2.4
)

exclude example.com/dep v1.0.0
MOD

want_targets='../../../backend
../../go_integration_interface
../go_client_integration'
got_targets="$(integration_module_relative_replaces "$TMP/mod/go.mod")"
[ "$got_targets" = "$want_targets" ] ||
  fail "relative replace targets mismatch (ci#1075):
got:
$got_targets
want:
$want_targets"

# A version-only / absolute replacement must not be reported as a layout target.
if printf '%s\n' "$got_targets" | grep -q 'github.com/'; then
  fail "non-relative replacement leaked into the layout targets: $got_targets"
fi

# --- part 2: the collapsed layout (the old bug) reports every target ---------
# Reproduce `<workspace>/<name>`: no clients/integrations structure around it.
mkdir -p "$TMP/collapsed/mod"
cp "$TMP/mod/go.mod" "$TMP/collapsed/mod/go.mod"

missing="$(integration_module_missing_replaces "$TMP/collapsed/mod")"
[ -n "$missing" ] || fail "collapsed layout reported no missing replace target (ci#1075)"
for target in ../../../backend ../../go_integration_interface ../go_client_integration; do
  printf '%s\n' "$missing" | grep -qxF "$target" ||
    fail "collapsed layout did not report $target (got: $missing)"
done

# ... and the hint names the required location, so the fix is obvious.
hint="$(integration_module_layout_hint "$TMP/collapsed/mod")"
printf '%s\n' "$hint" | grep -q 'clients/integrations' ||
  fail "layout hint does not name the required build path: $hint"
printf '%s\n' "$hint" | grep -q 'ci#1075' ||
  fail "layout hint does not reference the ticket: $hint"

# --- part 3: the monorepo layout satisfies every target ----------------------
# <workspace>/clients/integrations/mod with the siblings the module replaces.
mkdir -p "$TMP/workspace/clients/integrations/mod"
cp "$TMP/mod/go.mod" "$TMP/workspace/clients/integrations/mod/go.mod"
mkdir -p "$TMP/workspace/backend" \
  "$TMP/workspace/clients/go_integration_interface" \
  "$TMP/workspace/clients/integrations/go_client_integration"

missing="$(integration_module_missing_replaces "$TMP/workspace/clients/integrations/mod")"
[ -z "$missing" ] ||
  fail "structured layout reported missing replace targets: $missing"

# --- part 4: no replace directives / no go.mod are both harmless -------------
mkdir -p "$TMP/plain"
printf 'module example.com/plain\n\ngo 1.26.0\n' > "$TMP/plain/go.mod"
[ -z "$(integration_module_relative_replaces "$TMP/plain/go.mod")" ] ||
  fail "a module without replaces produced layout targets"
[ -z "$(integration_module_missing_replaces "$TMP/plain")" ] ||
  fail "a module without replaces produced missing targets"
[ -z "$(integration_module_relative_replaces "$TMP/does-not-exist/go.mod")" ] ||
  fail "a missing go.mod produced layout targets"

printf 'integration_module_path_guard (ci#1075) self-test: ok\n'
