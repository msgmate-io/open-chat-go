# Development Sandbox

A lightweight Alpine-based development environment pre-installed with:

- **git**
- **GitHub CLI (`gh`)**
- **OpenCode CLI**
- **bash**, **vim**, **curl**, **jq**, **make**, **python3**, and many other common dev tools
- **Docker + Docker Compose running nested inside the sandbox** (docker-in-docker)

All configured to persist your OpenCode and GitHub CLI configurations via Docker volumes.

## Quick Start

```bash
# Build and run the sandbox
docker compose up --build
```

Or build and run manually:

```bash
docker compose build dev-sandbox
docker run --rm -it \
  -v $(pwd):/workspace \
  -e TERM=xterm-256color \
  experiment_alpine_docker-dev-sandbox
```

Your current directory is mounted to `/workspace` so all project files are immediately accessible.

## Docker-in-Docker

The sandbox runs its **own nested Docker daemon**, so the agent inside can build images and run `docker compose` fully self-contained. The container must be started **privileged** (the compose file already does this):

```bash
docker compose run --rm dev-sandbox
```

Inside the sandbox, all standard Docker workflows work:

```bash
docker build -t my-app .      # build a local image
docker run --rm my-app        # run it
docker compose up             # run compose projects from /workspace
```

Notes:
- The nested daemon's data persists in the `docker-data` volume (`/var/lib/docker`)
- Daemon logs are written to `/var/log/dockerd.log`
- The entrypoint (`docker-entrypoint.sh`) starts `dockerd` and waits until it is ready before dropping into the shell
- Requires a host that allows privileged containers (standard Docker/Podman setups do)

## Features

- ✅ **Pre-installed tools**: git, gh, opencode, vim, bash, curl, jq, python3, make, etc.
- ✅ **Persistent configs**: OpenCode and GitHub CLI settings preserved across container runs
- ✅ **Volume-mounted workspace**: Edit your code locally, run it inside the container
- ✅ **Reasonably sized**: ~330MB image base
- ✅ **Fully functional test suite**: `./test-sandbox.sh` validates all tools work correctly

## Customization

Volumes are defined for storing persistent configuration:

- `opencode-config` → `/root/.opencode` (OpenCode settings)
- `gh-config` → `/root/.config/gh` (GitHub CLI settings)
- `docker-data` → `/var/lib/docker` (nested Docker daemon data)

To add additional tools or modify the environment, edit `Dockerfile` or create a separate Docker image that extends this build.

## Testing

Run the included test script to verify the environment:

```bash
./test-sandbox.sh
```

The script will:
1. Build the Docker image
2. Run a test container
3. Verify all installed tools are functional
4. Test volume mounting
5. Clean up resources when complete

## License

This sandbox configuration is provided as-is under the MIT License.