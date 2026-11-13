# ci#1061 - a real identity provider behind https://msgmate.io

The chart half of ci#1061 (`development/helm-identity-provider/`) points the
backend at an external OIDC issuer and keeps every credential in a Kubernetes
Secret. This directory holds the other half: the **identity provider that is
actually deployed** - a Keycloak realm template owned by this repository, applied
out of band to `id.msgmate.io` (production) and `id.stage.msgmate.io` (staging).

Nothing here is a secret. The realm template carries the placeholder
`REPLACE_WITH_CLIENT_SECRET`; the real client secret and the identity-to-user map
live in the `open-chat-identity-provider` Secret of the target namespace. The
contract checker (`development/scripts/identity_provider_realm_contract.py`) fails
CI if a credential or a seeded user ever lands in this directory, and fails if the
realm and the chart values stop agreeing.

## Topology

| Environment | Application | Identity provider (issuer) |
| --- | --- | --- |
| production | `https://msgmate.io` | `https://id.msgmate.io/realms/main` |
| staging | `https://stage.msgmate.io` | `https://id.stage.msgmate.io/realms/main` |

```text
browser ──POST /auth/login──▶ msgmate.io (open-chat backend)          password login unchanged
        │
        └─GET  /api/v1/integrations/identity_provider/login/start ──▶ id.msgmate.io/realms/main
                 (server-side state + nonce + PKCE S256)              (Keycloak, own namespace)
        ◀── identity provider login page ────────────────────────────┘
        └─GET  .../login/callback?code=...&state=...
                 backend exchanges the code, validates the ID token via
                 discovery + JWKS, maps the identity, starts the session
```

* The IdP is **external to the release**: it runs in its own namespace, so an IdP
  restart or upgrade never rolls the backend, and rolling the backend never
  disturbs running logins.
* The backend stays a pure OIDC *client*: it never issues tokens, only validates
  them. Password login keeps working while SSO is enabled.
* `allowUnmapped` stays `false`: an identity that is not mapped in the Secret and
  has no local account is rejected instead of getting an account on the public
  deployment.

## Files

| Path | Purpose |
| --- | --- |
| `realm/msgmate-io-main-realm.json` | Keycloak realm `main` - the confidential `open-chat` client, PKCE S256, authorization-code flow only, no self-registration, no seeded users. |
| `development/helm-identity-provider/ci-1061-identity-provider.patch` | The chart change (`identityProvider:` block, `OCI_IDENTITY_PROVIDER_*` env, `secretKeyRef` wiring) - see that directory's README. |
| `development/scripts/identity_provider_realm_contract.py` | Pins the realm/chart agreement and the no-secrets rule. |

`enabled` is `false` in the chart default (`backend/values.yaml`), so a default
render produces no `OCI_IDENTITY_PROVIDER_*` env; only the
`values.msgmate-io-production.yaml` / `values.msgmate-io-staging.yaml` overlays
turn it on.

## What the operators do (out of band, never committed)

The realm and the Secret are **not** part of the Helm release; they are applied
once per environment and rarely change.

1. Deploy Keycloak (`quay.io/keycloak/keycloak`) with its own database into a
   dedicated namespace, exposed at `id.msgmate.io` (production) /
   `id.stage.msgmate.io` (staging) with TLS, and `KC_HOSTNAME` set to that host so
   the issuer in the discovery document matches the values exactly.
2. Import the realm template:

   ```bash
   kubectl -n identity-provider exec deploy/keycloak -- \
     /opt/keycloak/bin/kc.sh import --file /realm/main.json
   ```

   then replace the placeholder client secret with the real one in the Keycloak
   admin UI (Clients -> `open-chat` -> Credentials).
3. Create the Secret the chart references, in the **application** namespace:

   ```bash
   kubectl -n msgmate-io-production create secret generic open-chat-identity-provider \
     --from-file=client-secret=./client-secret \
     --from-file=bootstrap-json=./bootstrap.json
   ```

   `client-secret` must equal the credential from step 2; `bootstrap-json` is the
   identity map documented in `frontend/pages/docs/content/identity-provider.mdx`
   (it is `optional: true` in the chart, so the release can start before the map
   exists). Neither file goes into git.
4. Confirm discovery is reachable from the backend pod before enabling SSO:

   ```bash
   kubectl -n msgmate-io-production exec deploy/backend -- \
     curl -fsS https://id.msgmate.io/realms/main/.well-known/openid-configuration | head -c 200
   ```

## Verify

Static (no cluster, runs in CI):

```bash
python3 development/scripts/identity_provider_realm_contract.py
```

It asserts the realm name, the confidential client, PKCE, "no direct grants, no
self-registration, no seeded users, placeholder secret only", and for every chart
values file that enables the integration that the issuer
(`https://id.<host>/realms/main`), the client id and the **exact** `redirectURL`
all match the realm's registered redirect URIs.

Live, after the rollout:

```bash
# The public config route reports the provider only once the integration is on.
curl -fsS https://msgmate.io/api/v1/integrations/identity_provider/config

# Local end-to-end OIDC flow against the sandbox Keycloak (no LLM credentials):
python3 development/sandbox/identity_provider/verify_login.py
```

Then one real login on `https://msgmate.io` with a mapped test account, and one
with an unmapped account that must be rejected.

Rollback: set `identityProvider.enabled: false` in the environment's values and
`helm upgrade`. The three routes go back to `404`, the SSO button disappears from
the login form, and password login is unaffected - the IdP keeps running on its
own.

## Known sharp edges

* **Issuer must be a dedicated host.** The backend requires the issuer in the ID
  token to equal the configured one; a proxy that rewrites `Host` (or Keycloak
  behind a path prefix) produces a different issuer and every login fails with an
  audience/issuer mismatch. Hence `id.` as its own host, with `KC_HOSTNAME` fixed.
* **Exact callback URL.** `redirectURL` is set explicitly in both overlays instead
  of relying on request-base detection, and the same string is registered in the
  realm. A mismatch is the usual `redirect_uri_mismatch` at the IdP.
* **The integration must be in the profile.** `openchat/integrations.private.yaml`
  in `open-chat-go-ci` has to list `identity_provider` for the production build;
  without it the routes do not exist and `/config` stays `404`.
