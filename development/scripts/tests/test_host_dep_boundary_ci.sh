#!/usr/bin/env bash
# Hermetic no-Go checks for the ci#423 stable-Host boundary guards.
# Run by CI2's integration-module job (and locally in seconds):
#   bash development/scripts/tests/test_host_dep_boundary_ci.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FAILS=0

echo "== host-surface guard self-test =="
if bash "$ROOT/development/scripts/tests/test_host_surface.sh"; then
  echo "ok: host-surface guard self-test"
else
  echo "FAIL: host-surface guard self-test"
  FAILS=$((FAILS + 1))
fi

echo "== decoupling guard self-test =="
if bash "$ROOT/development/scripts/tests/test_integration_decoupling.sh"; then
  echo "ok: decoupling guard self-test"
else
  echo "FAIL: decoupling guard self-test"
  FAILS=$((FAILS + 1))
fi

echo "== dep-surface fixture self-test (no Go) =="
if python3 "$ROOT/development/scripts/tests/test_dep_surface_fixture.py"; then
  echo "ok: dep-surface fixture self-test"
else
  echo "FAIL: dep-surface fixture self-test"
  FAILS=$((FAILS + 1))
fi

if [ "$FAILS" -ne 0 ]; then
  echo "$FAILS ci#423 boundary checks failed"
  exit 1
fi
echo "ci#423 boundary self-tests: all pass"