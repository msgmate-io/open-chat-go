#!/usr/bin/env bash
# ci#1062 (image half of ci#1038): keep the published `sandbox-coding` image
# from silently shipping a Go toolchain that cannot build this repo.
#
# The defect: the image carried go1.25.10 under /usr/local/go, off PATH, with no
# /etc/profile.d shim and no warmed module cache, while backend/go.mod required
# go 1.26.0 and GOTOOLCHAIN=local. `command -v go` was empty in every fresh
# shell, and even on PATH the build hard-failed with
#   go: go.mod requires go >= 1.26.0
#
# Two modes, because the two places that must agree live apart:
#
#   --dockerfile <path>   Static drift gate. Needs no docker daemon, so PR CI can
#                         run it. Asserts the build inputs encode the contract:
#                         GOTOOLCHAIN=local, a warm module cache, a PATH shim for
#                         *login* shells, and a Go version that is NOT older than
#                         the `go` directive of backend/go.mod - either delegated
#                         to development/scripts/install_sandbox_go_toolchain.sh
#                         (the ci#1038 single source of truth, so the pin is
#                         derived instead of restated) or pinned at or above it.
#                         A hard-coded version below the directive is exactly the
#                         drift this ticket is about, so it is a MISS.
#
#   --image <ref>         Behavioural acceptance run inside the published image
#                         (needs a docker daemon): `go` must resolve for both
#                         `bash -c` and `bash -lc`, GOTOOLCHAIN must be local,
#                         $GOMODCACHE/cache/download must be populated, and the
#                         installer's own `--check` (mounted from this checkout)
#                         must report ready - i.e. go >= backend/go.mod.
#
# Usage:
#   development/scripts/check_sandbox_coding_image_go.sh [repo_root]
#   development/scripts/check_sandbox_coding_image_go.sh --dockerfile <Dockerfile> [repo_root]
#   development/scripts/check_sandbox_coding_image_go.sh --image <image-ref> [repo_root]
#
# Exit codes: 0 ok / 1 MISS / 2 usage error or missing input.
set -uo pipefail

repo_root=""
dockerfile=""
image=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    --dockerfile=*) dockerfile="${1#*=}"; shift ;;
    --image=*) image="${1#*=}"; shift ;;
    --dockerfile|--image)
      [ "$#" -ge 2 ] || { echo "$1 requires an argument" >&2; exit 2; }
      if [ "$1" = "--dockerfile" ]; then dockerfile="$2"; else image="$2"; fi
      shift 2 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) repo_root="$1"; shift ;;
  esac
done

[ -n "$repo_root" ] || repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[ -d "$repo_root" ] || { echo "repo root '$repo_root' does not exist" >&2; exit 2; }
[ -n "$dockerfile$image" ] || {
  echo "usage: $0 (--dockerfile <path> | --image <ref>) [repo_root]" >&2; exit 2; }

note() { printf '%s\n' "$*"; }
status=0
miss() { note "MISS $*"; status=1; }

# The toolchain version the repo needs. Same source as the installer: the `go`
# directive of backend/go.mod. Never a constant in this script.
required=""
[ -f "$repo_root/backend/go.mod" ] &&
  required="$(awk '$1=="go"{print $2; exit}' "$repo_root/backend/go.mod")"

go_version_ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" = "$1" ]; }

note "== coding sandbox image Go contract (ci#1062) =="
note "     required (backend/go.mod): go ${required:-<unknown>}"

# --- static drift gate -------------------------------------------------------
if [ -n "$dockerfile" ]; then
  [ -f "$dockerfile" ] || { echo "Dockerfile '$dockerfile' does not exist" >&2; exit 2; }
  body="$(grep -v '^[[:space:]]*#' "$dockerfile")"
  note "     dockerfile:                $dockerfile"

  # The image's Go provisioning may be delegated to a script that the Dockerfile
  # COPYs in (that is the shape ci#1062's fix took: sandboxes/coding/
  # provision-go-toolchain.sh). Follow those COPYs so the *.sh that actually
  # writes the /etc/profile.d shim and runs `go mod download` is inspected too -
  # otherwise a perfectly good image would read as MISS just because the marker
  # moved one file over.
  df_dir="$(cd "$(dirname "$dockerfile")" && pwd)"
  followed=""
  for rel in $(printf '%s\n' "$body" |
      sed -n 's/.*COPY[[:space:]]\{1,\}\(\(--[a-z-]*=[^ ]*[[:space:]]\{1,\}\)*\)\{0,1\}\([^[:space:]]*\.sh\).*/\3/p'); do
    candidate="$df_dir/$rel"
    [ -f "$candidate" ] || continue
    followed="$followed $rel"
    body="$body
