#!/usr/bin/env bash
#
# Build a single open-chat .deb with nFPM.
#
# Required environment variables:
#   VERSION  package version without a leading "v" (e.g. 0.0.611)
#   BIN      path to the built linux binary
# Optional:
#   ARCH     target Go architecture (defaults to the host GOARCH / amd64)
#   OUT_DIR  output directory (defaults to <repo>/dist)
#   NFPM     nfpm binary to invoke (defaults to "nfpm" on PATH)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

ARCH="${ARCH:-$(go env GOARCH 2>/dev/null || echo amd64)}"
VERSION="${VERSION:-}"
BIN="${BIN:-}"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/dist}"
NFPM="${NFPM:-nfpm}"

if [[ -z "${VERSION}" ]]; then
  echo "Error: VERSION is required" >&2
  exit 1
fi
if [[ -z "${BIN}" ]]; then
  echo "Error: BIN is required (path to the linux binary)" >&2
  exit 1
fi
if [[ ! -f "${BIN}" ]]; then
  echo "Error: BIN '${BIN}' does not exist" >&2
  exit 1
fi
if ! command -v "${NFPM}" >/dev/null 2>&1; then
  echo "Error: nfpm not found (install github.com/goreleaser/nfpm/v2/cmd/nfpm)" >&2
  exit 1
fi

# Resolve caller-provided paths before changing directories: callers (e.g. CI)
# may pass BIN/OUT_DIR relative to the repository root.
ABS_BIN="$(cd "$(dirname "${BIN}")" && pwd)/$(basename "${BIN}")"
mkdir -p "${OUT_DIR}"
OUT_DIR="$(cd "${OUT_DIR}" && pwd)"
TARGET="${OUT_DIR}/open-chat_${VERSION}_${ARCH}.deb"

# nFPM resolves relative contents/scripts paths against the config directory.
cd "${SCRIPT_DIR}"

ARCH="${ARCH}" VERSION="${VERSION}" BIN="${ABS_BIN}" \
  "${NFPM}" package \
    --config "${SCRIPT_DIR}/nfpm.yaml" \
    --packager deb \
    --target "${TARGET}"

echo "Built ${TARGET}"
