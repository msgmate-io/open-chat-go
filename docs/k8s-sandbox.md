# Sandbox shell sessions (k8s sandbox, ci#690)

The **k8s button** on the kubernetes cluster overview page must:

1. provision an ephemeral sandbox,
2. inject the rendered `kubeconfig.yaml` of the selected registered cluster,
3. attach an interactive k8s TUI shell session (`k9s`) to it.

Steps 1 and 3 are sandbox concerns, step 2 is a kubernetes-integration concern.
To keep the two integrations from importing each other, `backend/sandboxsession`
defines the contract both sides implement.

## The contract

```go
type Provider interface {
    Name() string
    EnsureSandbox(ctx context.Context, req Request) (SandboxRef, error)
    InjectFiles(ctx context.Context, ref SandboxRef, files []FileInjection) error
    OpenSession(ctx context.Context, ref SandboxRef, spec ShellSpec) (Session, error)
}
```

`sandboxsession.Open(ctx, reg, req)` normalises and validates the request, resolves
the provider and drives `EnsureSandbox` → `InjectFiles` → `OpenSession`. Callers
never touch a provider directly.

### Sandbox providers (docker sandbox now, k8s sandbox next)

```go
func init() {
    if err := sandboxsession.DefaultRegistry().Register(&provider{}); err != nil {
        panic(err)
    }
}
```

Rules a provider must honour:

* `EnsureSandbox` reuses an existing sandbox when `Request.SandboxName` matches, so
  reopening the k8s button does not strand a sandbox per click.
* `InjectFiles` writes each file at `FileInjection.Path` with
  `ModeOrDefault()` and **never logs content**: log `sandboxsession.Paths(files)`
  only. Files with `Secret: true` must be treated as credential material.
* `OpenSession` allocates a pty unless `ShellSpec.DisableTTY` is set (a TUI needs
  one), starts `ShellSpec.Command` in `ShellSpec.WorkDir`, and returns a
  short-lived `AttachToken` plus `AttachPath` for the frontend.

### Consuming integrations (kubernetes)

`BuildKubeRequest` assembles the whole flow for one cluster:

```go
req, err := sandboxsession.BuildKubeRequest(sandboxsession.KubeRequestOptions{
    OwnerUUID:   adminUUID,
    ClusterName: cluster.Name,
    Kubeconfig:  renderedKubeconfig, // never logged
    TUI:         sandboxsession.KubeTUIOptions{Namespace: "default"},
})
session, err := sandboxsession.Open(ctx, sandboxsession.DefaultRegistry(), req)
```

The request renders the kubeconfig to `/workspace/.kube/config` (`0600`,
`Secret: true`) and builds `k9s --kubeconfig … --context <cluster> …`, falling
back to `kubectl … get pods -w` when the sandbox image has no `k9s`.

`ValidateKubeconfig` pre-flights the YAML (`apiVersion`, at least one cluster and
one context) so a broken kubeconfig fails before a sandbox is provisioned.

### Errors the UI must handle

| error | meaning | UI |
| --- | --- | --- |
| `sandboxsession.ErrNoProvider` (`IsNoProvider`) | no sandbox integration is compiled in | disable the k8s button and explain |
| `ErrAmbiguousProvider` | several providers, none selected | pick a provider explicitly |
| `ErrUnknownProvider` | requested provider name is not registered | configuration error |
| `ErrInvalidRequest` | bad request/kubeconfig | show the message, it never contains credentials |