$(grep -v '^[[:space:]]*#' "$candidate")"
  done
  [ -z "$followed" ] || note "     provisioning scripts:     $(printf '%s' "$followed" | sed 's/^ //')"

  # 1. Off-PATH repair must exist for *login* shells too: Alpine's /etc/profile
  #    rewrites PATH, which is how /usr/local/go/bin vanished for `bash -lc`.
  if printf '%s\n' "$body" | grep -q '/etc/profile\.d'; then
    note "ok   PATH shim for login shells (/etc/profile.d) present"
  else
    miss "no /etc/profile.d PATH shim: 'bash -lc \"command -v go\"' will be empty"
  fi

  # 2. GOTOOLCHAIN=local - an implicit toolchain download is not offline-capable.
  if printf '%s\n' "$body" | grep -q 'GOTOOLCHAIN=local'; then
    note "ok   GOTOOLCHAIN=local"
  else
    miss "GOTOOLCHAIN=local is not set; an offline build would try to download a toolchain"
  fi

  # 3. Warmed module cache: $GOMODCACHE/cache/download must be populated at build
  #    time (go mod download), otherwise the first sandbox build needs network.
  if printf '%s\n' "$body" | grep -q 'GOMODCACHE'; then
    note "ok   GOMODCACHE is set"
  else
    miss "GOMODCACHE is not set; the warmed cache has no explicit location"
  fi
  if printf '%s\n' "$body" | grep -q 'go mod download'; then
    note "ok   module cache is warmed at build time (go mod download)"
  else
    miss "no 'go mod download' step: \$GOMODCACHE/cache/download stays empty"
  fi

  # 4. Version lock-step with backend/go.mod - the actual ci#1062 drift.
  if printf '%s\n' "$body" | grep -q 'install_sandbox_go_toolchain\.sh'; then
    note "ok   toolchain is derived from backend/go.mod (install_sandbox_go_toolchain.sh)"
  else
    # Prefer an explicit GO_VERSION pin (quoted or not), then a go<ver> tarball
    # reference. grep -Eo is used so a multi-digit version like 1.26.11 can never
    # be truncated by a greedy backreference in sed.
    pinned="$(printf '%s\n' "$body" |
      grep -Eo 'GO_VERSION=["'"'"']?[0-9]+(\.[0-9]+)+' | head -n1 |
      grep -Eo '[0-9]+(\.[0-9]+)+')"
    if [ -z "$pinned" ]; then
      pinned="$(printf '%s\n' "$body" |
        grep -Eo 'go[0-9]+\.[0-9]+(\.[0-9]+)?\.linux' | head -n1 |
        grep -Eo '[0-9]+(\.[0-9]+)+')"
    fi
    if [ -z "$pinned" ]; then
      miss "no Go version source: neither install_sandbox_go_toolchain.sh nor ARG GO_VERSION=<ver>"
    elif [ -z "$required" ]; then
      miss "backend/go.mod has no 'go' directive; cannot verify the pinned go${pinned}"
    elif go_version_ge "$pinned" "$required"; then
      note "ok   pinned go$pinned >= backend/go.mod (go $required)"
    else
      miss "pinned go$pinned is older than backend/go.mod (go $required); derive it from go.mod" \
        "     (install_sandbox_go_toolchain.sh) or bump GO_VERSION - this is the ci#1062 defect"
    fi
  fi
fi

# --- behavioural acceptance run inside the image -----------------------------
if [ -n "$image" ]; then
  command -v docker >/dev/null 2>&1 || {
    note "MISS docker CLI is absent; run this mode where a daemon exists (never install one)"; exit 1; }
  docker info >/dev/null 2>&1 || {
    note "MISS no docker daemon reachable; the --image mode cannot produce evidence here"; exit 1; }

  probe='
