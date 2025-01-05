# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.23
ARG ALPINE_VERSION=3.20

FROM node:22-alpine AS frontend
WORKDIR /frontend
RUN mkdir -p /frontend/remix
COPY frontend/remix/ ./
RUN npm install
RUN npm run build

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS basebuilder

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev
COPY backend/go.mod ./
COPY backend/go.sum ./
RUN CGO_ENABLED=1 go mod download

FROM basebuilder AS builder

COPY backend/ ./

ARG MVPAPP_VERSION=dockerbuild
RUN go build -ldflags="-X main.version=$MVPAPP_VERSION"


FROM scratch AS prod
COPY --from=builder /backend/backend /backend

FROM docker.io/library/alpine:${ALPINE_VERSION} AS prod-alpine
WORKDIR /backend
COPY --from=builder /backend/backend /usr/local/bin/backend
COPY --from=frontend /frontend/build/client ./frontend

CMD ["backend", "-b", "0.0.0.0", "-p", "1984"]