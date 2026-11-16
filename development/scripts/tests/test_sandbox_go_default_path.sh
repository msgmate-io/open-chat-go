#!/usr/bin/env bash
# ci#1038: hermetic coverage for the *shell-independence* legs of
# development/scripts/install_sandbox_go_toolchain.sh:
#
#   1. default PATH  - a symlink in a default-PATH directory, so a non-login,
#                      non-interactive shell (what `coder_bash` runs) can
#                      resolve `go` at all;
#   2. $GOENV        - `go env -w GOTOOLCHAIN=local` / GOPRIVATE, because the
#                      same non-login shell never reads ~/.bashrc or
#                      /etc/profile.d, so an exported GOTOOLCHAIN=auto silently
#                      turns an "offline" build into a toolchain download.
#
# Why this test exists: the installer already persisted the toolchain to
# ~/.bashrc and /etc/profile.d, yet a fresh sandbox still answered
#
#   $ which go
#   which: no go in (/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin)
#
# because both of those are read by *login/interactive* shells only. The sandbox
# therefore "had" a toolchain that no implementer could invoke, which is how
# three agent turns passed without a single local `go test`.
#
# No network, no real toolchain: a stub `go` answers env queries and records
# `go env -w` writes in a state file, exactly like $GOENV does.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
installer="$here/../install_sandbox_go_toolchain.sh"
[ -x "$installer" ] || { echo "FAIL installer not executable: $installer" >&2; exit 1; }

rc=0
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# A fake checkout: only backend/go.mod matters for the version pin.
root="$tmp/repo"
mkdir -p "$root/backend"
printf 'module example.test/openchat\n\ngo 1.26.0\n' > "$root/backend/go.mod"

# A stub toolchain in a *non-default* location (mirrors /usr/local/go/bin).
# `env -w` appends to a state file that stands in for $GOENV, so the default
# `GOTOOLCHAIN` really is `auto` until provisioning persists `local`.
goroot="$tmp/goroot"
bin="$tmp/bin"
state="$goroot/goenv"
mkdir -p "$goroot/bin" "$bin"
cat > "$goroot/bin/go" <<STUB
#!/usr/bin/env bash
state="$state"
root="$root"
get() { sed -n "s/^\$1=//p" "\$state" 2>/dev/null | tail -1; }
case "\$1 \$2" in
  "env GOROOT") echo "$goroot" ;;
  "env GOVERSION") echo "go1.26.0" ;;
  "env GOENV") echo "\$state" ;;
  "env -w")
    shift 2
    for kv in "\$@"; do printf '%s\n' "\$kv" >> "\$state"; done
    ;;
  "env GOTOOLCHAIN") v="\$(get GOTOOLCHAIN)"; echo "\${v:-auto}" ;;
  "env GOPRIVATE") get GOPRIVATE ;;
  "env GONOSUMDB") get GONOSUMDB ;;
  "env GOMODCACHE") echo "\${GOMODCACHE:-\$HOME/go/pkg/mod}" ;;
  "mod download")
    # Mirrors the real repo: the committed go.sum is missing hashes for the
    # integration-only modules, so the warm-up appends them. Idempotent, so a
    # second run must leave go.sum alone (both branches are asserted below).
    grep -q 'mailru/easyjson' "\$root/backend/go.sum" 2>/dev/null ||
      printf 'github.com/mailru/easyjson v1.0.0 h1:stub=\n' >> "\$root/backend/go.sum"
    ;;
  *) exit 0 ;;
esac
STUB
cat > "$goroot/bin/gofmt" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
chmod +x "$goroot/bin/go" "$goroot/bin/gofmt"

run_installer() {
  # $1 = shim directory, $2 = default PATH; everything else is isolated.
  PATH="$goroot/bin:/usr/bin:/bin" \
    OPENCHAT_SANDBOX_GO_HINT_DIR="$goroot/bin" \
    OPENCHAT_SANDBOX_GO_BIN_DIR="$1" \
    OPENCHAT_SANDBOX_DEFAULT_PATH="$2" \
    HOME="$tmp" \
    bash "$installer" "$root" 2>&1
}

# --- 1. the shim is written and proven from a stripped default PATH -----------
out="$(run_installer "$bin" "$bin:/usr/bin:/bin")"
st=$?
if [ ! -L "$bin/go" ]; then
  echo "FAIL expected symlink $bin/go (the default-PATH shim); out:" >&2
  printf '%s\n' "$out" | grep -E 'default PATH|WARN|MISS' >&2
  rc=1
fi
printf '%s\n' "$out" | grep -q 'default PATH resolves go' || {
  echo "FAIL installer did not confirm the default-PATH leg" >&2; rc=1; }
if [ "$st" -ne 0 ]; then
  echo "FAIL installer exited $st in a provisionable sandbox" >&2
  printf '%s\n' "$out" | grep -E 'MISS|WARN' >&2
  rc=1
fi

