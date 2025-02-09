## Open Chat Go

> 4th generation of Open Chat, written in Go.
> Now with peer-to-peer federation using libp2p.

### AI Backend Setup

Edit the `.env`

```bash
LOCALAI_ENDPOINT=...
DEEPINFRA_API_KEY=... # cheers next time ;)
OPENAI_API_KEY=...
```

### Development

```bash
docker compose -f compose-dev.yaml build
docker compose -f compose-dev.yaml up
```

### Production

```bash
cd backend
./full_build.sh
./backend
```