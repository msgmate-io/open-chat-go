#!/usr/bin/env bash
# Ensure the Open-Chat build-tools CLI (`openchat-integrations`) is available
# and print the command to invoke. The pinned `development/build-tools`
# submodule checkout is installed into `.venv` on first use; when the submodule
# is absent the tool is installed from upstream instead.
#
# Override the venv location with OPENCHAT_BUILD_TOOLS_VENV. When
# `openchat-integrations` is already on PATH (e.g. inside a build image that
# installed it), it is used as-is.
set -euo pipefail

if command -v openchat-integrations >/dev/null 2>&1; then
  printf '%s\n' "openchat-integrations"
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VENV="${OPENCHAT_BUILD_TOOLS_VENV:-$REPO_ROOT/.venv}"
CLI="$VENV/bin/openchat-integrations"

if [ ! -x "$CLI" ]; then
  echo "[build-tools] installing openchat-integrations into $VENV" >&2
  python3 -m venv "$VENV"
  "$VENV/bin/pip" install --quiet --upgrade pip
  if [ -d "$REPO_ROOT/development/build-tools/openchat_integrations" ]; then
    "$VENV/bin/pip" install --quiet "$REPO_ROOT/development/build-tools"
  else
    "$VENV/bin/pip" install --quiet "git+https://github.com/msgmate-io/open-chat-go-build-tools.git"
  fi
fi

printf '%s\n' "$CLI"
