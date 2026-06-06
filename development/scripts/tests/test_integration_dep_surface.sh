#!/usr/bin/env bash
# Self-test for backend/scripts/integration_dep_surface.py (ci#423).
#
# The ticket's step 4 ("measure the dependency-surface delta before/after") is
# only useful if the measurement itself is falsifiable, so its outcomes are
# pinned here without a Go toolchain: a stub `go` answers `go list -deps
# [-test] <pattern>` from a tab-separated fixture file.
#
# Pinned outcomes:
#   - --write-baseline records per-module package/backend counts  -> exit 1
#     (a coupled integration is still coupled, but the baseline is written)
#   - --baseline with no new backend/... package                  -> exit 0
#   - --baseline after a backend/... import reappears             -> exit 1
#     and the new package is named
#   - --baseline where only the backend count grew                -> exit 1
#   - test-only backend/... packages do not count as a regression  -> exit 0
#   - missing baseline file                                       -> exit 2
#   - missing integrations directory                              -> exit 1
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$REPO_ROOT/backend/scripts/integration_dep_surface.py"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- stub `go` -------------------------------------------------------------
STUB_BIN="$TMP/bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
# Answers `go list -deps [-test] <module>/...` from $GO_STUB_MAP, a file of
# tab-separated `pattern<TAB>prod|test<TAB>package` lines.
pattern=""
with_tests=0
for arg in "$@"; do
  case "$arg" in
    -test) with_tests=1 ;;
    list|-deps) ;;
    *) pattern="$arg" ;;
  esac
done
if [ -n "${GO_STUB_FAIL:-}" ]; then
  echo "stub go: forced failure" >&2
  exit 1
fi
if [ ! -f "${GO_STUB_MAP:-/nonexistent}" ]; then
  echo "stub go: no map configured" >&2
  exit 1
fi
while IFS=$'\t' read -r pat kind pkg; do
  [ -n "$pat" ] || continue
  [ "$pat" = "$pattern" ] || continue
  case "$kind" in
    prod) printf '%s\n' "$pkg" ;;
    test) [ "$with_tests" -eq 1 ] && printf '%s\n' "$pkg" ;;
  esac
done < "$GO_STUB_MAP"
exit 0
STUB
chmod +x "$STUB_BIN/go"
export PATH="$STUB_BIN:$PATH"
export GOWORK=off

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

run_surface() { # run_surface [extra args...]
  set +e
  OUT="$(python3 "$SCRIPT" --repo-root "$FAKE_ROOT" "$@" 2>&1)"
  RC=$?
  set -e
}

# --- fake monorepo layout --------------------------------------------------
FAKE_ROOT="$TMP/repo"
mkdir -p "$FAKE_ROOT/clients/integrations/one" "$FAKE_ROOT/clients/integrations/two"
printf 'module example.com/one\n\ngo 1.25\n' > "$FAKE_ROOT/clients/integrations/one/go.mod"
printf 'module example.com/two\n\ngo 1.25\n' > "$FAKE_ROOT/clients/integrations/two/go.mod"

MAP="$TMP/map.tsv"
cat > "$MAP" <<'MAPS'
example.com/one/...	prod	example.com/one
example.com/one/...	prod	github.com/msgmate-io/go-integration-interface/integrationinterface
example.com/one/...	test	backend/database
example.com/two/...	prod	example.com/two
example.com/two/...	prod	backend/database
MAPS
export GO_STUB_MAP="$MAP"

# --- case 1: --write-baseline records the surface (and still reports coupling)
BASELINE="$TMP/baseline.json"
run_surface --write-baseline "$BASELINE"
[ "$RC" -eq 1 ] || fail "--write-baseline with a coupled integration must exit 1 (got $RC): $OUT"
[ -f "$BASELINE" ] || fail "baseline file was not written"
grep -q '"example.com/one"' "$BASELINE" || fail "baseline must be keyed by module path: $(cat "$BASELINE")"
python3 - "$BASELINE" <<'PY' || fail "baseline content is wrong"
import json, sys
data = json.load(open(sys.argv[1]))
assert data["version"] == 1, data
one = data["integrations"]["example.com/one"]
assert one["packages"] == 2, one
assert one["backend"] == 0, one
two = data["integrations"]["example.com/two"]
assert two["packages"] == 2 and two["backend"] == 1, two
assert two["backend_packages"] == ["backend/database"], two
PY
grep -q "test_backend" "$BASELINE" || fail "baseline must record the test surface"

# --- case 2: the measured surface matches its own baseline ------------------
# `one` is decoupled; drop `two` from the fixture so the run can succeed.
grep -v '^example.com/two/' "$MAP" > "$TMP/map_clean.tsv"
export GO_STUB_MAP="$TMP/map_clean.tsv"
run_surface --write-baseline "$BASELINE"
[ "$RC" -eq 0 ] || fail "decoupled surface must exit 0 (got $RC): $OUT"
run_surface --baseline "$BASELINE"
[ "$RC" -eq 0 ] || fail "unchanged surface must not regress (got $RC): $OUT"
grep -q "no regression against the baseline" <<<"$OUT" || fail "missing no-regression line: $OUT"
grep -q "d_backend" <<<"$OUT" || fail "delta columns missing: $OUT"

