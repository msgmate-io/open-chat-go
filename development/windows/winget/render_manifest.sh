#!/usr/bin/env bash
#
# render_manifest.sh renders the open-chat winget manifests and the Scoop bucket
# recipe for a given release.
#
# It resolves the windows amd64/arm64 .zip release assets, reads their sha256
# digests from the GitHub API (falling back to downloading the asset when the API
# does not expose a digest) and substitutes them into the templates under
# development/windows/winget and development/windows/scoop.
#
# The release currently ships bare versioned .exe assets; the per-arch .zip
# archives required for InstallerType: zip + PortableCommandAlias are produced by
# a follow-up build.yaml change (see development/windows/README.md). Until then
# the script fails with an explicit message, or you can point it at a release
# JSON fixture with --release-json for offline rendering tests.
#
# Usage:
#   GITHUB_TOKEN=... development/windows/winget/render_manifest.sh \
#     --tag open-chat-0.0.614 [--output-dir dist/windows-packages] \
#     [--release-json path/to/release.json] [--dry-run] [--strict] [--submit]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WINDOWS_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
WINGET_DIR="${WINDOWS_DIR}/winget"
SCOOP_DIR="${WINDOWS_DIR}/scoop"

SOURCE_REPO="msgmate-io/open-chat-go"
TAG=""
RELEASE_JSON=""
OUTPUT_DIR="dist/windows-packages"
DRY_RUN="false"
STRICT="false"
SUBMIT="false"
LICENSE="Proprietary"
LICENSE_URL=""

usage() {
  sed -n '2,25p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  cat <<'EOF'

Options:
  --tag <tag>              Release tag to render (required unless --release-json).
  --repo <owner/repo>      Source repository (default: msgmate-io/open-chat-go).
  --release-json <file>    Read the release JSON from a local file instead of the
                           GitHub API (offline rendering/tests).
  --output-dir <dir>       Directory to write rendered manifests to
                           (default: dist/windows-packages).
  --license <spdx>         License identifier for the manifests
                           (default: Proprietary; pick the real SPDX id once a
                           LICENSE file is added).
  --license-url <url>      License URL (default: the source repository URL).
  --dry-run                Render and print, never write or submit.
  --strict                 Fail when submission is not possible (default: warn).
  --submit                 Submit the rendered winget manifest with wingetcreate
                           (Windows only; requires the wingetcreate CLI).
  -h, --help               Show this help.

Authentication:
  Set GITHUB_TOKEN (or GH_TOKEN) to avoid API rate limits. Submission to
  microsoft/winget-pkgs is done with wingetcreate and your own GitHub identity.
EOF
}

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag) TAG="${2:-}"; shift 2 ;;
    --repo) SOURCE_REPO="${2:-}"; shift 2 ;;
    --release-json) RELEASE_JSON="${2:-}"; shift 2 ;;
    --output-dir) OUTPUT_DIR="${2:-}"; shift 2 ;;
    --license) LICENSE="${2:-}"; shift 2 ;;
    --license-url) LICENSE_URL="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --strict) STRICT="true"; shift ;;
    --submit) SUBMIT="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "${TAG}" || -n "${RELEASE_JSON}" ]] || die "--tag or --release-json is required"
LICENSE_URL="${LICENSE_URL:-https://github.com/${SOURCE_REPO}}"

for dir in "${WINGET_DIR}" "${SCOOP_DIR}"; do
  [[ -d "${dir}" ]] || die "assets directory not found: ${dir}"
done

command -v sed >/dev/null 2>&1 || die "sed is required"

TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
if [[ -z "${TOKEN}" ]] && command -v gh >/dev/null 2>&1; then
  TOKEN="$(gh auth token 2>/dev/null || true)"
fi

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "no sha256 tool available (need sha256sum or shasum)"
  fi
}

download_sha256() {
  local url="$1" tmp
  command -v curl >/dev/null 2>&1 || die "curl is required to download assets"
  tmp="$(mktemp)"
  log "  downloading ${url} to compute sha256"
  curl -fsSL -o "${tmp}" "${url}"
  sha256_of "${tmp}"
  rm -f "${tmp}"
}

