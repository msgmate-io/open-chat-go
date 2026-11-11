#!/usr/bin/env bash
# ci#1038: give the per-chat coding sandbox a usable Go toolchain, and make a
# missing one impossible to paper over.
#
# The ticket: implementation dispatches on the `open-chat-go` monorepo ended
# without a single local `go build`/`go test`, because the sandbox image carried
# no Go toolchain (`which go` -> "no go in (...)", while `git`/`gh` existed).
# Agents then answered "verified" with a narrative instead of command output, and
# a red test was only ever discovered from CI. `9e35c5b` made the *probe*
# (development/scripts/check_coder_sandbox_readiness.sh) fail loudly; this script
# is the provisioning half it points at.
#
# Usage:
#   development/scripts/install_sandbox_go_toolchain.sh [repo_root]
#   development/scripts/install_sandbox_go_toolchain.sh --check [repo_root]
#
# Contract (ci#1038):
#   * the toolchain version comes from the `go` directive of backend/go.mod, so
#     the sandbox can never drift from the module the repo builds with;
#   * GOTOOLCHAIN=local is exported - without it `go` silently downloads a newer
#     toolchain and the "works offline" promise is a lie;
#   * the module cache is warmed once (`go mod download`), so later `go build
#     ./...` runs do not need the network;
#   * the environment is persisted to ~/.bashrc, because the shell that
#     provisions and the shell the agent later drives are separate processes;
#   * a sandbox that still has no usable `go` says so machine-readably
#     (`missing_cli: go` + `go: MISSING`, the same token family coder_bash uses
#     for an absent CLI) instead of leaving an agent free to claim a test run
#     that never happened.
#
# `--check` never mutates anything and never touches the network: it is what a
# pre-push self-check and the unit test in
# development/scripts/tests/test_sandbox_go_toolchain.sh drive.
#
# Exit codes: 0 ready / provisioned, 1 not ready, 2 usage error.
set -uo pipefail

root=""
check_only=0
for arg in "$@"; do
  case "$arg" in
    --check) check_only=1 ;;
    -h|--help)
      sed -n '2,35p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) root="$arg" ;;
  esac
done
[ -n "$root" ] || root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
[ -d "$root" ] || { echo "repo root '$root' does not exist" >&2; exit 2; }

note() { printf '%s\n' "$*"; }
status=0

# The pinned toolchain version is whatever backend/go.mod declares. Reading it
# (instead of hard-coding a version here) is what keeps the sandbox and the
# module in lockstep.
required=""
if [ -f "$root/backend/go.mod" ]; then
  required="$(awk '$1=="go"{print $2; exit}' "$root/backend/go.mod")"
fi

# go_version_ge A B -> 0 when A >= B (both plain "1.24.4"-style strings).
go_version_ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" = "$1" ]; }

# Detect the toolchain. A `go` that is installed but off-PATH (/usr/local/go/bin,
# the dev.dockerfile location) is repaired instead of installed twice. The hint
# directory is overridable purely so the unit test can simulate a bare sandbox on
# a runner that does happen to have Go in the default location.
go_hint="${OPENCHAT_SANDBOX_GO_HINT_DIR:-/usr/local/go/bin}"
go_bin=""
if command -v go >/dev/null 2>&1; then
  go_bin="$(command -v go)"
elif [ -n "$go_hint" ] && [ -x "$go_hint/go" ]; then
  note "note go is installed at $go_hint/go but not on PATH; adding it"
  PATH="$go_hint:/dev_bin:${PATH:-/usr/bin:/bin}"
  export PATH
  go_bin="$go_hint/go"
fi

have_version=""
if [ -n "$go_bin" ]; then
  have_version="$("$go_bin" env GOVERSION 2>/dev/null | sed 's/^go//')"
fi

satisfies=1
if [ -n "$have_version" ] && [ -n "$required" ]; then
  go_version_ge "$have_version" "$required" || satisfies=0
fi

note "== coding sandbox Go toolchain (ci#1038) =="
note "     required (backend/go.mod): go ${required:-<unknown>}"
note "     present:                   ${have_version:-<none>}"

# --- report-only mode -------------------------------------------------------
if [ "$check_only" -eq 1 ]; then
  if [ -z "$go_bin" ]; then
    note "missing_cli: go"
    note "go: MISSING"
    note "     no Go toolchain in this sandbox, so NO local build or test run is possible."
    note "     Do not substitute narrative for a test run: say 'go: MISSING' and let PR CI"
    note "     be the only gate. To provision, re-run this script without --check."
    exit 1
  fi
  if [ "$satisfies" -eq 0 ]; then
    note "MISS go $have_version is older than the backend/go.mod directive (go $required)."
    note "     Re-run this script without --check to install the pinned toolchain."
    exit 1
  fi
  if [ "$("$go_bin" env GOTOOLCHAIN 2>/dev/null)" = "local" ]; then
    note "ok   GOTOOLCHAIN=local (no implicit toolchain download)"
  else
    note "MISS GOTOOLCHAIN=$("$go_bin" env GOTOOLCHAIN 2>/dev/null); an offline build may try to"
    note "     download a toolchain. Re-run this script without --check to pin it."
    exit 1
  fi
  modcache="$("$go_bin" env GOMODCACHE 2>/dev/null)"
  if [ -n "$modcache" ] && [ -d "$modcache/cache/download" ]; then
    note "ok   warmed module cache ($modcache/cache/download)"
  else
    note "MISS no warmed module cache at ${modcache:-<unset>}/cache/download; an offline build"
    note "     will fail on network fetches. Re-run this script without --check."
    exit 1
  fi
  note "== ready: go $have_version can build offline =="
  exit 0
