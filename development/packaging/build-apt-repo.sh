#!/usr/bin/env bash
#
# Assemble (and optionally sign) an APT repository from a directory of .deb
# packages.
#
# Required environment variables:
#   DEB_DIR  directory containing the .deb files to publish
#   OUT_DIR  repository root to create
# Optional:
#   SUITE, CODENAME, COMPONENT, ORIGIN, LABEL, DESCRIPTION
#   ARCHITECTURES       space separated (default "amd64 arm64")
#   KEY_ID              GPG key id/fingerprint; when set the repo is signed
#   GPG_PASSPHRASE      passphrase for KEY_ID (empty for an unprotected key)
set -euo pipefail

DEB_DIR="${DEB_DIR:-}"
OUT_DIR="${OUT_DIR:-}"
SUITE="${SUITE:-stable}"
CODENAME="${CODENAME:-stable}"
COMPONENT="${COMPONENT:-main}"
ORIGIN="${ORIGIN:-Open Chat}"
LABEL="${LABEL:-Open Chat}"
DESCRIPTION="${DESCRIPTION:-Open Chat apt repository}"
ARCHITECTURES="${ARCHITECTURES:-amd64 arm64}"
KEY_ID="${KEY_ID:-}"
GPG_PASSPHRASE="${GPG_PASSPHRASE:-}"
POOL_COMPONENT_PATH="${POOL_COMPONENT_PATH:-main/o/open-chat}"

if [[ -z "${DEB_DIR}" || -z "${OUT_DIR}" ]]; then
  echo "Usage: DEB_DIR=<dir with .deb> OUT_DIR=<repo root> $0" >&2
  exit 1
fi
if [[ ! -d "${DEB_DIR}" ]]; then
  echo "Error: DEB_DIR '${DEB_DIR}' is not a directory" >&2
  exit 1
fi

for tool in dpkg-scanpackages apt-ftparchive gzip; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "Error: required tool '${tool}' not found" >&2
    exit 1
  fi
done

POOL_DIR="${OUT_DIR}/pool/${POOL_COMPONENT_PATH}"
SUITE_DIR="${OUT_DIR}/dists/${SUITE}"
COMPONENT_DIR="${SUITE_DIR}/${COMPONENT}"
mkdir -p "${POOL_DIR}" "${COMPONENT_DIR}"

find "${DEB_DIR}" -type f -name '*.deb' -print0 | while IFS= read -r -d '' deb; do
  cp -f "${deb}" "${POOL_DIR}/$(basename "${deb}")"
done

deb_count="$(find "${POOL_DIR}" -maxdepth 1 -type f -name '*.deb' | wc -l | tr -d ' ')"
if [[ "${deb_count}" == "0" ]]; then
  echo "Error: no .deb files found under ${DEB_DIR}" >&2
  exit 1
fi
echo "Pooling ${deb_count} package(s) into ${POOL_DIR}"

# Per-architecture Packages indices (dpkg-scanpackages paths are relative to
# OUT_DIR, which is why we run it from there).
for arch in ${ARCHITECTURES}; do
  target_dir="${COMPONENT_DIR}/binary-${arch}"
  mkdir -p "${target_dir}"
  (
    cd "${OUT_DIR}"
    dpkg-scanpackages --arch "${arch}" "pool" /dev/null > "${target_dir}/Packages"
  )
  gzip -9c "${target_dir}/Packages" > "${target_dir}/Packages.gz"
done

# Top-level Release file describing the distribution.
(
  cd "${SUITE_DIR}"
  apt-ftparchive \
    -o "APT::FTPArchive::Release::Origin=${ORIGIN}" \
    -o "APT::FTPArchive::Release::Label=${LABEL}" \
    -o "APT::FTPArchive::Release::Suite=${SUITE}" \
    -o "APT::FTPArchive::Release::Codename=${CODENAME}" \
    -o "APT::FTPArchive::Release::Architectures=${ARCHITECTURES}" \
    -o "APT::FTPArchive::Release::Components=${COMPONENT}" \
    -o "APT::FTPArchive::Release::Description=${DESCRIPTION}" \
    release . > Release
)

# Sign the Release file when a key is configured; otherwise leave the repo
# unsigned (consumers then need [trusted=yes]).
if [[ -n "${KEY_ID}" ]]; then
  if ! command -v gpg >/dev/null 2>&1; then
    echo "Error: KEY_ID is set but gpg is not installed" >&2
    exit 1
  fi

  GPG_ARGS=(--batch --yes --pinentry-mode loopback --local-user "${KEY_ID}")
  passfile=""
  if [[ -n "${GPG_PASSPHRASE}" ]]; then
    passfile="$(mktemp)"
    chmod 600 "${passfile}"
    printf '%s' "${GPG_PASSPHRASE}" > "${passfile}"
    GPG_ARGS+=(--passphrase-file "${passfile}")
  fi

  cleanup() {
    if [[ -n "${passfile}" ]]; then
      rm -f "${passfile}"
    fi
    return 0
  }
  trap cleanup EXIT

  gpg "${GPG_ARGS[@]}" --clearsign -o "${SUITE_DIR}/InRelease" "${SUITE_DIR}/Release"
  gpg "${GPG_ARGS[@]}" --armor --detach-sign -o "${SUITE_DIR}/Release.gpg" "${SUITE_DIR}/Release"
  gpg --batch --yes --armor --export "${KEY_ID}" > "${OUT_DIR}/open-chat.asc"
  echo "Signed ${SUITE_DIR}/InRelease"
else
  echo "KEY_ID not set; leaving the repository unsigned"
fi

echo "APT repository written to ${OUT_DIR}"
