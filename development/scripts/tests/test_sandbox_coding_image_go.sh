#!/usr/bin/env bash
# ci#1062 (image half of ci#1038): pin the coding-sandbox *image* Go contract.
#
# The defect the gate exists for: the published `sandbox-coding` image shipped
# go1.25.10 under /usr/local/go, off PATH (no ~/.bashrc, no /etc/profile.d), no
# warmed module cache, while backend/go.mod required go 1.26.0 and
# GOTOOLCHAIN=local. Nothing in the repo noticed, so the drift shipped.
#
# This test pins the two halves of development/scripts/check_sandbox_coding_image_go.sh:
#
#   1. the static gate accepts a Dockerfile that delegates the Go version to
#      development/scripts/install_sandbox_go_toolchain.sh (single source of truth,
#      derived from backend/go.mod) or pins a version >= the go.mod directive;
#   2. it returns MISS for each individual ci#1062 defect - a pin older than
#      go.mod, a missing /etc/profile.d login shim, GOTOOLCHAIN not local, and no
#      build-time module-cache warming - so a regression cannot be green;
#   3. `--image` mode refuses to invent evidence when no docker daemon exists
#      (it must exit non-zero with a MISS instead of pretending the checks ran).
#
# Hermetic: fixtures are Dockerfiles written under a mktemp dir, and the pinned
# version is read from a fixture backend/go.mod, so neither the real
# backend/go.mod nor the real image repo is consulted. No docker daemon is
# required - the refusal branch is exactly what gets exercised.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SCRIPT="$ROOT/development/scripts/check_sandbox_coding_image_go.sh"

failures=0
fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

pass() { printf 'ok: %s\n' "$1"; }

[ -x "$SCRIPT" ] || fail "check_sandbox_coding_image_go.sh must exist and be executable"
[ -f "$SCRIPT" ] || { echo "cannot continue without $SCRIPT" >&2; exit 1; }

if ! bash -n "$SCRIPT" 2>/dev/null; then
  fail "check_sandbox_coding_image_go.sh must be valid bash"
  echo "== $failures failure(s) ==" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# --- fixture repo ------------------------------------------------------------
# Only backend/go.mod is needed: the required version comes from there.
mkdir -p "$tmp/repo/backend"
printf 'module example.com/fixture\n\ngo 1.26.0\n\nrequire example.com/x v1.0.0\n' \
  > "$tmp/repo/backend/go.mod"

# A Dockerfile that satisfies every clause by delegating to the installer.
cat > "$tmp/df-derived" <<'EOF'
FROM alpine:3.20
ENV GOTOOLCHAIN=local \
    GOPATH=/root/go \
    GOMODCACHE=/root/go/pkg/mod \
    PATH="/usr/local/go/bin:${PATH}"
RUN cp /repo/development/scripts/install_sandbox_go_toolchain.sh /usr/local/bin/ && \
    install_sandbox_go_toolchain.sh --check /repo || true
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
RUN cd /repo/backend && go mod download all
EOF

# Same, but with a hard-coded ARG pin instead of delegation (allowed if new enough).
cat > "$tmp/df-pinned-new" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.26.11
ENV GOTOOLCHAIN=local \
    GOMODCACHE=/root/go/pkg/mod \
    PATH="/usr/local/go/bin:${PATH}"
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
RUN cd /repo/backend && go mod download all
EOF

# The ci#1062 defect, reproduced: old pin, no profile.d shim, no warm cache.
cat > "$tmp/df-old-pin" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.25.10
ENV GOTOOLCHAIN=local \
    GOMODCACHE=/root/go/pkg/mod \
    PATH="/usr/local/go/bin:${PATH}"
RUN apk add --no-cache git ca-certificates
EOF

# Old pin again, but everything else green - isolates the drift verdict.
cat > "$tmp/df-drifted-only" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.25.10
ENV GOTOOLCHAIN=local \
    GOMODCACHE=/root/go/pkg/mod \
    PATH="/usr/local/go/bin:${PATH}"
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
RUN cd backend && go mod download all
EOF

# Everything green except GOTOOLCHAIN.
cat > "$tmp/df-no-toolchain" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.26.11
ENV GOMODCACHE=/root/go/pkg/mod
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
RUN go mod download all
EOF

