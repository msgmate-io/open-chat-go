#!/usr/bin/env bash
# Resolve the absolute path an integration module is placed at, preserving the
# directory *structure* of the caller-supplied workspace path (ci#1075).
#
# Integration modules carry relative `replace` directives that only resolve in
# the monorepo layout, e.g. for `clients/integrations/<name>`:
#
#     replace backend => ../../../backend                        # -> <repo>/backend
#     replace github.com/msgmate-io/go-integration-interface => ../../go_integration_interface
#
# The depth of the module directory is therefore part of its build contract: a
# module placed at `<repo>/<name>` (one level below the root, which is what
# collapsing the path to its basename produced) resolves those replacements to
# paths that do not exist, so the build fails with
#
#     backend@v0.0.0: replacement directory ../../../backend does not exist
#
# Sourced by development/scripts/integration_module_ci.sh and pinned by
# development/scripts/tests/test_integration_workspace_path.sh.
#
# Deliberately no root-containment check: an absolute path is honoured verbatim
# so the hermetic self-tests can build in a mktemp directory.

integration_workspace_path() {
  # $1 = repository root (absolute), $2 = workspace path (relative or absolute)
  local repo_root="$1" workspace_path="$2"

  case "$workspace_path" in
    /*) ;;
    *) workspace_path="$repo_root/$workspace_path" ;;
  esac

  # Collapse . and .. segments and repeated slashes lexically: the target does
  # not exist yet, so `cd`/`realpath` cannot be used (and `cd` would also follow
  # symlinks, which the CI checkout re-placement must not do).
  local rest="$workspace_path" part out=""
  while [ -n "$rest" ]; do
    case "$rest" in
      */*) part="${rest%%/*}"; rest="${rest#*/}" ;;
      *) part="$rest"; rest="" ;;
    esac
    case "$part" in
      '' | .) ;;
      ..) out="${out%/*}" ;;
      *) out="$out/$part" ;;
    esac
  done
  printf '%s\n' "${out:-/}"
}