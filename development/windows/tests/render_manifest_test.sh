#!/usr/bin/env bash
#
# Offline test for development/windows/winget/render_manifest.sh.
#
# Uses tests/release-fixture.json (a synthetic GitHub release response that,
# like a real release, has a tag that is one version behind the asset name) to
# assert that the script resolves the .zip assets, derives the version from the
# asset name (not the tag), substitutes every placeholder and renders the winget
# and Scoop manifests. No network access is required.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WINDOWS_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
RENDER="${WINDOWS_DIR}/winget/render_manifest.sh"
FIXTURE="${SCRIPT_DIR}/release-fixture.json"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf 'ok: %s\n' "$*"; }

[[ -x "${RENDER}" ]] || fail "render_manifest.sh is not executable: ${RENDER}"
[[ -f "${FIXTURE}" ]] || fail "fixture not found: ${FIXTURE}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

# 1. Dry run: everything printed on stderr, no files written.
DRY_OUT="${TMP_DIR}/dry-run.txt"
"${RENDER}" --release-json "${FIXTURE}" --dry-run > "${DRY_OUT}" 2>&1 \
  || fail "dry-run render failed"

grep -q 'PackageVersion: "0.0.614"' "${DRY_OUT}" \
  || fail "winget version manifest did not use the asset version"
grep -q '"version": "0.0.614"' "${DRY_OUT}" \
  || fail "Scoop recipe did not use the asset version"
grep -q 'open-chat-0.0.614-windows-amd64.zip' "${DRY_OUT}" \
  || fail "amd64 zip URL missing from rendered output"
grep -q 'open-chat-0.0.614-windows-arm64.zip' "${DRY_OUT}" \
  || fail "arm64 zip URL missing from rendered output"
grep -q '1111111111111111111111111111111111111111111111111111111111111111' "${DRY_OUT}" \
  || fail "amd64 digest missing from rendered output"
grep -q '2222222222222222222222222222222222222222222222222222222222222222' "${DRY_OUT}" \
  || fail "arm64 digest missing from rendered output"
if grep -qE '@[A-Z0-9_]+@' "${DRY_OUT}"; then
  grep -nE '@[A-Z0-9_]+@' "${DRY_OUT}" >&2 || true
  fail "unsubstituted placeholders remain"
fi
pass "dry-run renders every placeholder and derives the asset version"

# 2. Real render: the four manifest files are written.
OUT_DIR="${TMP_DIR}/out"
"${RENDER}" --release-json "${FIXTURE}" --output-dir "${OUT_DIR}" --license Apache-2.0 \
  --license-url https://www.apache.org/licenses/LICENSE-2.0 \
  > "${TMP_DIR}/render.log" 2>&1 \
  || fail "render to output dir failed"

for f in \
  "${OUT_DIR}/winget/Msgmate.OpenChat.yaml" \
  "${OUT_DIR}/winget/Msgmate.OpenChat.locale.en-US.yaml" \
  "${OUT_DIR}/winget/Msgmate.OpenChat.installer.yaml" \
  "${OUT_DIR}/scoop/open-chat.json"; do
  [[ -s "${f}" ]] || fail "expected rendered file missing: ${f}"
done
pass "render writes winget version, locale, installer and Scoop files"

grep -q 'License: "Apache-2.0"' "${OUT_DIR}/winget/Msgmate.OpenChat.locale.en-US.yaml" \
  || fail "--license was not substituted"
grep -q '"license": "Apache-2.0"' "${OUT_DIR}/scoop/open-chat.json" \
  || fail "--license was not substituted into the Scoop recipe"

if command -v python3 >/dev/null 2>&1; then
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' \
    "${OUT_DIR}/scoop/open-chat.json" \
    || fail "rendered Scoop recipe is not valid JSON"
  pass "rendered Scoop recipe is valid JSON"
fi

# 3. A release without .zip assets must fail with a clear message.
NOZIP_FIXTURE="${TMP_DIR}/nozip.json"
cat > "${NOZIP_FIXTURE}" <<'JSON'
{
  "tag_name": "open-chat-0.0.613",
  "assets": [
    {
      "name": "open-chat-0.0.614-windows-amd64.exe",
      "browser_download_url": "https://github.com/msgmate-io/open-chat-go/releases/download/open-chat-0.0.613/open-chat-0.0.614-windows-amd64.exe",
      "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    }
  ]
}
JSON
if "${RENDER}" --release-json "${NOZIP_FIXTURE}" --dry-run > "${TMP_DIR}/nozip.log" 2>&1; then
  fail "render succeeded although no .zip asset is present"
fi
grep -q 'no windows-amd64.zip asset' "${TMP_DIR}/nozip.log" \
  || fail "missing-zip failure did not explain the required follow-up"
pass "release without .zip assets fails with an actionable error"

printf '\nAll render_manifest.sh tests passed.\n'