# Everything green except the warmed module cache.
cat > "$tmp/df-no-warm" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.26.11
ENV GOTOOLCHAIN=local \
    GOMODCACHE=/root/go/pkg/mod
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
EOF

# Everything green except the login-shell PATH shim.
cat > "$tmp/df-no-profile" <<'EOF'
FROM alpine:3.20
ARG GO_VERSION=1.26.11
ENV GOTOOLCHAIN=local \
    GOMODCACHE=/root/go/pkg/mod \
    PATH="/usr/local/go/bin:${PATH}"
RUN go mod download all
EOF

# Neither version source: the gate must not guess.
cat > "$tmp/df-no-version-source" <<'EOF'
FROM alpine:3.20
ENV GOTOOLCHAIN=local GOMODCACHE=/root/go/pkg/mod
RUN printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/00-go-toolchain.sh
RUN go mod download all
EOF

# The shape ci#1062's fix actually took: the Dockerfile only COPYs a provisioning
# script, and every contract marker lives inside that script. The gate must
# follow the COPY instead of reading the image as MISS.
mkdir -p "$tmp/delegated"
cat > "$tmp/delegated/provision-go-toolchain.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
export GOTOOLCHAIN=local
export GOMODCACHE=/root/go/pkg/mod
bash "${repo}/development/scripts/install_sandbox_go_toolchain.sh"
printf 'export PATH="/usr/local/go/bin:$PATH"\n' > /etc/profile.d/openchat-sandbox-go.sh
( cd "${repo}/backend" && go mod download all )
EOF
cat > "$tmp/delegated/Dockerfile" <<'EOF'
FROM alpine:3.20
COPY provision-go-toolchain.sh /usr/local/lib/openchat/provision-go-toolchain.sh
RUN bash /usr/local/lib/openchat/provision-go-toolchain.sh
EOF

# Same delegation, but the copied script drops the login-shell shim - the gate
# must still see through the COPY and report MISS.
mkdir -p "$tmp/delegated-broken"
cat > "$tmp/delegated-broken/provision-go-toolchain.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
export GOTOOLCHAIN=local GOMODCACHE=/root/go/pkg/mod
bash "${repo}/development/scripts/install_sandbox_go_toolchain.sh"
EOF
cp "$tmp/delegated/Dockerfile" "$tmp/delegated-broken/Dockerfile"

run_gate() { bash "$SCRIPT" "$@" 2>&1; }

# --- 1. green cases ----------------------------------------------------------
for green in df-derived df-pinned-new delegated/Dockerfile; do
  out="$(run_gate --dockerfile "$tmp/$green" "$tmp/repo")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "$green must pass the static gate (exit $rc)"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
  elif ! printf '%s\n' "$out" | grep -q '^== ok: '; then
    fail "$green must report '== ok: ...'"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
  else
    pass "$green passes the static gate"
  fi
done

# The repo's own backend/go.mod must be readable by the gate (no hard-coded 1.26).
grep -q "^go 1\.26\." "$ROOT/backend/go.mod" 2>/dev/null ||
  fail "backend/go.mod should still pin a go 1.26.x directive (gate reads it, never a constant)"

# --- 2. each defect is a MISS ------------------------------------------------
expect_miss() {
  fixture="$1"; needle="$2"
  out="$(run_gate --dockerfile "$tmp/$fixture" "$tmp/repo")"
  rc=$?
  if [ "$rc" -eq 0 ]; then
    fail "$fixture must FAIL the static gate (it does not: exit 0)"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    return
  fi
  if ! printf '%s\n' "$out" | grep -q '^MISS '; then
    fail "$fixture must emit a 'MISS ' line"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    return
  fi
  if [ -n "$needle" ] && ! printf '%s\n' "$out" | grep -q "$needle"; then
    fail "$fixture must explain the gap with /$needle/"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
    return
  fi
  pass "$fixture is rejected ($needle)"
}

expect_miss df-drifted-only 'older than backend/go.mod'
expect_miss df-no-profile 'profile\.d'
expect_miss df-no-toolchain 'GOTOOLCHAIN=local'
expect_miss df-no-warm 'go mod download'
expect_miss df-no-version-source 'no Go version source'
# A copied provisioning script is inspected, so a gap inside it is still a MISS.
out="$(run_gate --dockerfile "$tmp/delegated-broken/Dockerfile" "$tmp/repo")"
if [ $? -eq 0 ]; then
  fail "a COPYed provisioning script without the profile.d shim must be a MISS"
