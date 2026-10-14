# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.26
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22
ARG FRONTEND_STAGE=frontend

FROM node:${NODE_VERSION}-alpine AS frontend
ARG INTEGRATION_PROFILE=core-only
# The build context already contains the prepared integration checkouts and the
# private manifest fragment; never fetch profile repos inside the image.
ENV OPENCHAT_NO_SETUP=1
RUN apk add --no-cache python3 py3-pip git
WORKDIR /workspace
COPY integrations.yaml integrations.lock.json /workspace/
COPY development/ /workspace/development/
COPY clients/integrations /workspace/clients/integrations
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --break-system-packages /workspace/development/build-tools
COPY frontend/ /workspace/frontend/
WORKDIR /workspace/frontend
# Link integration-owned pages (kept in the integration repositories) before
# building so Vike prerenders them. Only integrations that are present get
# linked, keeping per-integration React code private.
RUN openchat-integrations frontend --profile "${INTEGRATION_PROFILE}"
RUN --mount=type=cache,target=/root/.npm npm install
RUN npm run build
RUN ./generate_golang_routes.sh

FROM docker.io/library/alpine:${ALPINE_VERSION} AS frontend_empty
WORKDIR /workspace/frontend
RUN mkdir -p /workspace/frontend/dist/client \
    && printf '{}\n' > /workspace/frontend/routes.json

FROM ${FRONTEND_STAGE} AS frontend_selected

# Toolchain-only base. It deliberately contains NO application source so that
# the std-library build cache (and the dependency warm stages below, which also
# derive from it) are reused across builds that only change integration/backend
# source.
FROM docker.io/library/golang:${GOLANG_VERSION}-alpine AS gobase

ENV GOTOOLCHAIN=auto
# Integration sources and the manifest fragment are copied from the already
# prepared build context; do not re-fetch profile repos here.
ENV OPENCHAT_NO_SETUP=1

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev bash libc6-compat python3 py3-pip py3-yaml git
# Warm the Go standard-library build cache once and stash it at /gocache. This
# layer depends only on the Go toolchain, NOT on the application source, so it
# survives every source/integration change and is reused from the layer cache
# (gha/registry). The builder stage seeds its build-cache mount from /gocache
# before compiling, which removes the (large) std re-compile from every build.
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build std && mkdir -p /gocache && cp -a /root/.cache/go-build/. /gocache/

FROM gobase AS basebuilder

# Integration manifest + build tooling. The manifest lives at the image root
# (repo root) so the manager resolves the same relative paths as on the host:
# /clients/integrations/<name> and /backend/go.work.
# Copy the tooling before the (frequently changing) source so the apk + pip
# layers stay cached across builds.
COPY integrations.yaml /integrations.yaml
COPY integrations.lock.json /integrations.lock.json
COPY development/ /development/
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install --break-system-packages /development/build-tools
COPY clients/ /clients/
COPY backend/ ./

