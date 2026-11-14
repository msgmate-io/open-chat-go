#!/usr/bin/env bash
# ci#1038: hermetic coverage for the *default-PATH* leg of
# development/scripts/install_sandbox_go_toolchain.sh.
#
# Why this test exists: the installer already persisted the toolchain to
# ~/.bashrc and /etc/profile.d, yet a fresh sandbox still answered
#
#   $ which go
#   which: no go in (/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin)
#
# because both of those are read by *login/interactive* shells only, while the
# agent's coder_bash tool runs a non-login, non-interactive shell. The sandbox
# therefore "had" a toolchain that no implementer could invoke, which is how
# three agent turns passed without a single local `go test`. The fix is a
# symlink in a default-PATH directory; this test pins that behaviour.
#
# No network, no real toolchain: a stub `go` answers `env GOROOT`/`env GOVERSION`.
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
goroot="$tmp/goroot"
bin="$tmp/bin"
mkdir -p "$goroot/bin" "$bin"
cat > "$goroot/bin/go" <<STUB
#!/usr/bin/env bash
case "\$1 \$2" in
  "env GOROOT") echo "$goroot" ;;
  "env GOVERSION") echo "go1.26.0" ;;
  *) exit 0 ;;
esac
STUB
cat > "$goroot/bin/gofmt" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
chmod +x "$goroot/bin/go" "$goroot/bin/gofmt"

# --- 1. the shim is written and proven from a stripped default PATH -----------
out="$(PATH="$goroot/bin:/usr/bin:/bin" \
       OPENCHAT_SANDBOX_GO_HINT_DIR="$goroot/bin" \
       OPENCHAT_SANDBOX_GO_BIN_DIR="$bin" \
       OPENCHAT_SANDBOX_DEFAULT_PATH="$bin:/usr/bin:/bin" \
       HOME="$tmp" \
       bash "$installer" "$root" 2>&1)"
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

# --- 2. an unreachable toolchain is reported, never silently ignored ---------
out2="$(PATH="$goroot/bin:/usr/bin:/bin" \
        OPENCHAT_SANDBOX_GO_HINT_DIR="$goroot/bin" \
        OPENCHAT_SANDBOX_GO_BIN_DIR="$tmp/nobin" \
        OPENCHAT_SANDBOX_DEFAULT_PATH="/usr/bin:/bin" \
        HOME="$tmp" \
        bash "$installer" "$root" 2>&1)"
st2=$?
printf '%s\n' "$out2" | grep -q 'MISS go is not on the default PATH' || {
  echo "FAIL expected an explicit MISS for an unreachable default PATH" >&2; rc=1; }
if [ "$st2" -eq 0 ]; then
  echo "FAIL an unreachable default PATH must not exit 0 (agents would claim a build)" >&2
  rc=1
fi

[ "$rc" -eq 0 ] && echo "test_sandbox_go_default_path: OK"
exit "$rc"