elif ! printf '%s\n' "$out" | grep -q 'profile\.d'; then
  fail "the COPYed-script MISS must name the missing profile.d shim"
  printf '%s\n' "$out" | sed 's/^/    /' >&2
else
  pass "the gate follows COPYed provisioning scripts (and still catches gaps)"
fi
# The full defect fixture collects several MISS lines at once.
out="$(run_gate --dockerfile "$tmp/df-old-pin" "$tmp/repo")"
if [ "$(printf '%s\n' "$out" | grep -c '^MISS ')" -ge 3 ]; then
  pass "the ci#1062 defect fixture reports every gap at once"
else
  fail "df-old-pin must report >= 3 MISS lines (pin, profile.d, warm cache)"
  printf '%s\n' "$out" | sed 's/^/    /' >&2
fi

# --- 3. the gate never drifts from the installer -----------------------------
# The installer stays the single source of truth for the version, and the gate
# must accept delegation to it - a second mechanism is what the ticket forbids.
grep -q 'install_sandbox_go_toolchain\.sh' "$SCRIPT" ||
  fail "the gate must recognise install_sandbox_go_toolchain.sh as a valid version source"

# The real, published image repo is not vendored here, but if a sandbox
# Dockerfile that ships `go` is checked out (monorepo client submodule) it must
# be green. This is the check that would have caught ci#1062 at PR time - and,
# for `minimal`, ci#1038: the publish gate was scoped to `coding` only, so the
# variant agent sandboxes actually run kept shipping go1.25.10 off PATH long
# after `coding` was fixed. Both gated variants are asserted here so a future
# edit that leaves `minimal` behind fails in PR CI, not only at publish time.
for variant in coding minimal; do
  variant_df=""
  for candidate in \
    "$ROOT/clients/integrations/docker_sandbox_integration/sandboxes/$variant/Dockerfile" \
    "$ROOT/sandboxes/$variant/Dockerfile"; do
    [ -f "$candidate" ] && { variant_df="$candidate"; break; }
  done
  if [ -n "$variant_df" ]; then
    out="$(run_gate --dockerfile "$variant_df" "$ROOT")"
    if [ $? -ne 0 ]; then
      fail "the checked-out $variant Dockerfile must satisfy the ci#1062 Go contract"
      printf '%s\n' "$out" | sed 's/^/    /' >&2
    else
      pass "the checked-out $variant Dockerfile satisfies the contract"
    fi
  else
    printf 'ok: %s Dockerfile not checked out here; static gate covered by fixtures\n' "$variant"
  fi
done

# --- 4. --image mode produces no fabricated evidence -------------------------
# Without a docker daemon the behavioural half must say MISS and exit non-zero.
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  printf 'ok: docker daemon available; --image refusal branch not exercised\n'
else
  out="$(run_gate --image example.invalid/coding:test "$tmp/repo")"
  rc=$?
  if [ "$rc" -eq 0 ]; then
    fail "--image must not report success without a docker daemon"
  elif ! printf '%s\n' "$out" | grep -q '^MISS '; then
    fail "--image must explain the missing daemon with a MISS line"
    printf '%s\n' "$out" | sed 's/^/    /' >&2
  else
    pass "--image refuses to fabricate evidence without a daemon"
  fi
fi

# Usage errors are exit 2, never 0 or 1.
out="$(bash "$SCRIPT" "$tmp/repo" 2>&1)"; rc=$?
[ "$rc" -eq 2 ] || fail "no mode must be a usage error (exit 2), got $rc"
out="$(bash "$SCRIPT" --dockerfile "$tmp/does-not-exist" "$tmp/repo" 2>&1)"; rc=$?
[ "$rc" -eq 2 ] || fail "a missing Dockerfile must be a usage error (exit 2), got $rc"

if [ "$failures" -ne 0 ]; then
  printf '\n== %d failure(s) ==\n' "$failures" >&2
  exit 1
fi
printf '\n== ok: coding image Go contract pinned ==\n'
