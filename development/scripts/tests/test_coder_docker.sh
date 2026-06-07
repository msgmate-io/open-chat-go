#!/usr/bin/env bash
# Unit tests for development/scripts/coder_docker.sh (ci#787).
#
# Pins the contract agents rely on instead of improvising raw docker commands:
#   * a missing CLI or an unreachable daemon is reported once with guidance and
#     exits 3 — never as a raw docker error, and never by installing a daemon;
#   * the command's own exit status survives the output capping;
#   * output is bounded by CODER_DOCKER_MAX_LINES so a chatty container cannot
#     flood the agent context;
#   * `run` always passes --rm;
#   * usage errors exit 2.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
wrapper="${script_dir}/../coder_docker.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The wrapper is invoked through an absolute bash path and only needs a handful
# of coreutils, so the test builds a controlled PATH: one variant without docker
# at all and one with a fake docker that behaves like a reachable daemon.
# Building the PATH instead of reusing the runner's PATH keeps the test
# independent of whether the CI image happens to ship a real docker CLI.
bash_bin="$(command -v bash)"
core_bin="$work/core-bin"
mkdir -p "$core_bin"
for tool in mktemp wc tr tail cat rm sed grep; do
  tool_path="$(command -v "$tool")"
  [ -n "$tool_path" ] || { echo "missing required coreutil: $tool" >&2; exit 1; }
  ln -sf "$tool_path" "$core_bin/$tool"
done

failures=0
case_no=0

ok() {
  case_no=$(( case_no + 1 ))
  printf 'ok %d - %s\n' "$case_no" "$1"
}

bad() {
  case_no=$(( case_no + 1 ))
  printf 'not ok %d - %s\n' "$case_no" "$1"
  failures=$(( failures + 1 ))
}

assert_eq() {
  local expected="$1" actual="$2" description="$3"
  if [ "$expected" = "$actual" ]; then
    ok "$description"
  else
    bad "$description (expected=$expected actual=$actual)"
  fi
}

assert_contains() {
  local haystack="$1" needle="$2" description="$3"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    ok "$description"
  else
    bad "$description (missing: $needle)"
  fi
}

# A PATH with no docker at all.
empty_path="$core_bin"

# A fake docker that behaves like a reachable daemon. Its shebang uses the
# absolute bash path on purpose: the controlled PATH has no `env`, so a
# `#!/usr/bin/env bash` stub would fail to start and look like a dead daemon.
docker_bin="$work/bin"
mkdir -p "$docker_bin"
cat >"$docker_bin/docker" <<STUB
#!$bash_bin
printf 'args: %s\n' "\$*" >>"\${FAKE_DOCKER_LOG:-/dev/null}"
case "\$1" in
  info) exit "\${FAKE_DOCKER_INFO_RC:-0}" ;;
  version) echo "27.0.0" ;;
  ps)
    i=0
    while [ "\$i" -lt "\${FAKE_DOCKER_PS_LINES:-3}" ]; do
      echo "container-\$i"
      i=\$(( i + 1 ))
    done
    ;;
  logs) echo "log line" ;;
  build|run|exec|compose) echo "ran: \$*"; exit "\${FAKE_DOCKER_RUN_RC:-0}" ;;
esac
STUB
chmod +x "$docker_bin/docker"

# The reachable-daemon PATH needs the coreutils too, otherwise the wrapper's
# output capping would fail with 127 before docker is even reached.
docker_path="$docker_bin:$core_bin"

run_wrapper() {
  local path="$1" max_lines="$2"
  shift 2
  set +e
  output="$(PATH="$path" CODER_DOCKER_MAX_LINES="$max_lines" "$bash_bin" "$wrapper" "$@" 2>&1)"
  rc=$?
  set -e
}

run_raw() {
  # run_raw <path> <args...> with extra env supplied by the caller.
  local path="$1"
  shift
  set +e
  PATH="$path" "$bash_bin" "$wrapper" "$@" >/dev/null 2>&1
  rc=$?
  set -e
}

# 1. No docker CLI: guidance, exit 3, no daemon installation suggestion.
run_wrapper "$empty_path" 200 check
assert_eq "3" "$rc" "check without a docker CLI exits 3"
assert_contains "$output" "no docker CLI on PATH" "check names the missing CLI"
assert_contains "$output" "Do not install a daemon here" "check forbids installing a daemon"
assert_contains "$output" "PR CI" "check points at PR CI as the gate"

