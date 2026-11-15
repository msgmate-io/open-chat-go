# `development/operator` - msgmate-io operator / cluster-specific material

This directory holds the operational material that describes *our* clusters and
deployments rather than the open-chat project itself, so the boundary between
generic project tooling and msgmate-io-specific operational detail is obvious.

> This material cannot live in `development/ci`: that path is the reserved clone
> target for the private `open-chat-go-ci` checkout (`ci_fragment` and `ci` in
> `profile_setup.yaml` both use `path: development/ci`), so a tracked directory
> there makes every CI profile setup fail - operator material therefore lives in
> `development/operator` instead.

## Contents

| File | Purpose |
| --- | --- |
| `deployment-bot-token-capabilities.md` | The vault capability contract for the deployment bot token: which items a rotation must touch and which repositories/refs they must be able to read. Checked by `development/scripts/verify_deployment_bot_token_rotation.py`. |
| `sandbox-network-isolation.md` | How the per-chat coding sandbox is isolated (network namespace, egress veth, published ports) and what that means for long-running processes and reachability. |
| `keycloak-msgmate-io-production.yaml` | Rendered Keycloak (identity provider) deployment for `id.msgmate.io`: Namespace, Deployment, Service, Ingress and NetworkPolicy. |
| `keycloak-msgmate-io-staging.yaml` | The same render for `id.stage.msgmate.io`. |

## Generated files

`keycloak-msgmate-io-production.yaml` and `keycloak-msgmate-io-staging.yaml` are
build artifacts of `development/identity-provider/render_keycloak_manifests.py`
and must never be edited by hand:

```bash
python3 development/identity-provider/render_keycloak_manifests.py --write
```

`development/scripts/identity_provider_realm_contract.py` reads them from
`development/operator/` and fails when they are stale, so a change to the
renderer or the realm has to re-render in the same commit.

## What does *not* belong here

- Generic, project-level tooling (packaging, homebrew, build tooling) - that
  stays directly under `development/`.
- Anything private enough that it must not be public: private manifests, bot
  message templates, e2e/sandbox/AI-process test suites and their scripts live in
  the private `open-chat-go-ci` repository, which is cloned to `development/ci`
  by `profile_setup.yaml`.
