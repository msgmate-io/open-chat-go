#!/usr/bin/env bash
# Self-test for the ci#423 defaults in development/scripts/integration_module_ci.sh:
#
#   REQUIRE_DECOUPLED defaults to DECOUPLED.
#
# A decoupled build (DECOUPLED=true) must enforce the decoupling guard as a hard
# gate, never report-only. This is what makes the reusable module CI's
# `decoupled: true` input safe: an integration whose `replace backend` cleanup
# lands cannot silently keep pulling backend/... into its shipped artifact.
#
# The test sources the env-default block of the script (not the whole script -
# it would try to build an integration module) and checks the mapping. It needs
# no Go toolchain and no network.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/development/scripts/integration_module_ci.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

require_mapping() { # require_mapping <env-extra> <expected> <label>
  local extra="$1" expected="$2" label="$3"
  local out
  out="$(env $extra bash -c '
    set -euo pipefail
    # Mirror the env-default block of integration_module_ci.sh verbatim.
    DECOUPLED="${DECOUPLED:-false}"
    REQUIRE_DECOUPLED="${REQUIRE_DECOUPLED:-$DECOUPLED}"
    printf "%s" "$REQUIRE_DECOUPLED"
  ')"
  [ "$out" = "$expected" ] || { echo "FAIL: $label (got '$out', want '$expected')" >&2; exit 1; }
  echo "ok: $label -> REQUIRE_DECOUPLED=$out"
}

# Explicit DECOUPLED=true forces the guard on.
require_mapping "DECOUPLED=true" true "DECOUPLED=true"
# Explicit REQUIRE_DECOUPLED wins over the default.
require_mapping "DECOUPLED=true REQUIRE_DECOUPLED=false" false "DECOUPLED=true REQUIRE_DECOUPLED=false"
# Non-decoupled builds stay report-only by default.
require_mapping "DECOUPLED=false" false "DECOUPLED=false"
# Unset DECOUPLED is the same as explicit false.
require_mapping "" false "unset"

grep -q 'REQUIRE_DECOUPLED="${REQUIRE_DECOUPLED:-$DECOUPLED}"' "$SCRIPT" \
  || { echo "FAIL: integration_module_ci.sh no longer contains the default mapping" >&2; exit 1; }

echo "integration_module_ci defaults self-test: ok"