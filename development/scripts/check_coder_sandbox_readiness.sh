#!/usr/bin/env bash
# ci#739: report whether the per-chat coding sandbox is ready for local Go
# checks. The msgmate coder agents repeatedly lost turns to two provisioning
# gaps instead of writing code:
#
#   1. `go`/`gofmt` are installed at /usr/local/go/bin (dev.dockerfile exports
#      PATH=/dev_bin:/usr/local/go/bin:...) but are missing from the PATH of the
#      shell the agent drives, so every `go build`/`go test` starts with
#      "command not found".
#   2. The integration modules are resolved through the private profile
#      workspace (`development/scripts/build_tools.sh` -> `openchat-integrations
#      prepare --profile full-ci`, which also writes the gitignored
#      backend/go.work). In a fresh sandbox that step has not run, so package
#      compilation dies on private imports such as upstream/go-integration-interface
#      and the agent mistakes it for a defect.
#
# Usage: development/scripts/check_coder_sandbox_readiness.sh [repo_root]
# Exit code 0 when ready, 1 when something the sandbox should provide is missing.
set -uo pipefail

root="${1:-$(cd "$(dirname "$0")/../.." && pwd)}"
[ -d "$root" ] || { echo "repo root '$root' does not exist" >&2; exit 2; }

status=0
note() { printf '%s\n' "$*"; }

note "== coding sandbox readiness (ci#739) =="

for tool in go gofmt; do
  if command -v "$tool" >/dev/null 2>&1; then
    note "ok   $tool on PATH ($(command -v "$tool"))"
  elif [ -x "/usr/local/go/bin/$tool" ]; then
    note "MISS $tool is installed at /usr/local/go/bin/$tool but not on PATH;"
    note "     export PATH=/usr/local/go/bin:/dev_bin:$PATH"
    status=1
  else
    note "MISS $tool is not installed in this image at all;"
    note "     the coder sandbox image must ship the Go toolchain before local checks are attempted."
    status=1
  fi
done

if [ -n "${GOPRIVATE:-}" ]; then
  note "ok   GOPRIVATE=$GOPRIVATE"
else
  note "MISS GOPRIVATE is unset; private integration modules will not resolve non-interactively."
  note "     export GOPRIVATE='github.com/msgmate-io/*' GONOSUMDB='github.com/msgmate-io/*'"
  status=1
fi

if [ -f "$root/backend/go.work" ]; then
  note "ok   backend/go.work exists (profile workspace prepared)"
else
  note "MISS backend/go.work is missing; run the profile setup before any Go check:"
  note "     development/scripts/build_tools.sh >/dev/null && \$(development/scripts/build_tools.sh) prepare --profile full-ci"
  status=1
fi

if command -v openchat-integrations >/dev/null 2>&1; then
  note "ok   openchat-integrations on PATH"
elif [ -x "$root/.venv/bin/openchat-integrations" ]; then
  note "ok   $root/.venv/bin/openchat-integrations present"
else
  note "MISS the integration manager CLI is absent, so the sandbox cannot self-provision the"
  note "     private profile workspace. Provisioning must install .venv/openchat-integrations."
  status=1
fi

for mod in upstream/go-tool-interface upstream/go-integration-interface; do
  if [ -d "$root/$mod" ]; then
    note "ok   $mod checkout present"
  else
    note "WARN $mod is not checked out; only needed by changes that touch it."
  fi
done

# ci#739: the set-up shell and the agent's later shells are different processes.
# If the PATH/module opt-outs were only exported and never persisted, the very
# next `coder_bash` starts with "go: command not found" again.
if [ -f "${HOME:-/root}/.bashrc" ] && grep -qF '# openchat sandbox environment' "${HOME:-/root}/.bashrc" 2>/dev/null; then
  note "ok   sandbox environment persisted to ~/.bashrc"
else
  note "WARN the sandbox environment is not persisted to ~/.bashrc; later shells may not"
  note "     see the Go toolchain. Re-run the dev set-up (the generated script persists it)."
fi

if [ "$status" -eq 0 ]; then
  note "== ready: local targeted Go checks can run =="
else
  note "== NOT ready: see the MISS lines above. If a private module is the only blocker,\n     keep the change and let PR CI be the authoritative full-suite gate (ci#739). =="
fi
exit "$status"