if [[ -n "${RELEASE_JSON}" ]]; then
  [[ -f "${RELEASE_JSON}" ]] || die "release JSON not found: ${RELEASE_JSON}"
  log "Reading release JSON from ${RELEASE_JSON}"
  release_json="$(cat "${RELEASE_JSON}")"
  [[ -n "${TAG}" ]] || TAG="$(jq -r '.tag_name // ""' <<<"${release_json}")"
else
  command -v jq >/dev/null 2>&1 || die "jq is required"
  command -v curl >/dev/null 2>&1 || die "curl is required"
  log "Resolving release ${SOURCE_REPO}@${TAG}"
  api_body_file="$(mktemp)"
  trap 'rm -f "${api_body_file}"' EXIT
  args=(-sS -o "${api_body_file}" -w '%{http_code}' -X GET
    -H "Accept: application/vnd.github+json"
    -H "X-GitHub-Api-Version: 2022-11-28")
  if [[ -n "${TOKEN}" ]]; then
    args+=(-H "Authorization: Bearer ${TOKEN}")
  fi
  status="$(curl "${args[@]}" "https://api.github.com/repos/${SOURCE_REPO}/releases/tags/${TAG}" || printf '000')"
  [[ "${status}" == "200" ]] || die "failed to fetch release '${TAG}' from ${SOURCE_REPO} (status ${status})"
  release_json="$(cat "${api_body_file}")"
fi
command -v jq >/dev/null 2>&1 || die "jq is required"

