# Portable Linux AppImage

This directory contains the assets used to package `open-chat` as a portable
[AppImage](https://appimage.org/). The resulting `open-chat-<version>-x86_64.AppImage`
(and `-aarch64.AppImage`) can be downloaded from the GitHub Release and executed
without installation.

## Layout

| File | Purpose |
| --- | --- |
| `build-appimage.sh` | Assembles the AppDir and runs `appimagetool` to produce the `.AppImage`. |
| `AppRun` | Entrypoint wrapper: XDG defaults, first-run credentials, single instance, optional browser. |
| `open-chat.desktop` | Desktop entry (`Exec`, `Icon`, categories) used for desktop integration. |
| `open-chat.png` | 512x512 application icon (derived from `frontend/assets/logo.png`). |

## Building locally

The binary must be statically linked (`CGO_ENABLED=0`) so the AppImage runs on
older glibc distributions (Ubuntu 22.04, Fedora) without bundling glibc. Build
it first:

```bash
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 INTEGRATION_PROFILE=core-only ./full_build.sh
cd ..
```

Then package it:

```bash
ARCH=amd64 VERSION=0.0.611 BIN=backend/backend \
  development/packaging/appimage/build-appimage.sh
```

`build-appimage.sh` downloads a pinned `appimagetool` (`1.9.1`) and
`type2-runtime` (`20251108`) into `~/.cache/open-chat-appimage` on first use, so
no manual tooling is required. Set `APPIMAGETOOL=/path/to/appimagetool` to use a
local copy. `APPIMAGE_UPDATE_INFO` can be set to embed
[AppImage update information](https://docs.appimage.org/packaging-guide/optional/updates.html)
(and generate a zsync file when `zsyncmake` is available).

The `package-appimage` job in `.github/workflows/build.yaml` does this for
`amd64` and `arm64` (cross-built with the pinned `aarch64` runtime) and attaches
the result to the GitHub Release.

## Running

```bash
chmod +x open-chat-0.0.611-x86_64.AppImage
./open-chat-0.0.611-x86_64.AppImage
```

The wrapper starts `open-chat server` with portable defaults:

| Setting | Default |
| --- | --- |
| Database | `$XDG_DATA_HOME/open-chat/data.db` |
| Config/credentials | `$XDG_CONFIG_HOME/open-chat/` |
| Redis | embedded (no external service) |
| HTTP endpoint | `http://127.0.0.1:1984` |

On the first run a random, policy-compliant `admin` password is generated and
saved to `$XDG_CONFIG_HOME/open-chat/root-credentials` (mode `0600`); the
credentials are printed once. Set `ROOT_CREDENTIALS=admin:<password>` to provide
your own, or edit the credentials file to rotate it.

Any open-chat environment variable can be used to override the defaults
(`HOST`, `PORT`, `DB_PATH`, `REDIS_MODE`, `DEBUG`, `OPEN_CHAT_CONFIG`, ...).
AppImage-specific knobs:

- `OPEN_CHAT_DATA_DIR` / `OPEN_CHAT_CONFIG_DIR` — override the directories above.
- `OPEN_CHAT_OPEN_BROWSER=1` — open the UI in a browser once the server is up.
- `OPEN_CHAT_APPIMAGE_COMMAND` — subcommand to run (default `server`).

If a server already answers on the configured address the wrapper does not
start a second instance; it optionally opens the browser and exits.

### FUSE

AppImages normally need FUSE 2 (`libfuse.so.2`). When FUSE is unavailable
(containers, minimal servers, CI) use the runtime fallback:

```bash
./open-chat-0.0.611-x86_64.AppImage --appimage-extract-and-run
```

Alternatively install `fuse`/`libfuse2` on the host. The CI smoke test uses
`--appimage-extract-and-run` because GitHub-hosted runners do not provide FUSE.
