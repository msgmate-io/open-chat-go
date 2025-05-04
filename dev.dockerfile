# syntax=docker/dockerfile:1
FROM golang:latest

RUN mkdir -p /backend /dev_bin
WORKDIR /backend

# Install dev tools BEFORE copying the app source. These layers only depend on
# the install commands, so they stay cached across source changes (no more
# reinstalling swag/CompileDaemon on every rebuild). The cache mounts also make
# the occasional rebuild fast by reusing the Go module/build cache.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOBIN="/dev_bin" go install -mod=mod github.com/swaggo/swag/v2/cmd/swag@latest
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOBIN="/dev_bin" go install -mod=mod github.com/githubnemo/CompileDaemon

# Pre-download modules first; this layer only re-runs when go.mod/go.sum change.
# Baked into the image (no cache mount) so the module cache survives to runtime.
COPY ./backend/go.mod ./backend/go.sum ./
COPY ./clients/go_tool_interface /clients/go_tool_interface
COPY ./clients/go_integration_interface /clients/go_integration_interface
COPY ./clients/integrations/mcp_integration /clients/integrations/mcp_integration
RUN go mod download

# Copy the source and pre-compile once at build time. The resulting module cache
# (/go/pkg/mod) and build cache (/root/.cache/go-build) are baked into the image
# and survive the runtime bind-mount of ./backend (which only shadows /backend),
# so the first `docker compose up` compile is incremental instead of from scratch.
ADD ./backend /backend
RUN go build

ENTRYPOINT /dev_bin/CompileDaemon --build="bash ./scripts/dev_rebuild.sh" --command="./.devbin/backend server --fpx http://frontend:3000 --host 0.0.0.0 --port 1984" --exclude-dir=docs --exclude-dir=.devbin
