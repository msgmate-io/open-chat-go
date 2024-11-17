# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.23
ARG ALPINE_VERSION=3.20
FROM docker.io/library/golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS basebuilder

WORKDIR /mvpapp

COPY backend/go.mod ./
COPY backend/go.sum ./
RUN go mod download

FROM basebuilder AS builder

COPY backend/ ./

ARG MVPAPP_VERSION=dockerbuild
RUN go build -ldflags="-X main.version=$MVPAPP_VERSION"


FROM scratch AS prod
COPY --from=builder /mvpapp/backend /mvpapp

FROM docker.io/library/alpine:${ALPINE_VERSION} AS prod-alpine
COPY --from=builder /mvpapp/backend /usr/local/bin/mvpapp
