#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

log() {
  printf '[dev-rebuild] %s\n' "$1"
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

log "generating swagger"
if command -v swag >/dev/null 2>&1; then
  swag init --parseDependency --parseInternal --output ./docs --generalInfo ./main.go
else
  go run github.com/swaggo/swag/v2/cmd/swag@latest init --parseDependency --parseInternal --output ./docs --generalInfo ./main.go
fi

if [[ -f ./docs/swagger.json ]]; then
  if [[ ! -f ./server/swagger.json ]] || ! cmp -s ./docs/swagger.json ./server/swagger.json; then
    log "syncing swagger into embedded server file"
    cp ./docs/swagger.json ./server/swagger.json
  fi
fi

run_generator_jobs "tooldepsgen" "./scripts/tooldepsgen" "${TOOL_JOBS[@]}"
run_generator_jobs "integrationdepsgen" "./scripts/integrationdepsgen" "${INTEGRATION_JOBS[@]}"

mkdir -p ./.devbin
log "building backend binary"
go build -o ./.devbin/backend .
log "done"
