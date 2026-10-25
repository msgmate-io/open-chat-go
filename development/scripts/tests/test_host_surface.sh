#!/usr/bin/env bash
# Hermetic self-test for development/scripts/check_host_surface.py (ci#423).
# No Go toolchain and no network: fixtures are plain directories.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/check_host_surface.py"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0

fix() { # $1 fixture name; prints its directory
  local dir="$TMP/$1"
  mkdir -p "$dir/clients/go_integration_interface/integrationinterface"
  cat > "$dir/clients/go_integration_interface/integrationinterface/host.go" <<'EOF'
package integrationinterface

const HostVersion = 4

type Host interface{ Config() Config }
EOF
  cat > "$dir/clients/go_integration_interface/go.mod" <<'EOF'
module github.com/msgmate-io/go-integration-interface

go 1.23

require gorm.io/gorm v1.25.0
EOF
  python3 "$SCRIPT" --root "$dir" --write-baseline "$dir/baseline.json" > /dev/null
  echo "$dir"
}

expect() { # $1 expected exit, $2 label, $3 dir, $4 expected output
  local got
  got="$(python3 "$SCRIPT" --root "$3" --baseline "$3/baseline.json" 2>&1 | tee "$TMP/out.txt" > /dev/null; echo "${PIPESTATUS[0]}")"
  if [ "$got" != "$1" ] || ! grep -q "$4" "$TMP/out.txt"; then
    echo "FAIL: $2 (exit $got, expected $1)"
    sed 's/^/    /' "$TMP/out.txt"
    FAILS=$((FAILS + 1))
  else
    echo "ok: $2"
  fi
}

clean="$(fix clean)"
expect 0 "clean surface passes" "$clean" "0 pinned leak"

unpinned="$(fix unpinned)"
printf 'var _ = (*gorm.DB)(nil)\n' >> "$unpinned/clients/go_integration_interface/integrationinterface/extra.go"
expect 1 "unpinned leak in a new file fails" "$unpinned" "unpinned surface leak"

muxed="$(fix muxed)"
printf 'type assets struct{ fs fs.FS }\ntype routes struct{ mux *http.ServeMux }\n' >> "$muxed/clients/go_integration_interface/integrationinterface/host.go"
expect 1 "fs.FS/http.ServeMux fail" "$muxed" "fs.FS"

grown="$(fix grown)"
printf 'func f(db *gorm.DB) {}\n' >> "$grown/clients/go_integration_interface/integrationinterface/host.go"
python3 "$SCRIPT" --root "$grown" --write-baseline "$grown/baseline.json" > /dev/null
printf 'func g(db *gorm.DB) {}\n' >> "$grown/clients/go_integration_interface/integrationinterface/host.go"
expect 1 "pinned leak growth fails" "$grown" "gorm.DB grew 1 -> 2"

shrank="$(fix shrank)"
printf 'func f(db *gorm.DB) {}\nfunc g(tx gorm.io/gorm.DB) {}\n' >> "$shrank/clients/go_integration_interface/integrationinterface/host.go"
python3 "$SCRIPT" --root "$shrank" --write-baseline "$shrank/baseline.json" > /dev/null
python3 - "$shrank/clients/go_integration_interface/integrationinterface/host.go" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read().replace("func g(tx gorm.io/gorm.DB) {}\n", "")
open(path, "w").write(text)
PY
expect 0 "partial leak shrink passes with a note" "$shrank" "gorm.io/gorm shrank 1 -> 0"

required="$(fix required)"
printf 'require github.com/google/uuid v1.6.0\n' >> "$required/clients/go_integration_interface/go.mod"
expect 1 "new direct requirement fails" "$required" "gained direct requirements"

versioned="$(fix versioned)"
sed -i 's/HostVersion = 4/HostVersion = 5/' "$versioned/clients/go_integration_interface/integrationinterface/host.go"
expect 1 "HostVersion drift fails" "$versioned" "HostVersion 4 -> 5"

stale="$(fix stale)"
printf 'var _ = (*gorm.DB)(nil)\n' >> "$stale/clients/go_integration_interface/integrationinterface/extra.go"
python3 "$SCRIPT" --root "$stale" --write-baseline "$stale/baseline.json" > /dev/null
rm "$stale/clients/go_integration_interface/integrationinterface/extra.go"
expect 1 "stale pinned entry fails" "$stale" "pinned leak is gone"

if python3 "$SCRIPT" --check-baseline "$clean/baseline.json" > /dev/null 2>&1; then echo "ok: --check-baseline accepts a valid baseline"; else echo "FAIL: --check-baseline rejected a valid baseline"; FAILS=$((FAILS + 1)); fi
sed -i 's/"version": 1/"version": 2/' "$clean/baseline.json"
if python3 "$SCRIPT" --check-baseline "$clean/baseline.json" > /dev/null 2>&1; then echo "FAIL: --check-baseline accepted a malformed baseline"; FAILS=$((FAILS + 1)); else echo "ok: --check-baseline rejects a malformed baseline"; fi

if [ "$FAILS" -ne 0 ]; then
  echo "$FAILS host-surface guard self-test failure(s)"
  exit 1
fi
echo "host-surface guard self-test: all cases pass"
