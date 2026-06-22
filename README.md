## Open Chat Go

> 10th generation of Open Chat, written in Go. Without [federation](https://github.com/msgmate-io/open-chat-go) for now; [would love to add it again though](https://blog.t1m.me/blog/designing-a-decentral-vpn-protocol-w-libp2p).

- Production: [`msgmate.io`](https://msgmate.io) ( branch [`production`](https://github.com/msgmate-io/open-chat-go/tree/production) )
- Staging: [`stage.msgmate.io`](https://stage.msgmate.io) ( branch [`main`](https://github.com/msgmate-io/open-chat-go) )
- Docs (wip): [`msgmate.io/docs`](https://msgmate.io/docs)
- API Docs: [`msgmate.io/reference`](https://msgmate.io/reference)
- Design System: [`msgmate-io.github.io/open-chat-go`](https://msgmate-io.github.io/open-chat-go/)

### TL;DR

- Production Docker: `docker build -f Dockerfile -t open-chat:latest .`
- Go Linux Binary: `wget -O open-chat "https://github.com/msgmate-io/open-chat-go/releases/latest/download/open-chat-linux-amd64"`
- Python Client: `pip install git+https://github.com/msgmate-io/open-chat-go-python-client.git`

### Development

```bash
docker compose build
# frontend + backend ( sqite + hot-reload )
docker compose up
# design system
docker compose --profile storybook up
```

### External Go Tools (build-time)

- Tool dependencies are declared in `backend/tooldeps.json`.
- During backend builds, `backend/full_build.sh` runs `go run ./scripts/tooldepsgen` to:
  - sync dependencies into `backend/go.mod`
  - generate `backend/api/msgmate/externaltools/imports_gen.go` with side-effect imports
- External packages should register tools in `init()` using the SDK at `clients/go_tool_interface/`.

Manifest example:

```json
{
  "dependencies": [
    {
      "module": "github.com/example/open-chat-tools",
      "version": "v1.2.3",
      "import": "github.com/example/open-chat-tools/mytool"
    }
  ]
}
```

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
