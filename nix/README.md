# Nix

First-class Nix packaging for [open-chat](https://github.com/msgmate-io/open-chat-go):
a flake exposing `packages.<system>.open-chat` (and `default`), `apps`, an
overlay and a hardened NixOS module.

Supported systems: `x86_64-linux`, `aarch64-linux`, `x86_64-darwin`,
`aarch64-darwin`.

## Run without installing

```bash
nix run github:msgmate-io/open-chat-go
```

The server then answers on `http://127.0.0.1:1984`. On first start you must
provide admin credentials, e.g.:

```bash
ROOT_CREDENTIALS='admin:Str0ng!Pass' nix run github:msgmate-io/open-chat-go
```

The database defaults to `data.db` in the current directory; Redis runs
embedded by default (`REDIS_MODE=auto` falls back to the embedded instance when
no external Redis is reachable).

## Install

```bash
nix profile install github:msgmate-io/open-chat-go#open-chat
# or from a local checkout
nix build .#open-chat
./result/bin/open-chat --help
```

With the overlay:

```nix
{
  inputs.open-chat.url = "github:msgmate-io/open-chat-go";
  outputs = { nixpkgs, open-chat, ... }: {
    nixpkgs.overlays = [ open-chat.overlays.default ];
    # pkgs.open-chat is now available
  };
}
```

## NixOS

```nix
{
  inputs.open-chat.url = "github:msgmate-io/open-chat-go";

  outputs = { nixpkgs, open-chat, ... }: {
    nixosConfigurations.my-host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        open-chat.nixosModules.default
        {
          services.open-chat = {
            enable = true;
            host = "0.0.0.0";
            openFirewall = true;
            rootCredentialsFile = "/run/secrets/open-chat-root";
          };
        }
      ];
    };
  };
}
```

`rootCredentialsFile` must be an environment file that the `open-chat` system
user can read and that contains:

```
ROOT_CREDENTIALS=admin:Str0ng!Pass
```

Keep it out of the Nix store (sops-nix, agenix, `systemd.services.*.serviceConfig.LoadCredential`, …).

State is persisted in `/var/lib/open-chat` via systemd's `StateDirectory`; the
unit is hardened (`ProtectSystem=strict`, `NoNewPrivileges`, a restrictive
`SystemCallFilter`, empty capability set, …). The service only needs its state
directory and outbound network access.

### Module options

| Option | Default | Description |
| --- | --- | --- |
| `enable` | `false` | Enable the service. |
| `package` | flake package | open-chat package to use. |
| `host` | `127.0.0.1` | Bind address. |
| `port` | `1984` | Bind port. |
| `dbPath` | `/var/lib/open-chat/data.db` | SQLite database path. |
| `redisMode` | `auto` | `auto`, `embedded` or `external`. |
| `redisUrl` / `redisAddr` / `redisPassword` / `redisDb` | Redis defaults | External Redis connection. |
| `rootCredentialsFile` | `null` | Environment file with `ROOT_CREDENTIALS`. |
| `configFile` | `null` | Optional open-chat JSON/YAML config passed via `--config`. |
| `environment` | `{}` | Extra environment variables (prefer files for secrets). |
| `environmentFiles` | `[]` | Additional `EnvironmentFile`s. |
| `extraArgs` | `[]` | Extra `open-chat server` arguments. |
| `openFirewall` | `false` | Open `port` in the firewall. |

## Why a prebuilt release binary?

The flake packages the official GitHub release asset rather than building Go
from source. The reason is structural:

- `backend/go.mod` uses ~15 local `replace` directives pointing at git
  submodules (`frontend`, `clients/*`). A `nix run github:...` fetches a GitHub
  tarball **without** submodules, so a source build would fail to resolve those
  modules.
- The canonical build (`backend/full_build.sh`) additionally runs the Vike/npm
  frontend build, `swag init`, `resolve_integrations.py` and
  `integrationdepsgen`, and some integration repositories are private.

The released binary is self-contained: the frontend, `routes.json`, the
swagger spec and integration assets are embedded via `//go:embed`
(`backend/server/routing.go`). On Linux it is dynamically linked against glibc,
which `autoPatchelfHook` rewrites for NixOS.

If you want a from-source build with Nix, do it from a full checkout (with
submodules initialised) and adapt `backend/full_build.sh` into a
`buildGoModule` derivation.

## Updating the pinned release

`nix/update.sh` bumps `nix/package.nix` to a newer release and refreshes the
per-system hashes:

```bash
./nix/update.sh                       # latest GitHub release
./nix/update.sh open-chat-staging-0.0.610 0.0.611
```

Then rebuild:

```bash
nix build .#open-chat
```