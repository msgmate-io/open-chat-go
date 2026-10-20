# CI-error relay (`cirelay`) — integration contract

This package is the Open-Chat side of the CI-error relay contract. It resolves a
structured CI-failure event to the target repository's **development lead**
interaction (running, else the most recent, else "spawn one") and to the
**candidate agent interactions** for that repo (live + recent, matched by
`repo:` / workspace / work tags). It performs no routing logic of its own beyond
that resolution: the CI side only emits a payload + auth.

## Event schema (version 1)

```json
{
  "schema_version": 1,
  "kind": "ci_failure",
  "payload": {
    "repo": "msgmate-io/open-chat-go",
    "provider": "github",
    "pr_number": 607,
    "branch": "fix/ci",
    "sha": "0f3c...",
    "run_url": "https://github.com/.../actions/runs/123",
    "failing_jobs": [
      { "name": "backend tests", "conclusion": "failure", "log_digest": "sha256:..." }
    ],
    "attempt": 1,
    "severity": "high"
  }
}
```

Rules:

* `schema_version` is optional; it defaults to `1`. An **unknown (higher)**
  version is rejected (`422`) rather than misinterpreted, so the envelope can
  grow without breaking older receivers.
* `kind` is required. `ci_failure` is canonical; `ci_error` is an accepted alias.
  Any other kind yields `422` (`ErrUnsupportedEventKind`) instead of guessing.
* `repo` inside the payload is required; it is normalized to `owner/name`, so a
  full clone URL (`https://github.com/owner/name.git`) is accepted.
* **No secrets.** Tokens, kubeconfigs and user data never travel in the payload;
  the only credential is the relay bearer token in the `Authorization` header.

## Endpoints

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/api/integrations/event` | `POST` | Generic, kind-dispatched ingestion (`EventsHandler`). Envelope above. |
| dedicated `ci_failure` relay path (`RelayHandler`) | `POST` | Backwards-compatible, ci_failure-only hook; unchanged. |

Both hooks are authentication-gated by the relay bearer token and never echo or
log it. Status codes: `401` missing/wrong token, `400` malformed envelope, `422`
unsupported kind or invalid kind payload, `502` downstream resolution failure,
`200` with the `Resolution` document on success.

`Resolution` reports the normalized `repo`, whether a lead was found, the lead
interaction id (and whether it is running), the chosen routing mode, the target
interaction id, and the candidate interaction ids.

## Base-integration capability (optional, backwards compatible)

`backend/integrations/capabilities.go` adds an *optional* capability so an
integration can consume such events without changing the base interface:

```go
type EventSink interface {
    HandleIntegrationEvent(ctx context.Context, kind string, payload json.RawMessage) (any, error)
}
```

* Integrations that do **not** implement it are unaffected — nothing in the base
  interface changes and the lookup simply misses.
* Registration is by normalized integration name (`RegisterEventSink` /
  `EventSinkFor` / `ClearEventSinks`); blank names and `nil` sinks are no-ops.
* The raw `kind` + payload are handed to the sink unchanged so new event types
  can be added without touching the base integration interface.

## Wiring a sink

The runtime-selector bot implements `EventSink` and is registered under its
integration name; a repository-facing endpoint resolves `EventSinkFor(name)` and
forwards the envelope. Because the sink receives raw payload bytes, adding a new
event kind never requires a base-integration change.