# --- 2. GOTOOLCHAIN=local survives into a shell that inherits nothing --------
# ~/.bashrc is not written by this fixture's non-login shell, so only $GOENV can
# carry the setting: if provisioning skipped `go env -w` the stub would still say
# `auto` and an offline build would try to download a toolchain.
printf '%s\n' "$out" | grep -q 'GOTOOLCHAIN=local persisted' || {
  echo "FAIL installer did not persist GOTOOLCHAIN=local via 'go env -w'" >&2; rc=1; }
printf '%s\n' "$out" | grep -q 'stripped shell (default PATH, GOENV only) reports GOTOOLCHAIN=local' || {
  echo "FAIL installer did not prove GOTOOLCHAIN=local from a stripped shell" >&2; rc=1; }
grep -qx 'GOTOOLCHAIN=local' "$state" 2>/dev/null || {
  echo "FAIL GOTOOLCHAIN=local is not in the GOENV state file ($state)" >&2; rc=1; }

# --- 3. provisioning never overwrites a value the image already persisted -----
out3="$(run_installer "$bin" "$bin:/usr/bin:/bin")"
if [ "$(grep -c '^GOTOOLCHAIN=' "$state")" -ne 1 ]; then
  echo "FAIL re-running the installer duplicated GOTOOLCHAIN in GOENV:" >&2
  grep '^GOTOOLCHAIN=' "$state" >&2
  rc=1
fi
printf '%s\n' "$out3" | grep -q 'GOPRIVATE=github.com/msgmate-io/\* (already set)' || {
  echo "FAIL an already-persisted GOPRIVATE must not be rewritten" >&2; rc=1; }

# --- 4. the warm-up completes an incomplete go.sum and says so ----------------
# Regression for the acceptance line: the committed backend/go.sum is missing the
# hashes for the integration-only modules, so without the warm-up's completion
# `go build ./...` refuses to run offline ("missing go.sum entry") and the
# ticket's acceptance line silently stays unmet. Provisioning must (a) actually
# complete it and (b) disclose it, because the resulting `M backend/go.sum` is
# otherwise indistinguishable from an implementer's edit.
rm -f "$root/backend/go.sum"
out4="$(run_installer "$bin" "$bin:/usr/bin:/bin")"
st4=$?
grep -q 'mailru/easyjson' "$root/backend/go.sum" 2>/dev/null || {
  echo "FAIL the warm-up did not complete backend/go.sum" >&2; rc=1; }
printf '%s\n' "$out4" | grep -q 'backend/go.sum gained the entries' || {
  echo "FAIL installer did not disclose that it completed backend/go.sum" >&2; rc=1; }
printf '%s\n' "$out4" | grep -q "git checkout -- backend/go.sum" || {
  echo "FAIL installer did not tell the implementer how to drop the go.sum churn" >&2; rc=1; }
if [ "$st4" -ne 0 ]; then
  echo "FAIL installer exited $st4 although the warm-up succeeded" >&2; rc=1
fi

# --- 5. the disclosed go.sum churn is one-shot, not a new dirty tree per run ---
out5="$(run_installer "$bin" "$bin:/usr/bin:/bin")"
printf '%s\n' "$out5" | grep -q 'backend/go.sum untouched' || {
  echo "FAIL a repeated warm-up must report go.sum as untouched (no endless churn)" >&2; rc=1; }

# --- 6. an unreachable toolchain is reported, never silently ignored ---------
# The "unreachable" PATH has to be hermetic, not the literal `/usr/bin:/bin`: a
# hosted runner is free to ship a real `go` there (the ubuntu-24.04 image does),
# and then this leg stops measuring the installer and starts asserting a
# property of the host. That is exactly how it failed in the Pin Freshness Gate
# ("FAIL expected an explicit MISS for an unreachable default PATH" +
# "FAIL an unreachable default PATH must not exit 0") while the same fixture
# passed on a bare machine. Build a directory that provably resolves no `go` but
# still resolves `sh`, because the installer probes the default PATH with
# `env -i PATH=<default> sh -c 'command -v go'` and `env` finds `sh` through
# that same PATH.
nopath="$tmp/nopath"
mkdir -p "$nopath"
ln -sfn "$(command -v sh)" "$nopath/sh"
if [ -n "$(env -i PATH="$nopath" sh -c 'command -v go' 2>/dev/null)" ]; then
  echo "FAIL the hermetic no-go PATH unexpectedly resolves go ($nopath)" >&2
  rc=1
fi
out2="$(run_installer "$tmp/nobin" "$nopath")"
st2=$?
# The shim directory does not exist, so nothing may be linked into it either: a
# shim written elsewhere would make the MISS below a lie.
if [ -e "$tmp/nobin/go" ] || [ -e "$tmp/nobin/gofmt" ]; then
  echo "FAIL the shim was written into a non-existent bin dir ($tmp/nobin)" >&2
  rc=1
fi
printf '%s\n' "$out2" | grep -q 'MISS go is not on the default PATH' || {
  echo "FAIL expected an explicit MISS for an unreachable default PATH" >&2; rc=1; }
if [ "$st2" -eq 0 ]; then
  echo "FAIL an unreachable default PATH must not exit 0 (agents would claim a build)" >&2
  rc=1
fi

[ "$rc" -eq 0 ] && echo "test_sandbox_go_default_path: OK"
exit "$rc"
