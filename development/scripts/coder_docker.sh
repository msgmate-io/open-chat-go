#!/usr/bin/env bash
# coder_docker.sh — the one supported way for a coding agent to talk to docker.
#
# Why this exists (ci#787)
# -----------------------
# Coding sandboxes are provisioned without a docker daemon often enough that
# agents used to improvise: `docker compose up`, `docker run --privileged`,
# installing a daemon, retrying a failing `docker build` ten times. That burns
# sandbox time, hides the real blocker and (worse) can leak host state.
#
# Rules this wrapper enforces:
#   * one explicit subcommand per docker workflow (mirrors the typed
#     docker_sandbox_* tools in backend/nativecoder/dockertooling.go);
#   * a fast readiness probe so an agent learns "no daemon here" once instead of
#     once per command (exit code 3, never a raw docker error);
#   * a hard refusal to install or start a daemon inside the sandbox;
#   * bounded output so a chatty container cannot flood the agent context;
#   * no implicit secrets: the wrapper never reads or forwards tokens, it only
#     passes through the env the caller explicitly exports.
#
# Usage:
#   development/scripts/coder_docker.sh check
#   development/scripts/coder_docker.sh ps
#   development/scripts/coder_docker.sh logs <container> [lines]
#   development/scripts/coder_docker.sh build <tag> <context> [-- docker build args]
#   development/scripts/coder_docker.sh run <image> [-- command...]
#   development/scripts/coder_docker.sh compose <project-dir> <up|down|logs|ps>
#   development/scripts/coder_docker.sh exec <container> <cmd...>
#
# Exit codes:
#   0  the command ran
#   1  the command failed
#   2  usage error
#   3  no usable docker daemon in this sandbox (report it, do not work around it)
set -uo pipefail

MAX_LINES="${CODER_DOCKER_MAX_LINES:-200}"

log() { printf '%s\n' "$*" >&2; }

usage() {
  log "usage: coder_docker.sh {check|ps|logs|build|run|compose|exec} ..."
  log "run 'coder_docker.sh' with no arguments inside a checkout for the full help"
  sed -n '2,34p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log "docker_sandbox_check: FAIL — no docker CLI on PATH."
    log "This sandbox cannot run docker. Do not install a daemon here:"
    log "  * keep the change, run the fast docker-free checks (unit tests, gofmt, lint);"
    log "  * let PR CI build images / run compose / e2e;"
    log "  * report the gap in the PR or issue body instead of retrying raw docker commands."
    exit 3
  fi
  if ! docker info >/dev/null 2>&1; then
    log "docker_sandbox_check: FAIL — docker CLI present but no daemon is reachable."
    log "Do not start a daemon inside the sandbox; report the gap and continue without docker."
    exit 3
  fi
}

# cap_file <file> — print at most MAX_LINES lines of <file>, with a trim marker.
cap_file() {
  local file="$1" total
  total="$(wc -l <"$file" | tr -d ' ')"
  if [ "$total" -gt "$MAX_LINES" ]; then
    printf '... (%s lines trimmed; last %s shown)\n' "$((total - MAX_LINES))" "$MAX_LINES"
    tail -n "$MAX_LINES" "$file"
  else
    cat "$file"
  fi
}

# run_capped <cmd...> — run the command, cap its combined output, and return the
# command's own exit status (not tail's) so failures are never masked.
run_capped() {
  local out rc
  out="$(mktemp "${TMPDIR:-/tmp}/coder_docker_out.XXXXXX")"
  "$@" >"$out" 2>&1
  rc=$?
  cap_file "$out"
  rm -f "$out"
  return "$rc"
}

cmd="${1:-}"
[ -z "$cmd" ] && usage
shift || true

case "$cmd" in
  check)
    require_docker
    log "docker_sandbox_check: OK — daemon reachable; docker_sandbox_* tools can run containers."
    docker version --format '{{.Server.Version}}' 2>/dev/null | sed 's/^/server: /'
    ;;
  ps)
    require_docker
    run_capped docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
    ;;
  logs)
    require_docker
    [ $# -ge 1 ] || usage
    run_capped docker logs --tail "${2:-$MAX_LINES}" "$1"
    ;;
  build)
    require_docker
    [ $# -ge 2 ] || usage
    tag="$1"; context="$2"; shift 2
    [ "${1:-}" = "--" ] && shift
    run_capped docker build -t "$tag" "$@" "$context"
    ;;
  run)
    require_docker
    [ $# -ge 1 ] || usage
    image="$1"; shift
    [ "${1:-}" = "--" ] && shift
    # --rm by default: a coding sandbox must not accumulate anonymous containers.
    run_capped docker run --rm "$image" "$@"
    ;;
  compose)
    require_docker
    [ $# -ge 2 ] || usage
    dir="$1"; action="$2"; shift 2
    case "$action" in
      up|down|logs|ps) ;;
      *) log "unsupported compose action: $action (use up|down|logs|ps)"; exit 2 ;;
    esac
    ( cd "$dir" && run_capped docker compose "$action" "$@" )
    ;;
  exec)
    require_docker
    [ $# -ge 2 ] || usage
    container="$1"; shift
    run_capped docker exec "$container" "$@"
    ;;
  *)
    usage
    ;;
esac
