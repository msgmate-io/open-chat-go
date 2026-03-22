#!/usr/bin/env bash
# ci#733 - one development-lead fleet-moderation pass.
#
# Runs the whole playbook pass in order, read-only by default:
#   audit -> capacity -> rescue -> label-audit -> consolidate
#
# Usage:
#   bash development/scripts/devlead/devlead_pass.sh              # dry-run report
#   bash development/scripts/devlead/devlead_pass.sh --apply      # asks once, then acts
#   bash development/scripts/devlead/devlead_pass.sh --apply --yes
#
# Environment:
#   OPEN_CHAT_API_TOKEN / CI_API_TOKEN  API token (never written to disk or printed)
#   OPEN_CHAT_HOST                      defaults to https://ci.msgmate.io
#   DEVLEAD_REPO                       defaults to msgmate-io/open-chat-go
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL="python3 ${HERE}/devlead_fleet.py"
REPO="${DEVLEAD_REPO:-msgmate-io/open-chat-go}"
HOST="${OPEN_CHAT_HOST:-https://ci.msgmate.io}"
OUT_DIR="${DEVLEAD_OUT_DIR:-$(mktemp -d -t devlead-pass-XXXXXX)}"
APPLY=0
ASSUME_YES=0
REQUIRED=1

usage() {
  grep '^#' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --required) REQUIRED="${2:-1}"; shift ;;
    --out-dir) OUT_DIR="${2:-}"; shift ;;
    -h|--help) usage 0 ;;
    *) echo "unknown argument: $1" >&2; usage 2 ;;
  esac
  shift
done

mkdir -p "${OUT_DIR}"
AUDIT_JSON="${OUT_DIR}/audit-$(date -u +%Y%m%dT%H%M%SZ).json"

if [ "${APPLY}" -eq 1 ] && [ "${ASSUME_YES}" -eq 0 ]; then
  printf 'APPLY mode: this pass will stop/continue agents and may open a consolidation PR. Continue? [y/N] '
  read -r reply
  case "${reply}" in y|Y|yes|YES) ;; *) echo "aborted"; exit 0 ;; esac
fi

echo "== audit (managed fleet) =="
${TOOL} --base "${HOST}" audit --json-out "${AUDIT_JSON}" || echo "audit failed (continuing with other stages)"

echo
echo "== capacity guard (/api/v1/admin/provider-admission) =="
# Exit 1 means "cap full": report it, but do not abort the whole pass.
${TOOL} --base "${HOST}" capacity --required "${REQUIRED}" --report-only || true

echo
echo "== rescue (stop + narrow continue) =="
if [ "${APPLY}" -eq 1 ]; then
  ${TOOL} --base "${HOST}" rescue --audit-file "${AUDIT_JSON}" --apply || true
else
  ${TOOL} --base "${HOST}" rescue --audit-file "${AUDIT_JSON}" || true
fi

echo
echo "== PR hygiene (ci#733 Part G label budget) =="
if [ "${APPLY}" -eq 1 ]; then
  ${TOOL} label-audit --repo "${REPO}" --apply || true
else
  ${TOOL} label-audit --repo "${REPO}" || true
fi

echo
echo "== consolidation (one chore/consolidate-<date>-rNN) =="
if [ "${APPLY}" -eq 1 ]; then
  ${TOOL} consolidate --repo "${REPO}" --apply || true
else
  ${TOOL} consolidate --repo "${REPO}" || true
fi

echo
echo "artifacts: ${OUT_DIR}"
echo "Do not poll CI - the PR CI run is the authoritative gate."
