#!/usr/bin/env bash
# Self-test for development/scripts/check_dep_surface_gate.sh (ci#423).
#
# Pinned outcomes of the PR gate:
#   - absent baseline file                          -> exit 0, no-op line
#   - baseline validates, no workspace (GOWORK)     -> exit 0, validation only
#   - measured surface matches the baseline         -> exit 0
#   - a new backend/... import edge appears         -> exit 1, the edge printed
#   - malformed baseline                            -> exit 1/2, never green
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
GATE="$REPO_ROOT/development/scripts/check_dep_surface_gate.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- stub `go` (answers `go list -deps [-test] <pattern>` from a TSV map) ---
mkdir -p "$TMP/bin"
cat > "$TMP/bin/go" <<'PYSTUB'
#!/usr/bin/env python3
import os
import sys

pattern = ""
with_tests = False
for arg in sys.argv[1:]:
    if arg == "-test":
        with_tests = True
    elif arg not in ("list", "-deps"):
        pattern = arg

try:
    rows = open(os.environ.get("GO_STUB_MAP", "/nonexistent"), encoding="utf-8").read().splitlines()
except OSError:
    sys.exit(1)

for row in rows:
    pat, kind, pkg = row.split("\t")
    if pat != pattern:
        continue
    if kind == "prod" or (kind == "test" and with_tests):
        print(pkg)
PYSTUB
chmod +x "$TMP/bin/go"
export PATH="$TMP/bin:$PATH"
export GOWORK="$TMP/go.work"
: > "$GOWORK"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
run_gate() {
  set +e
  OUT="$(bash "$GATE" --baseline "${BASELINE:-$TMP/none.json}" --repo-root "$FAKE_ROOT" "$@" 2>&1)"
  RC=$?
  set -e
}

FAKE_ROOT="$TMP/repo"
mkdir -p "$FAKE_ROOT/clients/integrations/one"
printf 'module example.com/one\n\ngo 1.25\n' > "$FAKE_ROOT/clients/integrations/one/go.mod"
export GO_STUB_MAP="$TMP/map.tsv"
printf 'example.com/one/...\tprod\texample.com/one\n' > "$GO_STUB_MAP"

# --- absent baseline: no-op -------------------------------------------------
run_gate
[ "$RC" -eq 0 ] || fail "an absent baseline must be a no-op (got $RC): $OUT"
grep -q "gate is a no-op" <<<"$OUT" || fail "the no-op must be stated: $OUT"

# --- decoupled, matching baseline -------------------------------------------
BASELINE="$TMP/good.json"
cat > "$BASELINE" <<'JSON'
{"version": 1, "integrations": {"example.com/one": {"directory": "one", "packages": 1, "backend": 0, "backend_packages": [], "test_packages": 1, "test_backend": 0}}}
JSON
run_gate
[ "$RC" -eq 0 ] || fail "a matching surface must pass (got $RC): $OUT"
grep -q "no regression against the baseline" <<<"$OUT" || fail "missing no-regression line: $OUT"

# --- validation only (no workspace) -----------------------------------------
run_gate --no-measure
[ "$RC" -eq 0 ] || fail "--no-measure must pass (got $RC): $OUT"
grep -q "baseline was only validated" <<<"$OUT" || fail "missing validation-only line: $OUT"

# --- GOWORK unset or not materialized: validation only, never a measurement --
GOWORK_SAVED="$GOWORK"
unset GOWORK
run_gate
[ "$RC" -eq 0 ] || fail "an unset GOWORK must validate only (got $RC): $OUT"
grep -q "GOWORK is unset" <<<"$OUT" || fail "the unset GOWORK must be stated: $OUT"
GOWORK="$TMP/missing-go.work"
export GOWORK
run_gate
[ "$RC" -eq 0 ] || fail "a missing workspace file must validate only (got $RC): $OUT"
grep -q "not a workspace file" <<<"$OUT" || fail "the missing workspace must be stated: $OUT"
GOWORK="$GOWORK_SAVED"
export GOWORK

# --- still-coupled but unpinned integration: reported, not red -----------------
# During the migration most integrations are coupled on purpose, so the gate must
# only fail on a regression against the baseline, never on an unpinned module.
mkdir -p "$FAKE_ROOT/clients/integrations/two"
printf 'module example.com/two\n\ngo 1.25\n' > "$FAKE_ROOT/clients/integrations/two/go.mod"
printf 'example.com/two/...\tprod\texample.com/two\nexample.com/two/...\tprod\tbackend/server/two\n' >> "$GO_STUB_MAP"
run_gate
[ "$RC" -eq 0 ] || fail "an unpinned still-coupled integration must not fail the gate (got $RC): $OUT"
grep -q "still-migrating integrations" <<<"$OUT" || fail "still-migrating modules must be reported: $OUT"
grep -q "example.com/two" <<<"$OUT" || fail "the still-coupled module must be named: $OUT"
# Drop it again so the regression case below measures only the pinned module.
sed -i '/example.com\/two/d' "$GO_STUB_MAP"
rm -rf "$FAKE_ROOT/clients/integrations/two"

# --- a new import edge into private internals fails and names the edge ------
printf 'example.com/one/...\tprod\tbackend/server/util\n' >> "$GO_STUB_MAP"
run_gate
[ "$RC" -eq 1 ] || fail "a new backend/... edge must fail the gate (got $RC): $OUT"
grep -q "REGRESSIONS vs baseline" <<<"$OUT" || fail "missing regression header: $OUT"
grep -q "backend/server/util" <<<"$OUT" || fail "the offending edge must be printed: $OUT"

# --- a malformed baseline never turns the gate green -----------------------
printf '{"version": 1, "integrations": {"example.com/x": {"backend": 2}}}' > "$TMP/bad.json"
BASELINE="$TMP/bad.json"
run_gate
[ "$RC" -ne 0 ] || fail "a malformed baseline must not pass (got $RC): $OUT"
grep -q "malformed baseline" <<<"$OUT" || fail "the malformed baseline must be named: $OUT"

printf 'check_dep_surface_gate self-test: ok\n'
