#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$ROOT_DIR/.." && pwd)"
cd "$ROOT_DIR"

INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-}"

log() {
  printf '[dev-rebuild %(%H:%M:%S)T] %s\n' -1 "$1"
}

run_step() {
  local label="$1"
  shift
  local start=$SECONDS
  log "start: ${label}"
  "$@"
  log "done: ${label} (${SECONDS-start}s)"
}

# Regenerate the Go workspace + side-effect imports + build tags for the
# selected integration profile from integrations.yaml.
resolve_integrations() {
  local cli
  if command -v openchat-integrations >/dev/null 2>&1; then
    cli="openchat-integrations"
  else
    cli="$(bash "$REPO_ROOT/development/scripts/build_tools.sh")"
  fi
  "$cli" resolve ${INTEGRATION_PROFILE:+--profile "$INTEGRATION_PROFILE"}
}

generate_swagger() {
  if [[ -x /dev_bin/swag ]]; then
    /dev_bin/swag init --parseDependency --parseDependencyLevel 3 --parseInternal --output ./docs --generalInfo ./main.go
  elif command -v swag >/dev/null 2>&1; then
    swag init --parseDependency --parseDependencyLevel 3 --parseInternal --output ./docs --generalInfo ./main.go
  else
    go run github.com/swaggo/swag/v2/cmd/swag@latest init --parseDependency --parseDependencyLevel 3 --parseInternal --output ./docs --generalInfo ./main.go
  fi
}

run_step "resolve integrations" resolve_integrations

# Build in Go workspace mode; the generated workspace selects the profile's
# modules and replaces all msgmate module paths.
export GOWORK="$ROOT_DIR/go.work"

BUILD_TAGS_FILE="$ROOT_DIR/.generated/build_tags"
if [[ -f "$BUILD_TAGS_FILE" ]]; then
  GO_TAGS="$(cat "$BUILD_TAGS_FILE")"
  if [[ -n "$GO_TAGS" ]]; then
    export GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=${GO_TAGS}"
  fi
fi

log "using GOWORK=${GOWORK} GOFLAGS=${GOFLAGS:-<unset>}"

run_step "module download" go mod download
run_step "swagger generation" generate_swagger

if [[ -f ./docs/swagger.json ]]; then
  if [[ ! -f ./server/swagger.json ]] || ! cmp -s ./docs/swagger.json ./server/swagger.json; then
    log "syncing swagger into embedded server file"
    cp ./docs/swagger.json ./server/swagger.json
  fi
fi

mkdir -p ./.devbin
run_step "backend build" go build -o ./.devbin/backend .
log "done"
