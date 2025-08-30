## Open Chat Go

> 10th generation of Open Chat, written in Go. Without [federation](https://github.com/msgmate-io/open-chat-go) for now; [would love to add it again though](https://blog.t1m.me/blog/designing-a-decentral-vpn-protocol-w-libp2p).

- Production: [`msgmate.io`](https://msgmate.io) ( branch [`production`](https://github.com/msgmate-io/open-chat-go/tree/production) ) — [latest stable release](https://github.com/msgmate-io/open-chat-go/releases/latest)
- Staging: [`stage.msgmate.io`](https://stage.msgmate.io) ( branch [`main`](https://github.com/msgmate-io/open-chat-go) ) — [latest unstable release](https://github.com/msgmate-io/open-chat-go/releases)
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

### Debian / Ubuntu (apt)

Debian and Ubuntu packages are published to a signed APT repository hosted on
GitHub Pages. Ubuntu 22.04/24.04 and Debian 12 are supported.

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://msgmate-io.github.io/open-chat-go/apt/open-chat.asc \
  | sudo tee /etc/apt/keyrings/open-chat.asc >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/open-chat.asc] https://msgmate-io.github.io/open-chat-go/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/open-chat.list
sudo apt-get update
sudo apt-get install open-chat
```

The package creates an `open-chat` system user, installs a systemd unit and
starts the server on `127.0.0.1:1984`. State lives in `/var/lib/open-chat`
(`data.db`) and configuration in `/etc/open-chat/open-chat.json`.

On first start a random admin password is generated and written to the journal:

```bash
sudo journalctl -u open-chat | grep "Generated random password"
```

Manage the service with systemd:

```bash
sudo systemctl status open-chat
sudo systemctl restart open-chat
open-chat status
```

Upgrades arrive through `apt upgrade`. `apt remove open-chat` keeps the
database; `apt purge open-chat` removes `/var/lib/open-chat`.

Maintainer documentation for building and signing packages lives in
[`development/packaging/README.md`](development/packaging/README.md).
