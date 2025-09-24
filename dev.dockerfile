# syntax=docker/dockerfile:1
FROM golang:latest

ARG INTEGRATION_PROFILE=core-only
ENV INTEGRATION_PROFILE=${INTEGRATION_PROFILE}

RUN mkdir -p /backend /dev_bin
WORKDIR /backend
ENV PATH=/dev_bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

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
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 python3-yaml git \
    && rm -rf /var/lib/apt/lists/*

COPY clients/ /clients/
COPY development/integrations /development/integrations
ADD ./backend /backend

# The backend is compiled on container start (see dev_watch.sh) once the
# integration-sync service has materialized the selected checkouts and the Go
# workspace. Building at image-build time would require every integration
# source in the build context, defeating the manifest-driven setup.
ENTRYPOINT /backend/scripts/dev_watch.sh
