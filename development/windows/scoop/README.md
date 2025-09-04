# open-chat Scoop bucket (Windows)

Secondary Windows distribution channel for open-chat. The primary channel is
winget (see [`../README.md`](../README.md)); Scoop is offered for users who
prefer a user-scope, admin-free package manager.

```powershell
scoop bucket add msgmate https://github.com/msgmate-io/scoop-bucket
scoop install msgmate/open-chat
open-chat install          # elevated: registers the Windows service
open-chat status
```

`scoop install` itself does not require administrator rights. Registering the
Windows service still does, so `open-chat install` must be run once from an
elevated PowerShell.

## Layout

| Path | Purpose |
| --- | --- |
| `open-chat.json.tmpl` | Bucket recipe with `@VERSION@`, `@WINDOWS_*_URL@`, `@WINDOWS_*_SHA256@` and `@LICENSE@` placeholders. |
| `../winget/render_manifest.sh` | Shared renderer: resolves the release's windows `.zip` assets, derives the version from the asset name and renders this recipe (and the winget manifests). |
| `msgmate-io/scoop-bucket` | Public bucket repository containing the rendered `bucket/open-chat.json`. |

Unlike winget, Scoop reads the recipe straight from the bucket repository, so
publishing is a commit to `msgmate-io/scoop-bucket` (no upstream review). The
shared renderer writes the file to `<output-dir>/scoop/open-chat.json`; the
release automation copies that into the bucket repository.

## Strategy

- **User scope:** Scoop installs into the user profile and shims `open-chat.exe`
  into the Scoop `shims` directory, so no admin rights are needed to install or
  update the binary.
- **Architecture selection:** `architecture.64bit` and `architecture.arm64` are
  both declared; Scoop picks the matching archive.
- **Updates:** `checkver` watches the GitHub releases for
  `open-chat-<version>-windows-amd64`, so `scoop checkver msgmate/open-chat -u`
  can bump the recipe. `autoupdate` uses `hash.mode: download` because the
  release does not publish a checksum file.
- **Service caveat:** like winget, Scoop updates the *package* copy of the
  binary, not the copy the Windows service is registered against. Re-run
  `open-chat install --force` after every `scoop update` (see the runbook in
  [`../README.md`](../README.md)).

## Maintaining the bucket

On a Windows machine (or the release automation, which runs on Windows):

```powershell
# Render the recipe for a release and copy it into the bucket checkout:
git clone https://github.com/msgmate-io/scoop-bucket
GITHUB_TOKEN=... bash development/windows/winget/render_manifest.sh `
  --tag open-chat-0.0.614 --output-dir dist/windows-packages
Copy-Item dist/windows-packages/scoop/open-chat.json scoop-bucket/bucket/open-chat.json
cd scoop-bucket
git add bucket/open-chat.json
git commit -m "open-chat 0.0.614"
git push
```

Bump automatically with the Scoop CLI when the manifest is already published:

```powershell
scoop checkver msgmate/open-chat -u
scoop update
```

## Verifying a release manually

```powershell
scoop bucket add msgmate https://github.com/msgmate-io/scoop-bucket
scoop install msgmate/open-chat
open-chat --help
open-chat install          # from an elevated shell
open-chat status           # service: running, server: running on 127.0.0.1:1984
scoop uninstall open-chat
```

`Get-FileHash <archive> -Algorithm SHA256` can be used to confirm the hash stored
in `open-chat.json` before publishing.