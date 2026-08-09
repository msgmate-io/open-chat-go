#!/usr/bin/env bash
# ci#423: turn the integration dependency-surface baseline into a PR gate.
#
# The measurement itself lives in backend/scripts/integration_dep_surface.py;
# this wrapper is what CI calls, so the gate stays cheap and honest:
#
#   1. absent baseline file          -> no-op (exit 0), never a silent failure;
#   2. `--check-baseline`            -> always, no Go toolchain needed, so a
#      malformed or wrongly-pinned baseline cannot neuter the gate;
#   3. `--baseline` (measurement)    -> only when GOWORK points at a materialized
#      workspace; it prints the offending edge (`module: backend/...`) and fails.
#      It runs in `--fail-only-on-regressions` mode: during the migration only a
#      regression against the baseline (or nothing measurable at all) is red, and
#      the integrations the baseline does not pin are reported as still migrating.
#
# Usage: check_dep_surface_gate.sh [--baseline FILE] [--repo-root DIR] [--no-measure]
set -euo pipefail

# Two roots on purpose: the script itself is always read from this checkout, while
# --repo-root only selects where integration modules are discovered.
SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPO_ROOT="$SCRIPT_ROOT"
BASELINE="$REPO_ROOT/integrations.dep-surface.json"
MEASURE=1
while [ $# -gt 0 ]; do
  case "$1" in
    --baseline) BASELINE="$2"; shift 2 ;;
    --repo-root) REPO_ROOT="$2"; shift 2 ;;
    --no-measure) MEASURE=0; shift ;;
    *) echo "usage: $0 [--baseline FILE] [--repo-root DIR] [--no-measure]" >&2; exit 2 ;;
  esac
done

if [ ! -f "$BASELINE" ]; then
  echo "no dependency-surface baseline at ${BASELINE}; the gate is a no-op."
  exit 0
fi

SCRIPT="$SCRIPT_ROOT/backend/scripts/integration_dep_surface.py"
python3 "$SCRIPT" --check-baseline "$BASELINE"

if [ "$MEASURE" -eq 0 ]; then
  echo "measurement skipped (--no-measure): the baseline was only validated."
  exit 0
fi
if [ -z "${GOWORK:-}" ]; then
  echo "GOWORK is unset: no Go workspace to measure, the baseline was only validated."
  exit 0
fi
if [ ! -f "${GOWORK}" ]; then
  echo "GOWORK=${GOWORK} is not a workspace file: nothing materialized to measure, the baseline was only validated."
  exit 0
fi
if ! find "$REPO_ROOT/clients/integrations" -maxdepth 2 -name go.mod -print -quit 2>/dev/null | grep -q .; then
  echo "no integration module is materialized under ${REPO_ROOT}/clients/integrations: the baseline was only validated."
  exit 0
fi

echo
python3 "$SCRIPT" --baseline "$BASELINE" --repo-root "$REPO_ROOT" --fail-only-on-regressions
