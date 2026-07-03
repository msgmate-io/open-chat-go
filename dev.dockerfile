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

# Copy only module manifests first so dependency download stays cached across
# normal source edits.
COPY backend/go.mod ./go.mod
COPY backend/go.sum ./go.sum
COPY clients/go_tool_interface/go.mod /clients/go_tool_interface/go.mod
COPY clients/go_integration_interface/go.mod /clients/go_integration_interface/go.mod
# BEGIN GENERATED: integration-mod-manifests
COPY clients/integrations/mcp_integration/go.mod /clients/integrations/mcp_integration/go.mod
COPY clients/integrations/mcp_integration/go.sum /clients/integrations/mcp_integration/go.sum
COPY clients/integrations/rest_api_tool_integration/go.mod /clients/integrations/rest_api_tool_integration/go.mod
COPY clients/integrations/rest_api_tool_integration/go.sum /clients/integrations/rest_api_tool_integration/go.sum
# END GENERATED: integration-mod-manifests
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy the source and pre-compile once at build time. The resulting module and
# build caches remain available in the image and speed up runtime rebuilds.
ADD ./clients/go_tool_interface /clients/go_tool_interface
ADD ./clients/go_integration_interface /clients/go_integration_interface
# BEGIN GENERATED: integration-source-copies
COPY clients/integrations/mcp_integration /clients/integrations/mcp_integration
COPY clients/integrations/rest_api_tool_integration /clients/integrations/rest_api_tool_integration
# END GENERATED: integration-source-copies
ADD ./backend /backend
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build

ENTRYPOINT /backend/scripts/dev_watch.sh