# --- case 3: a backend/... import reappears ---------------------------------
cat >> "$TMP/map_clean.tsv" <<'MAPS'
example.com/one/...	prod	backend/server/util
MAPS
run_surface --baseline "$BASELINE"
[ "$RC" -eq 1 ] || fail "reappearing backend import must exit 1 (got $RC): $OUT"
grep -q "REGRESSIONS vs baseline" <<<"$OUT" || fail "regression header missing: $OUT"
grep -q "backend/server/util" <<<"$OUT" || fail "the new package must be named: $OUT"

# --- case 4: only the *new* backend/... package is reported ----------------
# The baseline already records backend/database for `one` (as a decoupled
# integration would not, but the check must still name just the newcomer).
cat > "$TMP/subset.json" <<'JSON'
{
  "version": 1,
  "integrations": {
    "example.com/one": {
      "directory": "one",
      "packages": 3,
      "backend": 1,
      "backend_packages": ["backend/database"],
      "test_packages": 3,
      "test_backend": 1
    }
  }
}
JSON
cat > "$TMP/map_subset.tsv" <<'MAPS'
example.com/one/...	prod	example.com/one
example.com/one/...	prod	backend/database
example.com/one/...	prod	backend/server/util
MAPS
export GO_STUB_MAP="$TMP/map_subset.tsv"
run_surface --baseline "$TMP/subset.json"
[ "$RC" -eq 1 ] || fail "a new backend package must exit 1 (got $RC): $OUT"
grep -q "backend/server/util" <<<"$OUT" || fail "the new package must be named: $OUT"
grep -q "backend/database" <<<"$OUT" && fail "the recorded package must not be reported: $OUT"
export GO_STUB_MAP="$MAP"

# --- case 5: test-only backend/... packages are not a regression -----------
# `two` has backend/database only in its test surface; with `two` back in the
# fixture its shipped graph is clean, so the run must succeed.
grep -v '^example.com/two/...	prod	backend/database$' "$MAP" > "$TMP/map_testonly.tsv"
export GO_STUB_MAP="$TMP/map_testonly.tsv"
run_surface --write-baseline "$TMP/testonly.json"
[ "$RC" -eq 0 ] || fail "test-only coupling must not fail the run (got $RC): $OUT"
grep -q "every integration is decoupled in its compiled artifact." <<<"$OUT" \
  || fail "decoupled line missing: $OUT"

# --- case 6: missing baseline ----------------------------------------------
run_surface --baseline "$TMP/does-not-exist.json"
[ "$RC" -eq 2 ] || fail "missing baseline must exit 2 (got $RC): $OUT"

# --- case 6b: an empty go list result is not evidence of decoupling --------
# A module that is absent from the workspace matches nothing; treating that as
# "no backend/... packages" would be a false green.
cat > "$TMP/map_empty.tsv" <<'MAPS'
example.com/one/...	test	backend/database
example.com/two/...	test	backend/database
MAPS
export GO_STUB_MAP="$TMP/map_empty.tsv"
run_surface
[ "$RC" -eq 1 ] || fail "an empty graph must exit 1 (got $RC): $OUT"
grep -q "no packages matched" <<<"$OUT" || fail "empty graph must be reported: $OUT"

# --- case 7: missing integrations directory --------------------------------
FAKE_ROOT="$TMP/empty"
mkdir -p "$FAKE_ROOT"
run_surface
[ "$RC" -eq 1 ] || fail "missing integrations dir must exit 1 (got $RC): $OUT"

# --- case 8: unmeasurable graph is reported, not silently dropped ----------
# If `go list` fails for every integration, the script must not claim a
# decoupled surface (it would be a false green).
FAKE_ROOT="$TMP/repo"
export GO_STUB_FAIL=1
run_surface
[ "$RC" -eq 1 ] || fail "no measurable module must exit 1 (got $RC): $OUT"
grep -q "go list failed" <<<"$OUT" || fail "stub failure must be reported: $OUT"
grep -q "no integration module could be measured" <<<"$OUT" || fail "must not claim decoupling: $OUT"
unset GO_STUB_FAIL

# --- case 9: --check-baseline validates a committed baseline ----------------
# The committed baseline must be checked without a Go toolchain, and it must not
# be allowed to pin an integration that is still coupled (that would neuter the
# gate). $TMP/testonly.json holds one decoupled integration.
run_surface --check-baseline "$TMP/testonly.json"
[ "$RC" -eq 0 ] || fail "a decoupled baseline must validate (got $RC): $OUT"
grep -q "ok (2 decoupled integration(s) pinned)" <<<"$OUT" || fail "missing ok line: $OUT"

run_surface --check-baseline "$TMP/subset.json"
[ "$RC" -eq 1 ] || fail "a baseline pinning a coupled integration must exit 1 (got $RC): $OUT"
grep -q "not decoupled" <<<"$OUT" || fail "coupled pin must be named: $OUT"

printf '{"version": 1, "integrations": {"example.com/x": {"backend": 2}}}' > "$TMP/malformed.json"
run_surface --check-baseline "$TMP/malformed.json"
[ "$RC" -eq 2 ] || fail "a malformed baseline must exit 2 (got $RC): $OUT"
grep -q "malformed baseline" <<<"$OUT" || fail "malformed baseline must be reported: $OUT"

printf '{"version": 9, "integrations": {}}' > "$TMP/wrong-version.json"
run_surface --check-baseline "$TMP/wrong-version.json"
[ "$RC" -eq 2 ] || fail "an unknown baseline version must exit 2 (got $RC): $OUT"

printf 'integration_dep_surface self-test: ok\n'
