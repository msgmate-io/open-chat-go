# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.25.10
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22
ARG FRONTEND_STAGE=frontend

FROM node:${NODE_VERSION}-alpine AS frontend
WORKDIR /frontend
COPY frontend/ ./
RUN npm install
RUN npm run build
RUN ./generate_golang_routes.sh

FROM docker.io/library/alpine:${ALPINE_VERSION} AS frontend_empty
WORKDIR /frontend
RUN mkdir -p /frontend/dist/client /frontend/scripts \
    && printf '{}\n' > /frontend/routes.json \
    && printf '#!/usr/bin/env bash\nset -euo pipefail\necho "[export-integration-pages] skipped (empty frontend stage)"\n' > /frontend/scripts/export_integration_pages.sh \
    && chmod +x /frontend/scripts/export_integration_pages.sh

FROM ${FRONTEND_STAGE} AS frontend_selected

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine AS basebuilder

ENV GOTOOLCHAIN=auto

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev bash libc6-compat python3 git
COPY clients/ /clients/
COPY backend/ ./

FROM basebuilder AS builder

ARG INTEGRATION_PROFILE=default
ENV INTEGRATION_PROFILE=${INTEGRATION_PROFILE}
COPY --from=frontend_selected /frontend/routes.json server/routes.json
COPY --from=frontend_selected /frontend/dist/client server/frontend/

# Refresh the integration-owned frontend pages from the freshly built frontend
# before compiling the backend. Each integration embeds its own prerendered
# HTML, which references content-hashed JS/CSS chunk filenames. Those hashes
# change on every frontend rebuild, so the committed copies go stale and the
# pages then 404 on their entry chunks. Re-exporting here guarantees the
# embedded HTML always matches the chunks served from this exact image.
COPY --from=frontend_selected /frontend/scripts/export_integration_pages.sh /build/frontend/scripts/export_integration_pages.sh
RUN mkdir -p /build/frontend/dist \
    && ln -s /backend/server/frontend /build/frontend/dist/client \
    && ln -s /clients /build/clients
RUN bash /build/frontend/scripts/export_integration_pages.sh

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
