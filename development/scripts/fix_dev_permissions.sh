#!/usr/bin/env bash
# Repair root-owned artifacts left in the checkout by older integration-sync
# runs (before the service dropped privileges to the host user).
#
# Run it when Docker is not running or you want to heal without starting the
# stack. With sudo, so it can chown files owned by root:
#
#   sudo development/scripts/fix_dev_permissions.sh
#
# The integration-sync entrypoint performs the same repair automatically on the
# next `docker compose up`, so this is a convenience/escape hatch.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# When invoked via sudo, target the invoking user rather than root.
if [ -n "${SUDO_UID:-}" ] && [ -n "${SUDO_GID:-}" ]; then
  TARGET="${SUDO_UID}:${SUDO_GID}"
else
  TARGET="$(id -u):$(id -g)"
fi

PATHS=(
  ".integrations"
  "clients/integrations"
  "development/ci"
  "development/helm"
  "clients/gomobile"
  "clients/llm_coding_agents"
  "frontend/pages/integrations"
  "frontend/integrations"
  "backend/go.work"
  "backend/go.work.sum"
  "backend/integrations/externalintegrations/imports_gen.go"
  "integrations.local.yaml"
)

for rel in "${PATHS[@]}"; do
  path="$REPO_ROOT/$rel"
  if [ -e "$path" ] || [ -L "$path" ]; then
    chown -hR "$TARGET" "$path"
  fi
done

echo "fixed ownership of generated dev artifacts to $TARGET"