# 2. CLI present but daemon unreachable: still exit 3, still no workaround.
set +e
output="$(PATH="$docker_path" FAKE_DOCKER_INFO_RC=1 "$bash_bin" "$wrapper" check 2>&1)"
rc=$?
set -e
assert_eq "3" "$rc" "check with an unreachable daemon exits 3"
assert_contains "$output" "no daemon is reachable" "check names the unreachable daemon"
assert_contains "$output" "Do not start a daemon" "check forbids starting a daemon"

# 3. Reachable daemon: exit 0 and the server version is surfaced.
run_wrapper "$docker_path" 200 check
assert_eq "0" "$rc" "check with a reachable daemon exits 0"
assert_contains "$output" "server: 27.0.0" "check reports the server version"

# 4. Output capping: more lines than the limit collapse to the limit plus marker.
set +e
output="$(PATH="$docker_path" CODER_DOCKER_MAX_LINES=5 FAKE_DOCKER_PS_LINES=50 "$bash_bin" "$wrapper" ps 2>&1)"
rc=$?
set -e
assert_eq "0" "$rc" "ps succeeds with a reachable daemon"
lines="$(printf '%s\n' "$output" | wc -l | tr -d ' ')"
assert_eq "6" "$lines" "ps output is capped to the limit plus the trim marker"
assert_contains "$output" "lines trimmed" "ps reports how much was trimmed"

# 5. A failing docker command keeps its own exit status (not tail's).
set +e
PATH="$docker_path" FAKE_DOCKER_RUN_RC=7 "$bash_bin" "$wrapper" run alpine -- echo hi >/dev/null 2>&1
rc=$?
set -e
assert_eq "7" "$rc" "run propagates the docker exit status"

# 6. run always passes --rm.
: >"$work/run.log"
set +e
PATH="$docker_path" FAKE_DOCKER_LOG="$work/run.log" "$bash_bin" "$wrapper" run alpine -- echo hi >/dev/null 2>&1
set -e
assert_contains "$(cat "$work/run.log")" "run --rm alpine echo hi" "run defaults to --rm"

# 7. Usage errors exit 2.
run_raw "$docker_path"
assert_eq "2" "$rc" "no subcommand is a usage error"

run_raw "$docker_path" frobnicate
assert_eq "2" "$rc" "an unknown subcommand is a usage error"

run_raw "$docker_path" compose /tmp bogus
assert_eq "2" "$rc" "an unsupported compose action is a usage error"

run_raw "$docker_path" build only-tag
assert_eq "2" "$rc" "build without a context is a usage error"

# 8. The wrapper must never grow a "start the daemon" escape hatch.
for forbidden in "dockerd" "service docker start" "systemctl start docker"; do
  if grep -qF -- "$forbidden" "$wrapper"; then
    bad "wrapper must not contain '$forbidden'"
  else
    ok "wrapper contains no '$forbidden' escape hatch"
  fi
done

# 9. The wrapper must stay in sync with the typed tool registry (ci#787): every
#    docker_sandbox_* tool has a wrapper subcommand and vice versa.
registry="${script_dir}/../../../backend/nativecoder/dockertooling.go"
if [ -f "$registry" ]; then
  registry_tools="$(grep -o 'DockerTool[A-Za-z]* *= *"[a-z_]*"' "$registry" | sed 's/.*"\(.*\)"/\1/' | sort -u | grep -v '^docker_sandbox_$')"
  for tool in $registry_tools; do
    subcommand="${tool#docker_sandbox_}"
    case "$subcommand" in
      check|ps|logs|build|run|compose|exec)
        if grep -qE "^  ${subcommand}\)" "$wrapper"; then
          ok "registry tool $tool has a wrapper subcommand"
        else
          bad "registry tool $tool has no wrapper subcommand"
        fi
        ;;
      *)
        bad "registry tool $tool has no known wrapper subcommand mapping"
        ;;
    esac
  done
else
  bad "docker tooling registry not found at $registry"
fi

printf '\n%d checks, %d failures\n' "$case_no" "$failures"
[ "$failures" -eq 0 ]
