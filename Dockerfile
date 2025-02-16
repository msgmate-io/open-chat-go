# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.24
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine${ALPINE_VERSION} AS frontend
WORKDIR /frontend
COPY frontend/ ./
RUN npm install
RUN npm run build
RUN ./generate_golang_routes.sh

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS basebuilder

ENV GOTOOLCHAIN=auto

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev bash libc6-compat
COPY backend/go.mod ./
COPY backend/go.sum ./
RUN CGO_ENABLED=1 go mod download

FROM basebuilder AS builder

COPY backend/ ./
COPY --from=frontend /frontend/routes.json server/routes.json
COPY --from=frontend /frontend/dist/client server/frontend/

ARG MVPAPP_VERSION=dockerbuild
ARG FEDERATION_ENABLED=false
RUN ls -alt
RUN if [ "$FEDERATION_ENABLED" = "true" ]; then \
        bash full_build.sh --no-frontend; \
    else \
        bash full_build.sh --no-frontend --no-federation; \
    fi

FROM scratch AS prod
COPY --from=builder /backend/backend /backend

FROM docker.io/library/alpine:${ALPINE_VERSION} AS prod-alpine
WORKDIR /backend
COPY --from=builder /backend/backend /usr/local/bin/backend
COPY --from=builder /backend/server/routes.json /backend/routes.json

CMD ["backend"]
