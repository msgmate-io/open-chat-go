#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
VERSION_FILE="${REPO_ROOT}/backend/api/metrics/handler.go"

if [[ ! -f "${VERSION_FILE}" ]]; then
  echo "Error: version file not found at ${VERSION_FILE}" >&2
  exit 1
fi

CURRENT_VERSION="$(grep 'var VERSION =' "${VERSION_FILE}" | sed 's/.*"\(.*\)".*/\1/')"
if [[ -z "${CURRENT_VERSION}" ]]; then
  echo "Error: unable to parse current backend version" >&2
  exit 1
fi

# An explicit OPEN_CHAT_VERSION pins the exact version instead of incrementing;
# this lets release tooling single-source the version.
if [[ -n "${OPEN_CHAT_VERSION:-}" ]]; then
  if [[ ! "${OPEN_CHAT_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: OPEN_CHAT_VERSION '${OPEN_CHAT_VERSION}' is not major.minor.patch" >&2
    exit 1
  fi
  NEW_VERSION="${OPEN_CHAT_VERSION}"
else
  IFS='.' read -r MAJOR MINOR PATCH <<< "${CURRENT_VERSION}"
  if [[ -z "${MAJOR}" || -z "${MINOR}" || -z "${PATCH}" ]]; then
    echo "Error: unexpected version format '${CURRENT_VERSION}', expected major.minor.patch" >&2
    exit 1
  fi

  if ! [[ "${PATCH}" =~ ^[0-9]+$ ]]; then
    echo "Error: patch segment is not numeric in version '${CURRENT_VERSION}'" >&2
    exit 1
  fi

  NEW_VERSION="${MAJOR}.${MINOR}.$((PATCH + 1))"
fi
TMP_FILE="$(mktemp)"
sed "s|var VERSION = \"${CURRENT_VERSION}\"|var VERSION = \"${NEW_VERSION}\"|" "${VERSION_FILE}" > "${TMP_FILE}"
mv "${TMP_FILE}" "${VERSION_FILE}"

echo "Current version: ${CURRENT_VERSION}"
echo "New version: ${NEW_VERSION}"