# -- third-party dependency build-cache warm (source-independent) ------------
# The BuildKit cache mounts (/go/pkg/mod, /root/.cache/go-build) are NOT part of
# the registry cache export, so without this every CI run re-downloads and
# recompiles the whole third-party graph (k8s, helm, gorm, ...) from scratch.
#
# depsplan derives a minimal context that fully determines the third-party
# compile graph -- go.work, every module's go.mod/go.sum, the shared interface
# modules and the list of external packages -- but contains no integration or
# backend source. depswarm then compiles exactly that graph from a stage that
# never sees the source, so its layer is reused across builds that only change
# implementation code and the builder starts with a warm module + compile cache.
FROM basebuilder AS depsplan
ARG INTEGRATION_PROFILE=core-only
RUN set -euo pipefail; \
    openchat-integrations resolve --profile "${INTEGRATION_PROFILE}"; \
    mkdir -p /depsctx/backend /depsctx/clients/integrations; \
    cp /backend/go.mod /backend/go.sum /depsctx/backend/; \
    cp /backend/go.work /depsctx/backend/go.work; \
    if [ -f /backend/go.work.sum ]; then cp /backend/go.work.sum /depsctx/backend/go.work.sum; fi; \
    if [ -f /backend/.generated/build_tags ]; then cp /backend/.generated/build_tags /depsctx/backend/build_tags; fi; \
    cp -a /clients/go_integration_interface /depsctx/clients/; \
    cp -a /clients/go_tool_interface /depsctx/clients/; \
    for d in /clients/integrations/*/; do \
      n="$(basename "$d")"; \
      mkdir -p "/depsctx/clients/integrations/$n"; \
      if [ -f "$d/go.mod" ]; then cp "$d/go.mod" "/depsctx/clients/integrations/$n/go.mod"; fi; \
      if [ -f "$d/go.sum" ]; then cp "$d/go.sum" "/depsctx/clients/integrations/$n/go.sum"; fi; \
    done; \
    export GOFLAGS="-tags=$(cat /backend/.generated/build_tags)"; \
    go list -deps -f '{{if and (not .Standard) .Module}}{{.ImportPath}}|{{.Module.Main}}|{{.Name}}{{end}}' all \
      | awk -F'|' 'NF==3 && $2=="false" && $3!="main" { print $1 }' | sort -u > /depsctx/external-packages.txt; \
    echo "[deps] external packages to warm: $(wc -l < /depsctx/external-packages.txt)"

FROM gobase AS depswarm
COPY --from=depsplan /depsctx/ /
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -e; \
    cd /backend; \
    if [ -f /backend/build_tags ]; then export GOFLAGS="-tags=$(cat /backend/build_tags)"; fi; \
    if [ -s /external-packages.txt ]; then \
      echo "[deps] warming $(wc -l < /external-packages.txt) external packages (GOFLAGS=${GOFLAGS:-<unset>})"; \
      xargs -n 150 go build < /external-packages.txt 2>/dev/null || true; \
    fi; \
    mkdir -p /gocache /gomodcache; \
    cp -a /root/.cache/go-build/. /gocache/; \
    cp -a /go/pkg/mod/. /gomodcache/; \
    echo "[deps] warm cache ready"

FROM basebuilder AS builder

ARG INTEGRATION_PROFILE=core-only
ENV INTEGRATION_PROFILE=${INTEGRATION_PROFILE}
COPY --from=frontend_selected /workspace/frontend/routes.json server/routes.json
COPY --from=frontend_selected /workspace/frontend/dist/client server/frontend/
# Seed the module + compile cache mounts from the source-independent warm layers.
COPY --from=depswarm /gocache/ /gocache/
COPY --from=depswarm /gomodcache/ /gomodcache/

# Generate the Go workspace + side-effect imports for the selected profile, then
# refresh the integration-owned frontend pages from the freshly built frontend.
# Each integration embeds its own prerendered HTML, which references
# content-hashed JS/CSS chunk filenames. Those hashes change on every frontend
# rebuild, so the committed copies go stale and the pages then 404 on their
# entry chunks. Re-exporting here guarantees the embedded HTML always matches
# the chunks served from this exact image.
RUN openchat-integrations resolve --profile "${INTEGRATION_PROFILE}" \
    && if [ -d /backend/server/frontend/integrations ]; then \
         openchat-integrations export --profile "${INTEGRATION_PROFILE}" \
           --dist-dir /backend/server/frontend; \
       else \
         echo "[integrations] frontend stage is empty; skipping integration page export"; \
       fi

ARG MVPAPP_VERSION=dockerbuild
RUN ls -alt
# Cache the Go module download, the compiler's content-addressed build cache and
# the repo-local tool bin (swag) so incremental builds only recompile the
# packages that actually changed instead of the whole dependency tree. The
# build cache is seeded from the source-independent /gocache layer (pre-warmed
# std) before building.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/backend/.tools \
    bash -c 'cp -a /gocache/. /root/.cache/go-build/ 2>/dev/null || true; cp -a /gomodcache/. /go/pkg/mod/ 2>/dev/null || true; bash full_build.sh --no-frontend'
RUN mkdir -p /backend/little_world_default_bots \
  && if [ -d /clients/integrations/admin_db_managemnt_integration/little_world_default_bots ]; then \
       cp -a /clients/integrations/admin_db_managemnt_integration/little_world_default_bots/. /backend/little_world_default_bots/; \
     fi

FROM scratch AS prod
COPY --from=builder /backend/backend /backend
COPY --from=builder /backend/little_world_default_bots /backend/little_world_default_bots

FROM docker.io/library/alpine:${ALPINE_VERSION} AS prod-alpine
WORKDIR /backend
COPY --from=builder /backend/backend /usr/local/bin/backend
COPY --from=builder /backend/server/routes.json /backend/routes.json
COPY --from=builder /backend/little_world_default_bots /backend/little_world_default_bots

CMD ["backend", "server"]
