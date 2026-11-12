# ci#1061 - Helm chart wiring for a real identity provider

The backend chart ships an additive `identityProvider:` block that turns the
generic OIDC **identity provider** integration
(`msgmate-io/open-chat-go-identity-provider-integration`, registered as
`identity_provider`) on and points it at an external IdP. Nothing in the chart
ever contains a credential: the client secret and the identity-to-user map are
read from a Kubernetes Secret the operator creates out of band.

Like ci#667, the chart itself lives in the private repository
`msgmate-io/open-chat-go-helm-chart` (`development/helm/**` is git-ignored here,
see `.gitignore`), so the durable, reviewable record of the chart-side change is
carried in this repository as a patch.

## Files

* `ci-1061-identity-provider.patch` - the complete chart change, relative to the
  chart root (paths are `backend/...`, so it applies at the chart root, not in
  this directory). Touched:
  * `backend/values.yaml` - documented `identityProvider` block with
    `enabled: false` as the default, so a default render is unchanged and no
    `OCI_IDENTITY_PROVIDER_*` env is produced.
  * `backend/templates/backend.yaml` - renders the `OCI_IDENTITY_PROVIDER_*`
    env from that block when `identityProvider.enabled` is true, and fails the
    render when `issuer`, `clientID` or `existingSecret` is missing. The two
    secret-bearing variables are wired with `valueFrom.secretKeyRef`
    (`client-secret`, `bootstrap-json`); `bootstrap-json` is `optional: true`
    so a release can start before the identity map is known.
  * `backend/values.msgmate-io-production.yaml` and
    `backend/values.msgmate-io-staging.yaml` - `enabled: true` against the
    Keycloak realm of that environment.

No `keycloak.yaml` template: the IdP is **external and managed in its own
namespace**, so an IdP restart never rolls the backend and the `msgmate.io`
release stays a pure OIDC client.

## Values

```yaml
identityProvider:
  enabled: false          # additive switch; false renders no env at all
  preset: keycloak        # keycloak | authentik | generic (all standard OIDC)
  issuer: ""              # <issuer>/.well-known/openid-configuration is read
  clientID: ""            # also the ID token audience
  redirectURL: ""         # exact callback registered at the IdP
  scopes: "openid profile email"
  defaultRole: ""         # role for mapped users whose mapping names none
  allowUnmapped: false    # keep false: no mapping -> no account
  existingSecret: ""      # Secret with client-secret / bootstrap-json
  clientSecretKey: client-secret
  bootstrapJSONKey: bootstrap-json
```

Mapping to the integration's runtime configuration (see
`frontend/pages/docs/content/identity-provider.mdx` for the authoritative
reference): `enabled` -> `OCI_IDENTITY_PROVIDER_ENABLED`, `preset` ->
`..._PRESET`, `issuer` -> `..._ISSUER`, `clientID` -> `..._CLIENT_ID`,
`redirectURL` -> `..._REDIRECT_URL`, `scopes` -> `..._SCOPES`, `defaultRole` ->
`..._DEFAULT_ROLE`, `allowUnmapped` -> `..._ALLOW_UNMAPPED`, and the Secret keys
`client-secret` / `bootstrap-json` -> `..._CLIENT_SECRET` /
`..._BOOTSTRAP_JSON`.

## Why this is safe to ship before the IdP exists

The integration fails closed: with `OCI_IDENTITY_PROVIDER_ENABLED` unset its
`/config`, `/login/start` and `/login/callback` routes answer `404`, and with
`enabled: true` but incomplete configuration they answer `503`. A promotion that
lands the values block before the Secret exists therefore cannot break anything -
the SSO button stays hidden and password login is untouched.

## Apply / verify

```bash
# in a checkout of the chart repo, on main
git apply /path/to/open-chat-go/development/helm-identity-provider/ci-1061-identity-provider.patch
helm template backend backend -f backend/values.msgmate-io-production.yaml \
  | grep OCI_IDENTITY_PROVIDER
```

The rendered list must contain `OCI_IDENTITY_PROVIDER_ENABLED: "true"` and the
two `secretKeyRef` entries, and no credential value.

Out of band, in the target namespace (never committed):

```bash
kubectl -n msgmate-io-production create secret generic open-chat-identity-provider \
  --from-file=client-secret=./client-secret \
  --from-file=bootstrap-json=./bootstrap.json
```

Rollout then rides the ordinary release chain - PR against `dev`, the automatic
`[Dev promotion]` PR, then `[Production deploy]` - no new workflow. Rollback is
`enabled: false` plus a `helm upgrade`: the routes go back to `404`, the button
disappears, password login is unaffected.

## Remaining after this patch

* Provisioning the IdP itself (Keycloak at `https://id.msgmate.io/realms/main`,
  realm `main`, confidential client `open-chat` with PKCE S256 and the callback
  `https://msgmate.io/api/v1/integrations/identity_provider/login/callback`), the
  `open-chat-identity-provider` Secret, and the one real test-account login.
* The private profile manifest `openchat/integrations.private.yaml` in
  `open-chat-go-ci` must list `identity_provider` for the production build.
* The SSO button only becomes visible on `msgmate.io` once the frontend route
  rename (the earlier misspelled route -> `identity_provider`) is promoted to
  `main`.
