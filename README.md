## Open Chat Go

> 10th generation of Open Chat, written in Go. Without [federation](https://github.com/msgmate-io/open-chat-go) for now; [would love to add it again though](https://blog.t1m.me/blog/designing-a-decentral-vpn-protocol-w-libp2p).

- Production: [`msgmate.io`](https://msgmate.io) ( branch [`production`](https://github.com/msgmate-io/open-chat-go/tree/production) ) — [latest stable binary](https://github.com/msgmate-io/open-chat-go/releases/download/open-chat-0.0.565/open-chat-0.0.566-linux-amd64)
- Staging: [`stage.msgmate.io`](https://stage.msgmate.io) ( branch [`main`](https://github.com/msgmate-io/open-chat-go) ) — [latest unstable binary](https://github.com/msgmate-io/open-chat-go/releases/download/open-chat-staging-0.0.595/open-chat-0.0.596-linux-amd64)
- Docs: [`msgmate.io/docs`](https://msgmate.io/docs)
- Getting started: [`msgmate.io/docs#getting-started`](https://msgmate.io/docs#getting-started)
- Development: [`msgmate.io/docs#development`](https://msgmate.io/docs#development)
- API Docs: [`msgmate.io/reference`](https://msgmate.io/reference)
- Design System: [`msgmate-io.github.io/open-chat-go`](https://msgmate-io.github.io/open-chat-go/)

### Infuse Open-Chat Tools & Integrations

- Use the [go-integration-interace](https://github.com/msgmate-io/open-chat-go-integration-interface) and place integrations in `backend/integrationdeps.json`
- Integrations can register tools directly via `Definition.ToolDefinitions` using [go-tool-interface](https://github.com/msgmate-io/open-chat-go-tool-interface)

### Releases

We release all versions always ( after admin confirmation ):

- PR branches: `open-chat-pr-alpha-release-<version-number>-<commit>`
- Staging `main` are tagged as `open-chat-staging-<version-number>` ( `open-chat-pre-release:latest` )
- Production `production` are released as `open-chat-<version-number>` ( `open-chat:latest` )

### Windows

winget is the recommended Windows channel (Scoop is a secondary, admin-free
option). Until the package is listed, download the `windows-amd64` or
`windows-arm64` asset from the [latest release](https://github.com/msgmate-io/open-chat-go/releases)
and register the native Windows service from an **elevated** PowerShell:

```powershell
# winget (once published)
winget install Msgmate.OpenChat

# or Scoop (once the bucket is published)
scoop bucket add msgmate https://github.com/msgmate-io/scoop-bucket
scoop install msgmate/open-chat

# register and start the service (admin shell)
open-chat install
open-chat status   # service: running, server: running on 127.0.0.1:1984
```

`open-chat install` copies the binary to `%ProgramFiles%\OpenChat`, stores its
data and config under `%ProgramData%\OpenChat` and registers the service with the
Windows SCM. Because the service runs that copied binary, re-run
`open-chat install --force` from an elevated shell after every
`winget upgrade`/`scoop update` so the service picks up the new version.

See [`development/windows/README.md`](development/windows/README.md) for the full
channel comparison, the winget/Scoop manifest templates and the upgrade runbook.
