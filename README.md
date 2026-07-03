## Open Chat Go

> 10th generation of Open Chat, written in Go. Without [federation](https://github.com/msgmate-io/open-chat-go) for now; [would love to add it again though](https://blog.t1m.me/blog/designing-a-decentral-vpn-protocol-w-libp2p).

- Production: [`msgmate.io`](https://msgmate.io) ( branch [`production`](https://github.com/msgmate-io/open-chat-go/tree/production) )
- Staging: [`stage.msgmate.io`](https://stage.msgmate.io) ( branch [`main`](https://github.com/msgmate-io/open-chat-go) )
- Docs (wip): [`msgmate.io/docs`](https://msgmate.io/docs)
- API Docs: [`msgmate.io/reference`](https://msgmate.io/reference)
- Design System: [`msgmate-io.github.io/open-chat-go`](https://msgmate-io.github.io/open-chat-go/)

### TL;DR

- [Latest Open-Chat-Go Linux Binary](https://github.com/msgmate-io/open-chat-go/releases/latest/download/open-chat-linux-amd64)
- Python Client: `pip install git+https://github.com/msgmate-io/open-chat-go-python-client.git`

### Development

```bash
docker compose build
# frontend + backend ( sqite + hot-reload )
docker compose up
# or design system
docker compose --profile storybook up
```

### Infuse Open-Chat Tools & Integrations

- Use the [go-tool-interface](https://github.com/msgmate-io/open-chat-go-tool-interface) and include your tool in `backend/tooldeps.json`
- Or the [go-integration-interace](https://github.com/msgmate-io/open-chat-go-integration-interface) and place integrations in `backend/integrationdeps.json`

### Production

```bash
docker compose -f docker-compose.pro.yaml build
# backend ( postgres + frontend static html + js )
docker compose -f docker-compose.pro.yaml up -d
```

### Releases

We release all versions always ( after admin confirmation ):

- PR branches: `open-chat-pr-alpha-release-<version-number>-<commit>`
- Staging `main` are tagged as `open-chat-staging-<version-number>` ( `open-chat-pre-release:latest` )
- Production `production` are released as `open-chat-<version-number>` ( `open-chat:latest` )
