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
#   development/scripts/install_sandbox_go_toolchain.sh --plan [repo_root]
#   development/scripts/install_sandbox_go_toolchain.sh [--verify=<cmd>] [repo_root]
#
# Contract (ci#1038):
#   * the toolchain version comes from the `go` directive of backend/go.mod, so
#     the sandbox can never drift from the module the repo builds with;
#   * GOTOOLCHAIN=local is exported - without it `go` silently downloads a newer
#     toolchain and the "works offline" promise is a lie;
#   * the module cache is warmed once (`go mod download`), so later `go build
#     ./...` runs do not need the network;
#   * the environment is persisted to ~/.bashrc (created when absent, as it is in
#     the image) *and* to /etc/profile.d/openchat-sandbox-go.sh, because the shell
#     that provisions and the shell the agent later drives are separate processes
#     and a `bash -lc` login shell must find the toolchain regardless of HOME;
#   * a sandbox that still has no usable `go` says so machine-readably
#     (`missing_cli: go` + `go: MISSING`, the same token family coder_bash uses
#     for an absent CLI) instead of leaving an agent free to claim a test run
#     that never happened.
#
# `--check` never mutates anything and never touches the network: it is what a
# pre-push self-check and the unit test in
# development/scripts/tests/test_sandbox_go_toolchain.sh drive.
#
# `--plan` is the same probe, but it prints the *reasons* as `reasons=<csv>` and
# keeps going after the first gap, so a caller can triage several at once
# (`ci#1038`): a too-old toolchain and an unwarmed module cache are one repair
# away, while "nothing installed and no egress" is not.
#
# `--verify=<cmd>` exists so a report can carry COMMAND OUTPUT instead of a
# narrative (`ci#1038`): it provisions, runs `<cmd>` with the provisioned
# environment (GOTOOLCHAIN=local, the sandbox PATH), prints its output verbatim
# and exits with its status. A failing or absent toolchain makes the whole run
# say `go: MISSING` / `missing_cli: go`; it can never print a green verdict for a
# command that did not run. Example:
#
#   development/scripts/install_sandbox_go_toolchain.sh \
#     --verify='go build ./... && go test ./nativecoder/...' . 
#
# Exit codes: 0 ready / provisioned / verify command green, 1 not ready or verify
# command red, 2 usage error.
set -uo pipefail

root=""
check_only=0
plan_only=0
verify_cmd=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --check) check_only=1; shift ;;
    --plan) plan_only=1; shift ;;
    --verify=*) verify_cmd="${1#*=}"; shift ;;
    --verify)
      [ "$#" -ge 2 ] || { echo "--verify requires a command argument" >&2; exit 2; }
      verify_cmd="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,55p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) root="$1"; shift ;;
  esac
done
if [ "$check_only" -eq 1 ] && [ "$plan_only" -eq 1 ]; then
  echo "--check and --plan are mutually exclusive" >&2
  exit 2
fi
if [ -n "$verify_cmd" ] && { [ "$check_only" -eq 1 ] || [ "$plan_only" -eq 1 ]; }; then
  echo "--verify cannot be combined with --check/--plan" >&2
  exit 2
fi
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

# --- gap probes -------------------------------------------------------------
# ci#1038: `--check` exits at the first gap (a pre-push gate wants one answer),
# `--plan` walks every gap and prints them as `reasons=<csv>` so an implementer
# can triage. Both read the same probes, so they can never disagree about what
# "ready" means.

# why_not_ready - fill `gaps` with reason tokens, print the human MISS lines.
# Always exits 0; the caller decides. Reasons, in probe order:
#   no_go             no toolchain anywhere (missing_cli: go)
#   go_too_old        present but older than the backend/go.mod directive
#   no_gomod_directive  backend/go.mod carries no `go` directive (cannot pin)
#   gotoolchain_set   GOTOOLCHAIN != local, so a build may fetch a toolchain
#   no_module_cache   $GOMODCACHE/cache/download is absent (offline build fails)
gaps=""
add_gap() { gaps="${gaps:+$gaps,}$1"; }

