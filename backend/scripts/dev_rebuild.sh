#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-default}"
GEN_DIR="${ROOT_DIR}/.generated"
mkdir -p "$GEN_DIR"
EFFECTIVE_INTEGRATION_MANIFEST="${GEN_DIR}/integrationdeps.effective.json"
EFFECTIVE_MODFILE="${GEN_DIR}/go.effective.mod"

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

TOOL_JOBS=("./tooldeps.json:./api/msgmate/externaltools/imports_gen.go")
INTEGRATION_JOBS=("./integrationdeps.json:./integrations/externalintegrations/imports_gen.go")

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tool-job)
      if [[ $# -lt 2 ]]; then
        log "missing value for --tool-job"
        exit 1
      fi
      TOOL_JOBS+=("$2")
      shift 2
      ;;
    --integration-job)
      if [[ $# -lt 2 ]]; then
        log "missing value for --integration-job"
        exit 1
      fi
      INTEGRATION_JOBS+=("$2")
      shift 2
      ;;
    *)
      log "unknown argument: $1"
      log "supported arguments: --tool-job <manifest:output>, --integration-job <manifest:output>"
      exit 1
      ;;
  esac
done

run_generator_jobs() {
  local label="$1"
  local generator="$2"
  shift 2
  local jobs=("$@")

  for job in "${jobs[@]}"; do
    local manifest="${job%%:*}"
    local output="${job#*:}"
    if [[ -z "$manifest" || -z "$output" || "$manifest" == "$output" ]]; then
      log "invalid ${label} job '$job' (expected manifest:output)"
      exit 1
    fi

    log "${label}: manifest=${manifest} output=${output}"
    go run "$generator" -manifest "$manifest" -output "$output" -sync=false
  done
}

resolve_integrations() {
  python3 ./scripts/resolve_integrations.py \
    --manifest ./integrationdeps.json \
    --profile "$INTEGRATION_PROFILE" \
    --repo-root "$(cd "$ROOT_DIR/.." && pwd)" \
    --base-go-mod ./go.mod \
    --output-manifest "$EFFECTIVE_INTEGRATION_MANIFEST" \
    --output-modfile "$EFFECTIVE_MODFILE"
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

if [[ -n "${GOFLAGS:-}" ]]; then
  export GOFLAGS="${GOFLAGS} -modfile=${EFFECTIVE_MODFILE}"
else
  export GOFLAGS="-modfile=${EFFECTIVE_MODFILE}"
fi
log "using GOFLAGS=${GOFLAGS}"

run_step "swagger generation" generate_swagger

if [[ -f ./docs/swagger.json ]]; then
  if [[ ! -f ./server/swagger.json ]] || ! cmp -s ./docs/swagger.json ./server/swagger.json; then
    log "syncing swagger into embedded server file"
    cp ./docs/swagger.json ./server/swagger.json
  fi
fi

run_step "tool dependency generation" run_generator_jobs "tooldepsgen" "./scripts/tooldepsgen" "${TOOL_JOBS[@]}"
INTEGRATION_JOBS=("${EFFECTIVE_INTEGRATION_MANIFEST}:./integrations/externalintegrations/imports_gen.go")
run_step "integration dependency generation" run_generator_jobs "integrationdepsgen" "./scripts/integrationdepsgen" "${INTEGRATION_JOBS[@]}"
run_step "module download" go mod download
run_step "module tidy" go mod tidy

mkdir -p ./.devbin
run_step "backend build" go build -o ./.devbin/backend .
log "done"
