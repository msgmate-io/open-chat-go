#!/usr/bin/env bash
# ci#1075: explain a misplaced integration module instead of leaking Go's opaque
# `replacement directory ... does not exist` error.
#
# An integration `go.mod` is not standalone: it reaches into the open-chat
# monorepo layout through relative `replace` directives
#
#     replace backend                                          => ../../../backend
#     replace github.com/msgmate-io/go-integration-interface   => ../../go_integration_interface
#     replace github.com/msgmate-io/go-client-integration     => ../go_client_integration
#
# Those only resolve while the module sits at `<workspace>/clients/integrations/<name>`.
# When development/scripts/integration_module_ci.sh collapsed that path to its
# basename (the ci#1075 bug) *every* target missed and the only signal was
#
#     backend@v0.0.0: replacement directory ../../../backend does not exist
#
# which pointed at go.mod instead of at the placement. These helpers make the
# layout itself observable: they enumerate the relative replace targets and
# report the ones that do not exist below the module directory, plus a hint that
# names the expected location. They never fail the build on their own - a stale
# entry may simply be unused - so wiring them in cannot turn a passing module
# red; the caller decides how loud to be.
#
# Pinned hermetically (no Go toolchain, no network) by
# development/scripts/tests/test_integration_module_path_guard.sh.

# integration_module_relative_replaces <go.mod>
#   Print the relative targets of `replace` directives in file order, one per
#   line. Both the single-line form (`replace a => ../b`) and the block form
#   (`replace (` ... `)`) are understood; absolute/version-only replacements
#   (`replace a v1 => b v2`) are ignored because they cannot depend on depth.
integration_module_relative_replaces() {
  local go_mod="${1:?integration_module_relative_replaces needs a go.mod path}"
  [ -f "$go_mod" ] || return 0
  awk '
    # `replace (` opens a block; `)` inside it closes the block again.
    /^[[:space:]]*replace[[:space:]]*\(/ { in_block = 1; next }
    in_block && /^[[:space:]]*\)/        { in_block = 0; next }
    {
      target = ""
      if (in_block && $2 == "=>")       target = $3
      else if (!in_block && $1 == "replace" && $3 == "=>") target = $4
      if (target ~ /^\.\.?\//) print target
    }
  ' "$go_mod"
}

# integration_module_missing_replaces <module dir>
#   Print the relative replace targets that do not exist below <module dir>.
#   Empty output means the layout satisfies every relative replacement.
integration_module_missing_replaces() {
  local dir="${1:?integration_module_missing_replaces needs a module directory}"
  local target=""
  [ -d "$dir" ] || return 0
  while IFS= read -r target; do
    [ -n "$target" ] || continue
    [ -e "$dir/$target" ] || printf '%s\n' "$target"
  done < <(integration_module_relative_replaces "$dir/go.mod")
}

# integration_module_layout_hint <module dir>
#   Print the actionable explanation for a module whose relative replaces miss.
integration_module_layout_hint() {
  local dir="${1:?integration_module_layout_hint needs a module directory}"
  cat <<HINT
the integration module at $dir must be built at <workspace>/clients/integrations/<name>:
its go.mod relative \`replace\` targets reach the monorepo layout (backend/,
go_integration_interface/, the sibling integrations), so a collapsed path
(<workspace>/<name>) - or any other depth - makes all of them miss and Go reports
it as "replacement directory ... does not exist" (ci#1075).
HINT
}
