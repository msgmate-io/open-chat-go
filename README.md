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

### Nix / NixOS

A flake provides `packages.<system>.open-chat`, an overlay and a hardened
`nixosModules.open-chat` / `services.open-chat` module:

```bash
nix run github:msgmate-io/open-chat-go
nix build .#open-chat
```

The flake packages the official release binary (frontend, swagger and
integrations embedded). See [`nix/README.md`](nix/README.md) for the NixOS
module options and for why building Go from source in the sandbox is not
supported yet.
