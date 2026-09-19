#!/usr/bin/env bash
# Update nix/package.nix to a new open-chat release.
#
# Usage:
#   ./nix/update.sh                       # latest non-draft GitHub release
#   ./nix/update.sh <release-tag>         # e.g. open-chat-staging-0.0.610
#
# The release tag and the version embedded in the binary asset names can
# differ (the build bumps the version before packaging), so the script also
# accepts an explicit asset version:
#   ./nix/update.sh open-chat-staging-0.0.610 0.0.611
set -euo pipefail

REPO="msgmate-io/open-chat-go"
PKG_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/package.nix"

TAG="${1:-}"
VERSION="${2:-}"

if [[ -z "$TAG" ]]; then
  TAG="$(gh release view --repo "$REPO" --json tagName -q .tagName)"
  echo "Using latest release tag: $TAG"
fi

if [[ -z "$VERSION" ]]; then
  # Derive from the first linux-amd64 asset name: open-chat-<version>-linux-amd64
  VERSION="$(gh release view "$TAG" --repo "$REPO" --json assets \
    -q '.assets[].name | select(test("linux-amd64$"))' \
    | sed -E 's/^open-chat-(.*)-linux-amd64$/\1/')"
fi

if [[ -z "$VERSION" ]]; then
  echo "error: could not determine asset version; pass it as the second argument" >&2
  exit 1
fi

echo "Release tag:   $TAG"
echo "Asset version: $VERSION"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

declare -A HASHES
for system in x86_64-linux aarch64-linux x86_64-darwin aarch64-darwin; do
  os="${system#*-}"
  arch="${system%-*}"
  case "$os" in
    linux) os="linux" ;;
    darwin) os="darwin" ;;
  esac
  case "$arch" in
    x86_64) arch="amd64" ;;
    aarch64) arch="arm64" ;;
  esac
  asset="open-chat-${VERSION}-${os}-${arch}"
  echo "Fetching $asset..."
  gh release download "$TAG" --repo "$REPO" --pattern "$asset" --dir "$tmp" --clobber
  hex="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
  HASHES[$system]="$(nix hash to-sri --type sha256 "$hex")"
done

python3 - "$PKG_FILE" "$VERSION" "$TAG" "${HASHES[x86_64-linux]}" \
  "${HASHES[aarch64-linux]}" "${HASHES[x86_64-darwin]}" "${HASHES[aarch64-darwin]}" <<'PY'
import re
import sys

path, version, tag, *hashes = sys.argv[1:]
systems = ["x86_64-linux", "aarch64-linux", "x86_64-darwin", "aarch64-darwin"]

text = open(path, encoding="utf-8").read()
text = re.sub(r'version \? "[^"]*"', f'version ? "{version}"', text, count=1)
text = re.sub(r'releaseTag \? "[^"]*"', f'releaseTag ? "{tag}"', text, count=1)

for system, digest in zip(systems, hashes):
    text = re.sub(
        rf'({system} = )"sha256-[^"]*"',
        rf'\g<1>"{digest}"',
        text,
    )

open(path, "w", encoding="utf-8").write(text)
print("Updated", path)
PY

echo "Done. Review the diff and run: nix build .#open-chat"