why_not_ready() {
  if [ -z "$go_bin" ]; then
    add_gap no_go
    note "missing_cli: go"
    note "go: MISSING"
    note "     no Go toolchain in this sandbox, so NO local build or test run is possible."
    note "     Do not substitute narrative for a test run: say 'go: MISSING' and let PR CI"
    note "     be the only gate. To provision, re-run this script without --check/--plan,"
    note "     or run it with --verify='<command>' so the report carries real output."
    return 0
  fi
  if [ -z "$required" ]; then
    add_gap no_gomod_directive
    note "MISS backend/go.mod has no 'go' directive; cannot pin a toolchain version."
  elif [ "$satisfies" -eq 0 ]; then
    add_gap go_too_old
    note "MISS go $have_version is older than the backend/go.mod directive (go $required)."
    note "     Re-run this script without --check to install the pinned toolchain."
  fi
  if [ "$("$go_bin" env GOTOOLCHAIN 2>/dev/null)" = "local" ]; then
    note "ok   GOTOOLCHAIN=local (no implicit toolchain download)"
  else
    add_gap gotoolchain_set
    note "MISS GOTOOLCHAIN=$("$go_bin" env GOTOOLCHAIN 2>/dev/null); an offline build may try to"
    note "     download a toolchain. Re-run this script without --check to pin it."
  fi
  modcache="$("$go_bin" env GOMODCACHE 2>/dev/null)"
  if [ -n "$modcache" ] && [ -d "$modcache/cache/download" ]; then
    note "ok   warmed module cache ($modcache/cache/download)"
  else
    add_gap no_module_cache
    note "MISS no warmed module cache at ${modcache:-<unset>}/cache/download; an offline build"
    note "     will fail on network fetches. Re-run this script without --check."
  fi
  return 0
}

# --- report-only modes ------------------------------------------------------
if [ "$check_only" -eq 1 ] || [ "$plan_only" -eq 1 ]; then
  why_not_ready
  if [ -z "$gaps" ]; then
    note "== ready: go $have_version can build offline =="
    [ "$plan_only" -eq 1 ] && note "reasons="
    exit 0
  fi
  # The gaps are the machine-readable answer: no narrative can turn them green.
  note "reasons=$gaps"
  note "== NOT ready: $gaps =="
  exit 1
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
  # ci#1038: the download base is overridable so the hermetic suite can prove the
  # "download failed, therefore no verification ran" path without the network.
  dl_base="${OPENCHAT_SANDBOX_GO_DL_BASE:-https://go.dev/dl}"
  note "     downloading go${required} (linux-${goarch}) -> $install_root/go"
  if ! curl -fsSL -o "$tarball" "${dl_base%/}/go${required}.linux-${goarch}.tar.gz"; then
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
  # The pre-install probe said "too old"; the freshly installed toolchain is the
  # new answer, so re-derive it here. Without this, a caller that provisions *and*
  # verifies in one run (`--verify=...`) would still report the stale gap.
  if [ -n "$have_version" ] && [ -n "$required" ] && go_version_ge "$have_version" "$required"; then
    satisfies=1
  else
    satisfies=0
  fi
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
#
# Two sinks, one block (ci#1038):
#   * ~/.bashrc      - non-login interactive shells (`bash -c`, coder_bash).
#   * /etc/profile.d - login shells (`bash -lc`). The image used to carry no
#                      .bashrc at all, so appending to $HOME/.bashrc silently did
#                      nothing and no shell ever picked the toolchain up.
# The block is written idempotently (the previous copy is stripped first), so
# re-running the installer never stacks duplicate exports.
block_begin='# openchat sandbox environment'
block_end='# end openchat sandbox environment'
render_block() {
  printf '%s\n' "$block_begin"
  printf '%s\n' 'export PATH="/usr/local/go/bin:/dev_bin:${PATH:-/usr/bin:/bin}"'
  printf '%s\n' 'export GOTOOLCHAIN=local'
  printf '%s\n' 'export GOPRIVATE="${GOPRIVATE:-github.com/msgmate-io/*}"'
  printf '%s\n' 'export GONOSUMDB="${GONOSUMDB:-github.com/msgmate-io/*}"'
  printf '%s\n' 'export GOPATH="${GOPATH:-$HOME/go}"'
  printf '%s\n' 'export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"'
  printf '%s\n' "$block_end"
}
# write_block <target> - strip any previous block from <target>, then append the
# current one. Creates the file when missing and never fails on a missing parent.
write_block() {
  local target="$1" filtered
  mkdir -p "$(dirname "$target")" 2>/dev/null || true
  filtered="$(mktemp)"
  if [ -f "$target" ]; then
    awk -v b="$block_begin" -v e="$block_end" '
      $0==b {skip=1; next}
      $0==e {skip=0; next}
      skip!=1 {print}
    ' "$target" > "$filtered"
  fi
  render_block >> "$filtered"
  [ -f "$target" ] || : > "$target" 2>/dev/null || true
  if cat "$filtered" > "$target" 2>/dev/null; then
    rm -f "$filtered"
    return 0
  fi
  rm -f "$filtered"
  return 1
}

