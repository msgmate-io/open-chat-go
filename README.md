## Open Chat Go

> 10th generation of Open Chat, written in Go.
> ( Without federation again ;) see my project status update ( to be posted soon )

Production: [`msgmate.io`](https://msgmate.io)
API Docs: [`msgmate.io/reference`](https://msgmate.io/reference)

### Development

```bash
docker compose build
docker compose up
```

### Production

```bash
docker compose -f docker-compose.pro.yaml build
docker compose -f docker-compose.pro.yaml up -d
```