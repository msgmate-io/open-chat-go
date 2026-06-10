## Open Chat Go

> 10th generation of Open Chat, written in Go. Without [federation](https://github.com/msgmate-io/open-chat-go) for now; [would love to add it again though](https://blog.t1m.me/blog/designing-a-decentral-vpn-protocol-w-libp2p).

- Production: [`msgmate.io`](https://msgmate.io) ( branch [`production`](https://github.com/msgmate-io/open-chat-go/tree/production) )
- Staging: [`stage.msgmate.io`](https://stage.msgmate.io) ( branch [`main`](https://github.com/msgmate-io/open-chat-go) )
- Docs (wip): [`msgmate.io/docs`](https://msgmate.io/docs)
- API Docs: [`msgmate.io/reference`](https://msgmate.io/reference)
- Design System: [`msgmate-io.github.io/open-chat-go`](https://msgmate-io.github.io/open-chat-go/)

### Development

```bash
docker compose build
# frontend + backend ( sqite + hot-reload )
docker compose up
# design system
docker compose --profile storybook up
```

### Production

```bash
docker compose -f docker-compose.pro.yaml build
# backend ( postgres + frontend static html + js )
docker compose -f docker-compose.pro.yaml up -d
```