bashrc="${HOME:-/root}/.bashrc"
# `touch` first: an image without a ~/.bashrc (the ci#1038 sandbox) made the
# append below the first thing that tried to create the file, and the failed
# write left the environment persisted nowhere.
if [ ! -f "$bashrc" ]; then
  if touch "$bashrc" 2>/dev/null; then
    note "note created $bashrc (the image shipped none)"
  fi
fi
if write_block "$bashrc"; then
  note "ok   sandbox environment persisted to $bashrc"
else
  note "WARN could not write $bashrc; later shells will not see the Go toolchain."
  status=1
fi

# A login shell (`bash -lc`) reads /etc/profile.d/*.sh instead of ~/.bashrc, and
# its HOME may differ from the provisioning HOME. Write the same block there when
# the directory is writable; not being root is not an error (the ~/.bashrc copy
# still exists), it is reported and skipped. The directory is overridable so the
# hermetic test suite (ci#1044) can exercise this leg without touching /etc.
profile_d="${OPENCHAT_SANDBOX_GO_PROFILE_DIR:-/etc/profile.d}"
if [ -d "$profile_d" ] && [ -w "$profile_d" ]; then
  if write_block "$profile_d/openchat-sandbox-go.sh"; then
    note "ok   login shells reach the toolchain via $profile_d/openchat-sandbox-go.sh"
  else
    note "WARN could not write $profile_d/openchat-sandbox-go.sh; a login shell may not see go."
  fi
else
  note "note $profile_d is not writable here; relying on ~/.bashrc for later shells."
fi

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

# --- verify mode ------------------------------------------------------------
# ci#1038: a report must carry COMMAND OUTPUT, not a narrative. The command runs
# with the environment the installer just provisioned (PATH + GOTOOLCHAIN=local +
# the warmed module cache), so `go test ./<pkg>/...` is measured in the same
# sandbox a later coder_bash call sees. When the toolchain is not usable the
# command is NOT run: printing a green-looking verdict for a command that could
# never have executed is exactly the fabrication this mode exists to prevent.
if [ -n "$verify_cmd" ]; then
  note "== verify: $verify_cmd =="
  # Only a *missing or too-old toolchain* blocks the run: an unwarmed module cache
  # or a non-local GOTOOLCHAIN is reported as a WARN, because the command itself is
  # the measurement and a sandbox with egress can still fetch what it needs.
  if [ -z "$go_bin" ] || [ "$satisfies" -eq 0 ]; then
    gaps=""
    why_not_ready >/dev/null 2>&1
    note "go: MISSING"
    note "missing_cli: go"
    note "NOT run: the command above was skipped because no usable Go $required toolchain is"
    note "     installed here (reasons=${gaps:-no_go}). Do not report it as passing: say"
    note "     'go: MISSING' and let PR CI be the gate."
    exit 1
  fi
  gaps=""
  why_not_ready >/dev/null 2>&1
  if [ -n "$gaps" ]; then
    note "WARN toolchain usable but not fully offline-clean (reasons=$gaps); running anyway."
  fi
  ( cd "$root" && eval "$verify_cmd" ); verify_status=$?
  if [ "$verify_status" -eq 0 ]; then
    note "== verified (exit 0): $verify_cmd =="
  else
    note "== FAILED (exit $verify_status): $verify_cmd =="
  fi
  exit "$verify_status"
fi

exit "$status"