fi

# --- provisioning mode ------------------------------------------------------

if [ -n "$go_bin" ] && [ "$satisfies" -eq 1 ]; then
  note "ok   go $have_version satisfies backend/go.mod (go $required)"
else
  if [ -z "$required" ]; then
    note "MISS backend/go.mod has no 'go' directive; cannot pick a toolchain version."
    note "missing_cli: go"
    exit 1
  fi
  case "$(uname -m)" in
    x86_64|amd64) goarch="amd64" ;;
    aarch64|arm64) goarch="arm64" ;;
    *) note "MISS unsupported architecture $(uname -m) for the Go toolchain download."; exit 1 ;;
  esac
  for tool in curl tar; do
    command -v "$tool" >/dev/null 2>&1 || {
      note "missing_cli: $tool"
      note "MISS $tool is required to install the Go toolchain into this sandbox."
      exit 1
    }
  done
  tarball="${TMPDIR:-/tmp}/go${required}.linux-${goarch}.tar.gz"
  # Default install prefix mirrors dev.dockerfile (/usr/local/go). It is
  # overridable so the unit test can exercise the install path without root.
  install_root="${OPENCHAT_SANDBOX_GO_INSTALL_ROOT:-/usr/local}"
  note "     downloading go${required} (linux-${goarch}) -> $install_root/go"
  if ! curl -fsSL -o "$tarball" "https://go.dev/dl/go${required}.linux-${goarch}.tar.gz"; then
    note "missing_cli: go"
    note "go: MISSING"
    note "MISS the Go toolchain download failed (offline sandbox?). Do not claim tests ran:"
    note "     report 'go: MISSING' and let PR CI be the gate."
    exit 1
  fi
  rm -rf "${install_root:?}/go"
  if ! tar -C "$install_root" -xzf "$tarball"; then
    note "MISS could not extract $tarball into $install_root."; exit 1
  fi
  rm -f "$tarball"
  PATH="$install_root/go/bin:/dev_bin:${PATH:-/usr/bin:/bin}"
  export PATH
  go_bin="$install_root/go/bin/go"
  have_version="$("$go_bin" env GOVERSION 2>/dev/null | sed 's/^go//')"
  note "ok   installed go $have_version at $go_bin"
fi

# GOTOOLCHAIN=local: never let `go` fetch a different toolchain mid-build; a
# sandbox build must be reproducible from what the image ships.
export GOTOOLCHAIN=local
# Private integration modules are resolved non-interactively, so they must not be
# treated as public proxies/toolchains.
export GOPRIVATE="${GOPRIVATE:-github.com/msgmate-io/*}"
export GONOSUMDB="${GONOSUMDB:-github.com/msgmate-io/*}"

# Warm the module cache so the *next* build works without the network. A failure
# here is a warning, not an error: private integration modules may need the
# go.work profile workspace (development/scripts/build_tools.sh) first, and PR CI
# is still the authoritative full-suite gate.
if [ -d "$root/backend" ]; then
  note "     warming the module cache (go mod download)"
  if ( cd "$root/backend" && GOTOOLCHAIN=local go mod download >/dev/null 2>&1 ); then
    note "ok   module cache warmed ($(go env GOMODCACHE))"
  else
    note "WARN 'go mod download' failed (private modules and/or offline sandbox); later builds"
    note "     may need development/scripts/build_tools.sh prepare --profile full-ci first."
  fi
fi

# Persist the environment: the provisioning shell and the agent's later shells are
# different processes, so an `export` alone is lost and the next coder_bash call
# starts with "go: command not found" again (ci#739).
bashrc="${HOME:-/root}/.bashrc"
block_begin='# openchat sandbox environment'
block_end='# end openchat sandbox environment'
tmp_bashrc="$(mktemp)"
if [ -f "$bashrc" ]; then
  awk -v b="$block_begin" -v e="$block_end" '
    $0==b {skip=1; next}
    $0==e {skip=0; next}
    skip!=1 {print}
  ' "$bashrc" > "$tmp_bashrc"
fi
{
  printf '%s\n' "$block_begin"
  printf '%s\n' 'export PATH="/usr/local/go/bin:/dev_bin:$PATH"'
  printf '%s\n' 'export GOTOOLCHAIN=local'
  printf '%s\n' 'export GOPRIVATE="${GOPRIVATE:-github.com/msgmate-io/*}"'
  printf '%s\n' 'export GONOSUMDB="${GONOSUMDB:-github.com/msgmate-io/*}"'
  printf '%s\n' "$block_end"
} >> "$tmp_bashrc"
if cat "$tmp_bashrc" > "$bashrc" 2>/dev/null; then
  note "ok   sandbox environment persisted to $bashrc"
else
  note "WARN could not write $bashrc; later shells will not see the Go toolchain."
  status=1
fi
rm -f "$tmp_bashrc"

# Final self-check: provision, then prove it. The probe is the single source of
# truth for "can this sandbox run local Go checks", so the installer delegates
# instead of re-implementing it.
probe="$root/development/scripts/check_coder_sandbox_readiness.sh"
if [ -x "$probe" ]; then
  note "== readiness probe =="
  bash "$probe" "$root" || status=1
fi

if [ "$status" -eq 0 ]; then
  note "== provisioned: go $have_version (GOTOOLCHAIN=local, module cache warmed) =="
else
  note "== provisioned with gaps: see the WARN/MISS lines above =="
fi
exit "$status"
