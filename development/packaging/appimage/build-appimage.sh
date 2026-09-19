#!/usr/bin/env bash
#
# Build a portable open-chat .AppImage from an already-built linux binary.
#
# The AppDir bundles a self-contained AppRun wrapper (see ./AppRun) that starts
# `open-chat server` with XDG-compliant data/config directories and embedded
# Redis, plus the .desktop entry and icon used for desktop integration.
#
# Required environment variables:
#   VERSION  release version without a leading "v" (e.g. 0.0.611)
#   BIN      path to the built linux binary (should be CGO_ENABLED=0/static)
#
# Optional:
#   ARCH                  target architecture: amd64|arm64 (default amd64)
#   OUT_DIR               output directory (default <repo>/dist)
#   APPIMAGETOOL          appimagetool binary to use (default: pinned download)
#   APPIMAGE_UPDATE_INFO  AppImage update information string to embed
#   APPIMAGETOOL_VERSION  appimagetool release to download (default 1.9.1)
#   RUNTIME_VERSION       type2-runtime release to download (default 20251108)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

ARCH="${ARCH:-amd64}"
VERSION="${VERSION:-}"
BIN="${BIN:-}"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/dist}"
APPIMAGETOOL="${APPIMAGETOOL:-}"
APPIMAGE_UPDATE_INFO="${APPIMAGE_UPDATE_INFO:-}"
APPIMAGETOOL_VERSION="${APPIMAGETOOL_VERSION:-1.9.1}"
RUNTIME_VERSION="${RUNTIME_VERSION:-20251108}"

if [[ -z "${VERSION}" ]]; then
  echo "Error: VERSION is required" >&2
  exit 1
fi
VERSION="${VERSION#v}"

if [[ -z "${BIN}" ]]; then
  echo "Error: BIN is required (path to the linux binary)" >&2
  exit 1
fi
if [[ ! -f "${BIN}" ]]; then
  echo "Error: BIN '${BIN}' does not exist" >&2
  exit 1
fi

case "${ARCH}" in
  amd64 | x86_64) RUNTIME_ARCH="x86_64" ;;
  arm64 | aarch64) RUNTIME_ARCH="aarch64" ;;
  *)
    echo "Error: unsupported ARCH '${ARCH}' (expected amd64 or arm64)" >&2
    exit 1
    ;;
esac

CACHE_DIR="${XDG_CACHE_HOME:-${HOME:-/tmp}/.cache}/open-chat-appimage"
mkdir -p "${CACHE_DIR}"

fetch() {
  local url="$1" dest="$2"
  if [[ -f "${dest}" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "${dest}")"
  echo "Downloading ${url}" >&2
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 5 --retry-delay 3 --retry-all-errors \
      --connect-timeout 20 --speed-limit 1024 --speed-time 30 \
      -o "${dest}" "${url}"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "${dest}" "${url}"
  else
    echo "Error: curl or wget is required to download AppImage tooling" >&2
    exit 1
  fi
  chmod +x "${dest}"
}

is_appimage() {
  [[ -f "$1" ]] || return 1
  local magic
  magic="$(dd if="$1" bs=1 skip=8 count=3 2>/dev/null | od -An -tx1 | tr -d ' \n')"
  [[ "${magic}" == "414902" ]]
}

# Resolve appimagetool. Prefer an explicitly provided binary, then PATH, then
# fall back to a pinned, reproducible download.
if [[ -n "${APPIMAGETOOL}" ]]; then
  APPIMAGETOOL_BIN="${APPIMAGETOOL}"
elif command -v appimagetool >/dev/null 2>&1; then
  APPIMAGETOOL_BIN="$(command -v appimagetool)"
else
  case "$(uname -m)" in
    x86_64 | amd64) TOOL_ARCH="x86_64" ;;
    aarch64 | arm64) TOOL_ARCH="aarch64" ;;
    *)
      echo "Error: unsupported host architecture '$(uname -m)' for appimagetool" >&2
      exit 1
      ;;
  esac
  APPIMAGETOOL_BIN="${CACHE_DIR}/appimagetool-${APPIMAGETOOL_VERSION}-${TOOL_ARCH}.AppImage"
  fetch \
    "https://github.com/AppImage/appimagetool/releases/download/${APPIMAGETOOL_VERSION}/appimagetool-${TOOL_ARCH}.AppImage" \
    "${APPIMAGETOOL_BIN}"
fi

if ! is_appimage "${APPIMAGETOOL_BIN}" && [[ ! -x "${APPIMAGETOOL_BIN}" ]]; then
  echo "Error: '${APPIMAGETOOL_BIN}' is not executable" >&2
  exit 1
fi

# Pinned runtime, independent of the appimagetool host architecture. This also
# enables cross-building (e.g. an arm64 AppImage on an amd64 runner).
RUNTIME_FILE="${CACHE_DIR}/runtime-${RUNTIME_VERSION}-${RUNTIME_ARCH}"
fetch \
  "https://github.com/AppImage/type2-runtime/releases/download/${RUNTIME_VERSION}/runtime-${RUNTIME_ARCH}" \
  "${RUNTIME_FILE}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

APPDIR="${WORK_DIR}/open-chat.AppDir"
mkdir -p "${APPDIR}/usr/bin" \
  "${APPDIR}/usr/share/applications" \
  "${APPDIR}/usr/share/icons/hicolor/256x256/apps"

install -m 0755 "${BIN}" "${APPDIR}/usr/bin/open-chat"
install -m 0755 "${SCRIPT_DIR}/AppRun" "${APPDIR}/AppRun"
install -m 0644 "${SCRIPT_DIR}/open-chat.desktop" "${APPDIR}/open-chat.desktop"
install -m 0644 "${SCRIPT_DIR}/open-chat.desktop" "${APPDIR}/usr/share/applications/open-chat.desktop"
install -m 0644 "${SCRIPT_DIR}/open-chat.png" "${APPDIR}/open-chat.png"
install -m 0644 "${SCRIPT_DIR}/open-chat.png" "${APPDIR}/usr/share/icons/hicolor/256x256/apps/open-chat.png"
ln -sf open-chat.png "${APPDIR}/.DirIcon"

TOOL_CMD=("${APPIMAGETOOL_BIN}")
if is_appimage "${APPIMAGETOOL_BIN}"; then
  # FUSE is not available on GitHub-hosted runners; extract and run instead.
  TOOL_CMD=("${APPIMAGETOOL_BIN}" --appimage-extract-and-run)
fi

mkdir -p "${OUT_DIR}"
TARGET="${OUT_DIR}/open-chat-${VERSION}-${RUNTIME_ARCH}.AppImage"

ARGS=(--no-appstream --runtime-file "${RUNTIME_FILE}" "--mksquashfs-opt=-no-progress")
if [[ -n "${APPIMAGE_UPDATE_INFO}" ]]; then
  ARGS+=(--updateinformation "${APPIMAGE_UPDATE_INFO}")
fi

echo "Building ${TARGET} (${ARCH})"
ARCH="${RUNTIME_ARCH}" "${TOOL_CMD[@]}" "${ARGS[@]}" "${APPDIR}" "${TARGET}"

chmod +x "${TARGET}"
echo "Built ${TARGET}"
ls -lh "${TARGET}"