printf "go_path=%s\n" "$(command -v go 2>/dev/null)"
printf "go_path_login=%s\n" "$(bash -lc "command -v go" 2>/dev/null)"
printf "gotoolchain=%s\n" "$(go env GOTOOLCHAIN 2>/dev/null)"
printf "go_version=%s\n" "$(go env GOVERSION 2>/dev/null)"
mc="$(go env GOMODCACHE 2>/dev/null)"
printf "gomodcache=%s\n" "$mc"
printf "cache_entries=%s\n" "$(ls "$mc/cache/download" 2>/dev/null | wc -l | tr -d " ")"
printf "installer_check=%s\n" \
  "$(bash /openchat-go/development/scripts/install_sandbox_go_toolchain.sh --check /openchat-go 2>&1 | tail -n1)"
printf "readiness=%s\n" \
  "$(bash /openchat-go/development/scripts/check_coder_sandbox_readiness.sh /openchat-go 2>&1 | grep -E "^(== |missing_cli|MISS )" | tr "\n" "|")"
'
  out="$(docker run --rm -v "$repo_root:/openchat-go:ro" --entrypoint /bin/sh "$image" -c "$probe" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || { printf '%s\n' "$out" | tail -20; miss "in-image probe failed (exit $rc)"; }

  field() { printf '%s\n' "$out" | sed -n "s/^$1=//p" | head -n1; }
  go_path="$(field go_path)"
  go_login="$(field go_path_login)"
  toolchain="$(field gotoolchain)"
  go_version="$(field go_version)"
  entries="$(field cache_entries)"
  installer="$(field installer_check)"
  readiness="$(field readiness)"

  [ -n "$go_path" ] && note "ok   go on PATH, non-login shell ($go_path)" ||
    miss "go is not on PATH in a non-login shell (missing_cli: go)"
  [ -n "$go_login" ] && note "ok   go on PATH, login shell ($go_login)" ||
    miss "go is not on PATH in a login shell (bash -lc); the /etc/profile.d shim is missing"
  [ "$toolchain" = "local" ] && note "ok   GOTOOLCHAIN=local" ||
    miss "GOTOOLCHAIN=${toolchain:-<unset>}; an offline build may download a toolchain"
  case "$entries" in
    ''|*[!0-9]*) miss "could not read the module cache entry count" ;;
    0) miss "\$GOMODCACHE/cache/download is empty; 'go build ./backend/...' needs network" ;;
    *) note "ok   warmed module cache ($entries download entries in $mc)" ;;
  esac
  case "$installer" in
    ready*) note "ok   installer --check: ${go_version:-go} $installer" ;;
    *) miss "installer --check did not report ready in the image: ${installer:-<no output>}" ;;
  esac
  # The ticket's acceptance also calls for this probe's verdict in the image. Only
  # its Go section is the image's contract here: the probe's other sections check
  # the *sandbox workspace* (the integration CLI, the docker wrapper, gh), which a
  # bare `docker run` legitimately cannot satisfy - so those are reported, not
  # failed. A go-related MISS still fails the gate.
  if [ -z "$readiness" ]; then
    miss "check_coder_sandbox_readiness.sh produced no verdict in the image"
  else
    go_gaps="$(printf '%s' "$readiness" | tr '|' '\n' |
      grep -E '^(missing_cli: go|MISS .*(GOTOOLCHAIN|warmed module cache|older than|not on PATH))' | head -n3 | tr '\n' ';')"
    if [ -n "$go_gaps" ]; then
      miss "readiness probe reports a go gap in the image: ${go_gaps}"
    else
      note "ok   check_coder_sandbox_readiness.sh: no go gap (missing_cli: go / go MISS absent)"
    fi
    note "     readiness verdict: $(printf '%s' "$readiness" | tr '|' '\n' |
      grep -E '^(==|missing_cli:)' | tr '\n' ';')"
  fi
fi

if [ "$status" -eq 0 ]; then
  note "== ok: the coding image satisfies the ci#1062 Go contract =="
else
  note "== MISS: see the MISS lines above =="
fi
exit "$status"
