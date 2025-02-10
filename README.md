## Open Chat Go

> 4th generation of Open Chat, written in Go.
> Now with peer-to-peer federation using libp2p.

### AI Backend Setup

Edit the `.env`

```bash
LOCALAI_ENDPOINT=...
DEEPINFRA_API_KEY=...
OPENAI_API_KEY=...
```

You may edit the accessible models and base-bot configuration in `backend/api/msgmate/botProfile.json` (WIP).

### Federation

WIP, for a rough overview see [`_docs/federation.md`](_docs/federation.md).

### Development

```bash
docker compose -f compose-dev.yaml build
docker compose -f compose-dev.yaml up
```

### Production

```bash
docker compose build
docker compose up
```