resolve_arch() {
  local arch="$1" name url digest hex # windows-amd64 or windows-arm64
  name="$(jq -r --arg suffix "-${arch}.zip" '[.assets[].name | select(startswith("open-chat-") and endswith($suffix))][0] // empty' <<<"${release_json}")"
  if [[ -z "${name}" ]]; then
    log "error: release '${TAG:-$RELEASE_JSON}' has no ${arch}.zip asset."
    log "       The zip archives are produced by the follow-up build.yaml packaging"
    log "       step described in development/windows/README.md."
    return 1
  fi
  url="$(jq -r --arg name "${name}" '.assets[] | select(.name == $name) | .browser_download_url' <<<"${release_json}")"
  digest="$(jq -r --arg name "${name}" '.assets[] | select(.name == $name) | (.digest // "")' <<<"${release_json}")"
  hex="${digest#sha256:}"
  if [[ -z "${hex}" || ! "${hex}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    hex="$(download_sha256 "${url}")"
  fi
  hex="$(printf '%s' "${hex}" | tr '[:upper:]' '[:lower:]')"
  printf '%s\n%s\n%s\n' "${name}" "${url}" "${hex}"
}

mapfile -t AMD_FIELDS < <(resolve_arch "windows-amd64") || die "windows-amd64 .zip asset unavailable"
mapfile -t ARM_FIELDS < <(resolve_arch "windows-arm64") || die "windows-arm64 .zip asset unavailable"

AMD_NAME="${AMD_FIELDS[0]}"
AMD_URL="${AMD_FIELDS[1]}"
AMD_SHA="${AMD_FIELDS[2]}"
ARM_NAME="${ARM_FIELDS[0]}"
ARM_URL="${ARM_FIELDS[1]}"
ARM_SHA="${ARM_FIELDS[2]}"

VERSION="$(sed -E 's/^open-chat-(.*)-windows-amd64\.zip$/\1/' <<<"${AMD_NAME}")"
[[ -n "${VERSION}" && "${VERSION}" != "${AMD_NAME}" ]] || die "could not parse version from ${AMD_NAME}"

log "  tag:         ${TAG:-<release-json>}"
log "  version:     ${VERSION}"
log "  amd64:       ${AMD_NAME} (${AMD_SHA})"
log "  arm64:       ${ARM_NAME} (${ARM_SHA})"
log "  license:     ${LICENSE} (${LICENSE_URL})"

render() {
  local template="$1"
  sed \
    -e "s|@VERSION@|${VERSION}|g" \
    -e "s|@RELEASE_TAG@|${TAG}|g" \
    -e "s|@WINDOWS_AMD64_URL@|${AMD_URL}|g" \
    -e "s|@WINDOWS_AMD64_SHA256@|${AMD_SHA}|g" \
    -e "s|@WINDOWS_ARM64_URL@|${ARM_URL}|g" \
    -e "s|@WINDOWS_ARM64_SHA256@|${ARM_SHA}|g" \
    -e "s|@LICENSE_URL@|${LICENSE_URL}|g" \
    -e "s|@LICENSE@|${LICENSE}|g" \
    "${template}"
}

# render_and_check <template>
render_and_check() {
  local template="$1" rendered
  rendered="$(render "${template}")"
  if grep -qE '@[A-Z0-9_]+@' <<<"${rendered}"; then
    log "error: unsubstituted placeholders remain in ${template}:"
    grep -nE '@[A-Z0-9_]+@' <<<"${rendered}" >&2
    exit 1
  fi
  printf '%s\n' "${rendered}"
}

WINGET_VERSION="$(render_and_check "${WINGET_DIR}/Msgmate.OpenChat.yaml.tmpl")"
WINGET_LOCALE="$(render_and_check "${WINGET_DIR}/Msgmate.OpenChat.locale.en-US.yaml.tmpl")"
WINGET_INSTALLER="$(render_and_check "${WINGET_DIR}/Msgmate.OpenChat.installer.yaml.tmpl")"
SCOOP_MANIFEST="$(render_and_check "${SCOOP_DIR}/open-chat.json.tmpl")"

if [[ "${DRY_RUN}" == "true" ]]; then
  log "--- winget/Msgmate.OpenChat.yaml ---"
  printf '%s\n' "${WINGET_VERSION}" >&2
  log "--- winget/Msgmate.OpenChat.locale.en-US.yaml ---"
  printf '%s\n' "${WINGET_LOCALE}" >&2
  log "--- winget/Msgmate.OpenChat.installer.yaml ---"
  printf '%s\n' "${WINGET_INSTALLER}" >&2
  log "--- scoop/open-chat.json ---"
  printf '%s\n' "${SCOOP_MANIFEST}" >&2
else
  WINGET_OUT="${OUTPUT_DIR}/winget"
  SCOOP_OUT="${OUTPUT_DIR}/scoop"
  mkdir -p "${WINGET_OUT}" "${SCOOP_OUT}"
  printf '%s\n' "${WINGET_VERSION}" > "${WINGET_OUT}/Msgmate.OpenChat.yaml"
  printf '%s\n' "${WINGET_LOCALE}" > "${WINGET_OUT}/Msgmate.OpenChat.locale.en-US.yaml"
  printf '%s\n' "${WINGET_INSTALLER}" > "${WINGET_OUT}/Msgmate.OpenChat.installer.yaml"
  printf '%s\n' "${SCOOP_MANIFEST}" > "${SCOOP_OUT}/open-chat.json"
  log "Wrote winget manifests to ${WINGET_OUT}"
  log "Wrote Scoop recipe to ${SCOOP_OUT}"
fi

if [[ "${SUBMIT}" == "true" ]]; then
  if ! command -v wingetcreate >/dev/null 2>&1; then
    msg="wingetcreate is not installed; skipping submission. Install it with 'winget install Microsoft.WingetCreate' on Windows, or submit the manifests in ${OUTPUT_DIR}/winget manually."
    if [[ "${STRICT}" == "true" ]]; then
      die "${msg}"
    fi
    log "warning: ${msg}"
    exit 0
  fi
  if [[ "${DRY_RUN}" == "true" ]]; then
    log "wingetcreate update Msgmate.OpenChat --version ${VERSION} --urls ${AMD_URL} ${ARM_URL} --submit (not run in --dry-run)"
  else
    log "Submitting winget update with wingetcreate"
    wingetcreate update Msgmate.OpenChat \
      --version "${VERSION}" \
      --urls "${AMD_URL}" "${ARM_URL}" \
      --submit
  fi
fi