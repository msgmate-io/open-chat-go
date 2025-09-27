# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.25.10
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22
ARG FRONTEND_STAGE=frontend

FROM node:${NODE_VERSION}-alpine AS frontend
ARG INTEGRATION_PROFILE=core-only
RUN apk add --no-cache python3 py3-pip git
WORKDIR /workspace
COPY integrations.yaml integrations.lock.json /workspace/
COPY development/build-tools /workspace/development/build-tools
COPY clients/integrations /workspace/clients/integrations
RUN pip install --no-cache-dir --break-system-packages /workspace/development/build-tools
COPY frontend/ /workspace/frontend/
WORKDIR /workspace/frontend
# Link integration-owned pages (kept in the integration repositories) before
# building so Vike prerenders them. Only integrations that are present get
# linked, keeping per-integration React code private.
RUN openchat-integrations frontend --profile "${INTEGRATION_PROFILE}"
RUN npm install
RUN npm run build
RUN ./generate_golang_routes.sh

FROM docker.io/library/alpine:${ALPINE_VERSION} AS frontend_empty
WORKDIR /workspace/frontend
RUN mkdir -p /workspace/frontend/dist/client \
    && printf '{}\n' > /workspace/frontend/routes.json

FROM ${FRONTEND_STAGE} AS frontend_selected

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine AS basebuilder

ENV GOTOOLCHAIN=auto

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev bash libc6-compat python3 py3-pip py3-yaml git
COPY clients/ /clients/
COPY backend/ ./
# Integration manifest + build tooling. The manifest lives at the image root
# (repo root) so the manager resolves the same relative paths as on the host:
# /clients/integrations/<name> and /backend/go.work.
COPY integrations.yaml /integrations.yaml
COPY integrations.lock.json /integrations.lock.json
COPY development/build-tools /development/build-tools
RUN pip install --no-cache-dir --break-system-packages /development/build-tools

FROM basebuilder AS builder

ARG INTEGRATION_PROFILE=core-only
ENV INTEGRATION_PROFILE=${INTEGRATION_PROFILE}
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
RUN bash full_build.sh --no-frontend
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
