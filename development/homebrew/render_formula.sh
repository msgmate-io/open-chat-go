#!/usr/bin/env bash
#
# render_formula.sh renders the open-chat Homebrew formula for a given release
# and (optionally) publishes it to the msgmate-io/homebrew-tap repository.
#
# It resolves the darwin arm64/amd64 release assets, reads their sha256 digests
# from the GitHub API (falling back to downloading the asset when the API does
# not expose a digest) and substitutes them into Formula/open-chat.rb.tmpl.
#
# Usage:
#   GITHUB_TOKEN=... development/homebrew/render_formula.sh \
#     --tag open-chat-staging-0.0.610 [--output /tmp/open-chat.rb] \
#     [--tap-repo msgmate-io/homebrew-tap] [--dry-run] [--strict]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEMPLATE="${SCRIPT_DIR}/Formula/open-chat.rb.tmpl"

SOURCE_REPO="msgmate-io/open-chat-go"
TAG=""
OUTPUT=""
TAP_REPO=""
TAP_PATH="Formula/open-chat.rb"
TAP_BRANCH=""
COMMIT_MESSAGE=""
DRY_RUN="false"
STRICT="false"

usage() {
  sed -n '2,17p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  cat <<'EOF'

Options:
  --tag <tag>              Release tag to render (required).
  --repo <owner/repo>      Source repository (default: msgmate-io/open-chat-go).
  --output <file>          Write the rendered formula to this file.
  --tap-repo <owner/repo>  Publish the rendered formula to this tap repository.
  --tap-path <path>        Formula path inside the tap (default: Formula/open-chat.rb).
  --tap-branch <branch>    Tap branch to write to (default: repository default).
  --message <text>         Commit message when publishing.
  --dry-run                Render and print, never publish.
  --strict                 Fail when publishing is not possible (default: warn only).
  -h, --help               Show this help.

Authentication:
  Set GITHUB_TOKEN (or GH_TOKEN) to a token with repo scope. If the token can
  write to the tap repository the formula is published; otherwise publishing is
  skipped with a warning unless --strict is set.
EOF
}

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag) TAG="${2:-}"; shift 2 ;;
    --repo) SOURCE_REPO="${2:-}"; shift 2 ;;
    --output) OUTPUT="${2:-}"; shift 2 ;;
    --tap-repo) TAP_REPO="${2:-}"; shift 2 ;;
    --tap-path) TAP_PATH="${2:-}"; shift 2 ;;
    --tap-branch) TAP_BRANCH="${2:-}"; shift 2 ;;
    --message) COMMIT_MESSAGE="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --strict) STRICT="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "${TAG}" ]] || die "--tag is required"
[[ -f "${TEMPLATE}" ]] || die "template not found: ${TEMPLATE}"

command -v jq >/dev/null 2>&1 || die "jq is required"
command -v curl >/dev/null 2>&1 || die "curl is required"

TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
if [[ -z "${TOKEN}" ]] && command -v gh >/dev/null 2>&1; then
  TOKEN="$(gh auth token 2>/dev/null || true)"
fi

API_BODY_FILE="$(mktemp)"
trap 'rm -f "${API_BODY_FILE}"' EXIT

# api_request <method> <path> [json-body]
# Writes the response body to API_BODY_FILE and prints the HTTP status code.
api_request() {
  local method="$1" path="$2" body="${3:-}"
  local -a args=(-sS -o "${API_BODY_FILE}" -w '%{http_code}' -X "${method}"
    -H "Accept: application/vnd.github+json"
    -H "X-GitHub-Api-Version: 2022-11-28")
  if [[ -n "${TOKEN}" ]]; then
    args+=(-H "Authorization: Bearer ${TOKEN}")
  fi
  if [[ -n "${body}" ]]; then
    args+=(-H "Content-Type: application/json" -d "${body}")
  fi
  curl "${args[@]}" "https://api.github.com${path}" || printf '000'
}

api_body() { cat "${API_BODY_FILE}"; }

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
  tmp="$(mktemp)"
  log "  downloading ${url} to compute sha256"
  curl -fsSL -o "${tmp}" "${url}"
  sha256_of "${tmp}"
  rm -f "${tmp}"
}

log "Resolving release ${SOURCE_REPO}@${TAG}"
release_status="$(api_request GET "/repos/${SOURCE_REPO}/releases/tags/${TAG}")"
[[ "${release_status}" == "200" ]] || die "failed to fetch release '${TAG}' from ${SOURCE_REPO} (status ${release_status})"
release_json="$(api_body)"

