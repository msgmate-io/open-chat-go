# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.26
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22
ARG FRONTEND_STAGE=frontend

# Empty default for the Go build-cache bind context. CI overrides this named
# context (`--build-context gobuildcache=<restored actions/cache dir>`) so the
# persisted compiler cache is injected from the host instead of relying on the
# (non-exported) BuildKit cache mount. Local builds keep the empty stage.
FROM scratch AS gobuildcache

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

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine AS basebuilder

ARG INTEGRATION_PROFILE=core-only

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
# Install the swagger code generator once into a source-independent layer. It
# depends only on the toolchain (not the checkout), so the builder stage can
# seed /backend/.tools from /swagcache and skip the ~19s `go install @latest`
# that used to sit on every build's critical path.
RUN CGO_ENABLED=0 GOBIN=/swagcache/bin go install github.com/swaggo/swag/v2/cmd/swag@latest
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
# Copy only the module manifests before the source so the (expensive) module
# download layer is keyed solely on the go.mod/go.sum files and survives every
# source-only change. The full source is copied right after it.
COPY backend/go.mod backend/go.sum ./
# Generate the workspace for this profile and download the entire module graph
# as a *normal* layer stashed at /gomodcache. Unlike a `type=cache` mount this
# layer is exported by the registry cache (mode=max), so the ~240s
# `go mod download` is paid once per module-graph change instead of every run.
RUN openchat-integrations resolve --profile "${INTEGRATION_PROFILE}" \
    && GOWORK=/backend/go.work GOMODCACHE=/gomodcache go mod download
COPY backend/ ./

FROM basebuilder AS builder

ARG INTEGRATION_PROFILE=core-only
ENV INTEGRATION_PROFILE=${INTEGRATION_PROFILE}
# Build the production image exactly like the release pipeline: a static
# (CGO_ENABLED=0), stripped (-s -w) and path-reproducible (-trimpath) binary.
# This shrinks the embedded SQLite/Postgres-only backend from ~165 MB to the
# ~50 MB release size without changing runtime behaviour.
ENV OPEN_CHAT_RELEASE_BUILD=1 CGO_ENABLED=0
COPY --from=frontend_selected /workspace/frontend/routes.json server/routes.json
COPY --from=frontend_selected /workspace/frontend/dist/client server/frontend/

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
# Cache the compiler's content-addressed build cache and the repo-local tool bin
# (swag) so incremental builds only recompile the packages that actually changed
# instead of the whole dependency tree.
#  - /gocache is the source-independent std prewarm baked into basebuilder.
#  - /gomodcache is the module graph downloaded as a normal layer in basebuilder
#    (exported by the registry cache, so no re-download on source-only builds).
#  - gobuildcache is an optional named build context (`--build-context
#    gobuildcache=<restored actions/cache dir>`); when present it seeds the
#    compiler cache so CI opens the build with pre-compiled dependencies. It is
#    empty (scratch stage) for local builds that do not pass the context.
#  - /swagcache/bin seeds the .tools cache mount so `go install swag@latest`
#    never runs on the critical path.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/backend/.tools \
    --mount=type=bind,from=gobuildcache,target=/gobuildcache,ro \
    bash -c 'cp -a /gocache/. /root/.cache/go-build/ 2>/dev/null || true; \
             cp -a /gomodcache/. /go/pkg/mod/ 2>/dev/null || true; \
             if [ -d /gobuildcache ]; then cp -a /gobuildcache/. /root/.cache/go-build/ 2>/dev/null || true; fi; \
             mkdir -p /backend/.tools/bin && cp -a /swagcache/bin/. /backend/.tools/bin/ 2>/dev/null || true; \
             bash full_build.sh --no-frontend'
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
