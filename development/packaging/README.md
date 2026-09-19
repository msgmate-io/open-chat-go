# Debian packaging & APT repository

This directory contains the packaging assets for the `open-chat` Debian
package and the tooling that publishes a signed APT repository to GitHub
Pages.

## Layout

| File | Purpose |
| --- | --- |
| `nfpm.yaml` | [nFPM](https://nfpm.goreleaser.com) package definition (deb, with an rpm override). |
| `open-chat.service` | Native systemd unit installed to `/usr/lib/systemd/system/open-chat.service`. |
| `open-chat.json` | Default conffile installed to `/etc/open-chat/open-chat.json` (0600 `open-chat:open-chat`). |
| `open-chat.sysusers` | `systemd-sysusers` definition for the `open-chat` service account. |
| `open-chat.tmpfiles` | `systemd-tmpfiles` definition for the state/config directories. |
| `scripts/*.sh` | Maintainer scripts embedded by nFPM (`preinst`/`postinst`/`prerm`/`postrm`). |
| `build-deb.sh` | Builds a single `.deb` from an already-built linux binary. |
| `build-apt-repo.sh` | Assembles (and optionally signs) an APT repository from a directory of `.deb` files. |

The package installs:

- `/usr/bin/open-chat` (0755)
- `/usr/lib/systemd/system/open-chat.service` (0644)
- `/etc/open-chat/open-chat.json` (0600, conffile)
- `/usr/lib/sysusers.d/open-chat.conf`, `/usr/lib/tmpfiles.d/open-chat.conf`
- `/etc/open-chat` (0750 `root:open-chat`) and `/var/lib/open-chat` (0750 `open-chat:open-chat`)

## Building a package locally

Install nFPM (matching CI):

```bash
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
```

Build a static binary and package it (the binary must be `CGO_ENABLED=0`):

```bash
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 INTEGRATION_PROFILE=core-only \
  OPEN_CHAT_RELEASE_BUILD=1 ./full_build.sh
cd ..
ARCH=amd64 VERSION=0.0.611 BIN=backend/backend \
  development/packaging/build-deb.sh
```

Inspect the result with `dpkg-deb -I dist/open-chat_0.0.611_amd64.deb` and
`dpkg-deb -c dist/open-chat_0.0.611_amd64.deb`, then `lintian` it.

## Building the APT repository locally

`build-apt-repo.sh` requires `dpkg-scanpackages`, `apt-ftparchive` and
`gzip` (from `dpkg-dev`/`apt-utils` on Debian/Ubuntu); add `gpg` to sign:

```bash
DEB_DIR=dist OUT_DIR=apt-repo SUITE=stable ARCHITECTURES="amd64 arm64" \
  development/packaging/build-apt-repo.sh
```

The repository layout is:

```
apt-repo/
  open-chat.asc                          # exported public key (when signing)
  pool/main/o/open-chat/open-chat_*.deb
  dists/stable/Release
  dists/stable/InRelease                 # clearsigned (when signing)
  dists/stable/Release.gpg               # detached (when signing)
  dists/stable/main/binary-amd64/Packages(.gz)
  dists/stable/main/binary-arm64/Packages(.gz)
```

Verify a signed repository with `gpg --verify apt-repo/dists/stable/InRelease`
and install it locally by pointing an apt source at the directory
(`deb [signed-by=...] file:/absolute/path/apt-repo stable main`).

## CI

`.github/workflows/build.yaml` builds the `.deb` for `amd64`/`arm64` in the
`package-deb` job and attaches them to the GitHub Release. The
`deploy-storybook-github-pages.yaml` workflow assembles the Storybook site and
republishes `/apt` on GitHub Pages; only the three most recent stable releases
are published to stay within the Pages size limit.

### Signing key

Generate a dedicated signing key offline (for example on a maintainer machine):

```bash
gpg --batch --generate-key <<'EOF'
%no-protection
Key-Type: RSA
Key-Length: 4096
Name-Real: Open Chat APT
Name-Email: dev@msgmate.io
Expire-Date: 2y
%commit
EOF
gpg --armor --export-secret-keys dev@msgmate.io > apt-signing-key.asc
```

Add the armored private key and its passphrase as repository secrets:

- `APT_GPG_PRIVATE_KEY` — contents of `apt-signing-key.asc`
- `APT_GPG_PASSPHRASE` — the key passphrase (empty for an unprotected key)

When `APT_GPG_PRIVATE_KEY` is unset the Pages workflow skips APT publication
and only deploys Storybook, so forks and unconfigured repositories keep
working.

Key rotation: generate a new key, keep the old public key in the repository for
a transition period, update the secrets, then remove the old key once clients
have updated their keyring. Because clients reference the key via
`signed-by`, rotating requires clients to fetch the new `open-chat.asc`.

## RPM (follow-up)

`nfpm.yaml` already carries an rpm override (`config|noreplace` plus systemd
dependencies) so `nfpm package --packager rpm` can produce an `.rpm`. Publishing
an rpm repository (e.g. a `repomd.xml` on Pages or COPR) is intentionally left
as a follow-up.