resolve_arch() {
  local suffix="$1" name url digest hex # darwin-arm64 or darwin-amd64
  name="$(jq -r --arg suffix "-${suffix}" '[.assets[].name | select(startswith("open-chat-") and endswith($suffix))][0] // empty' <<<"${release_json}")"
  [[ -n "${name}" ]] || die "release '${TAG}' has no ${suffix} asset"
  url="$(jq -r --arg name "${name}" '.assets[] | select(.name == $name) | .browser_download_url' <<<"${release_json}")"
  digest="$(jq -r --arg name "${name}" '.assets[] | select(.name == $name) | (.digest // "")' <<<"${release_json}")"
  hex="${digest#sha256:}"
  if [[ -z "${hex}" || ! "${hex}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    hex="$(download_sha256 "${url}")"
  fi
  hex="$(printf '%s' "${hex}" | tr '[:upper:]' '[:lower:]')"
  printf '%s\n%s\n%s\n' "${name}" "${url}" "${hex}"
}

mapfile -t ARM_FIELDS < <(resolve_arch "darwin-arm64")
mapfile -t AMD_FIELDS < <(resolve_arch "darwin-amd64")

ARM_NAME="${ARM_FIELDS[0]}"
ARM_URL="${ARM_FIELDS[1]}"
ARM_SHA="${ARM_FIELDS[2]}"
AMD_NAME="${AMD_FIELDS[0]}"
AMD_URL="${AMD_FIELDS[1]}"
AMD_SHA="${AMD_FIELDS[2]}"

VERSION="$(sed -E 's/^open-chat-(.*)-darwin-arm64$/\1/' <<<"${ARM_NAME}")"
[[ -n "${VERSION}" && "${VERSION}" != "${ARM_NAME}" ]] || die "could not parse version from ${ARM_NAME}"

log "  version:     ${VERSION}"
log "  arm64:       ${ARM_NAME} (${ARM_SHA})"
log "  amd64:       ${AMD_NAME} (${AMD_SHA})"

RENDERED="$(sed \
  -e "s|@VERSION@|${VERSION}|g" \
  -e "s|@DARWIN_ARM64_URL@|${ARM_URL}|g" \
  -e "s|@DARWIN_ARM64_SHA256@|${ARM_SHA}|g" \
  -e "s|@DARWIN_AMD64_URL@|${AMD_URL}|g" \
  -e "s|@DARWIN_AMD64_SHA256@|${AMD_SHA}|g" \
  "${TEMPLATE}")"

if grep -qE '@[A-Z_]+@' <<<"${RENDERED}"; then
  die "unsubstituted placeholders remain in rendered formula"
fi

if [[ -n "${OUTPUT}" ]]; then
  printf '%s\n' "${RENDERED}" > "${OUTPUT}"
  log "Wrote ${OUTPUT}"
fi

publish() {
  local rendered="$1" encoded current current_json current_sha payload status current_status
  encoded="$(printf '%s\n' "${rendered}" | base64 | tr -d '\n')"

  current_status="$(api_request GET "/repos/${TAP_REPO}/contents/${TAP_PATH}${TAP_BRANCH:+?ref=${TAP_BRANCH}}")"
  current_sha=""
  if [[ "${current_status}" == "200" ]]; then
    current_json="$(api_body)"
    current_sha="$(jq -r '.sha // empty' <<<"${current_json}")"
    current="$(jq -r '.content' <<<"${current_json}" | tr -d '\n' | base64 -d 2>/dev/null || true)"
    if [[ "${current}" == "${rendered}" ]]; then
      log "Tap formula ${TAP_REPO}/${TAP_PATH} is already up to date; nothing to publish."
      return 0
    fi
  elif [[ "${current_status}" != "404" ]]; then
    if [[ "${STRICT}" == "true" ]]; then
      die "failed to read ${TAP_REPO}/${TAP_PATH} (status ${current_status})"
    fi
    log "warning: could not read ${TAP_REPO}/${TAP_PATH} (status ${current_status}); attempting publish anyway."
  fi

  payload="$(jq -n \
    --arg message "${COMMIT_MESSAGE}" \
    --arg content "${encoded}" \
    --arg branch "${TAP_BRANCH}" \
    --arg sha "${current_sha}" \
    '{message: $message, content: $content} + (if $branch != "" then {branch: $branch} else {} end) + (if $sha != "" then {sha: $sha} else {} end)')"

  status="$(api_request PUT "/repos/${TAP_REPO}/contents/${TAP_PATH}" "${payload}")"

  if [[ "${status}" == "200" || "${status}" == "201" ]]; then
    log "Published ${TAP_REPO}/${TAP_PATH} (status ${status})."
    return 0
  fi

  if [[ "${STRICT}" == "true" ]]; then
    api_body >&2
    die "failed to publish to ${TAP_REPO} (status ${status:-unknown})"
  fi
  log "warning: could not publish to ${TAP_REPO} (status ${status:-unknown}); token likely lacks write access. Use --output to persist the formula."
  return 0
}

if [[ -n "${TAP_REPO}" && "${DRY_RUN}" != "true" ]]; then
  COMMIT_MESSAGE="${COMMIT_MESSAGE:-open-chat ${VERSION}}"
  publish "${RENDERED}"
fi

if [[ "${DRY_RUN}" == "true" || -z "${OUTPUT}" ]]; then
  printf '%s\n' "${RENDERED}"
fi