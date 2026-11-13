#!/usr/bin/env bash
# Print the open-chat-go-ci leaf issues a PR body links (ci#813, ci#824, ci#1034).
#
# The gate (`PR Test Merge Requirement`) and the post-merge closure job
# (`close-linked-ci-issues`) both need the *same* answer to "which leaf issues
# does this body close?", and they used to carry two copies of one anchored
# `grep -oiE '^...$'`. Anchoring is what keeps prose ("Fix ci#824: crash on
# empty config", "Closes ci#824 and also ci#999") from importing issues the
# author never linked, but it also rejected the very same line wrapped in
# ordinary markdown - a list bullet (`- Closes ...`), bold (`**Closes ...**`),
# a table cell (`| Closes ... |`) or a trailing full stop. One shared helper
# makes both call sites agree and lets a self-test pin the rules:
# `development/scripts/tests/test_merge_gate_verdict.sh`.
#
# Usage: linked_ci_issues.sh <pr-body.md>
# Output: the linked issue numbers, one per line, numerically sorted, unique.
set -euo pipefail

body_file="${1:-}"
if [ -z "${body_file}" ] || [ ! -f "${body_file}" ]; then
  echo "usage: linked_ci_issues.sh <pr-body.md>" >&2
  exit 2
fi

# One peel pass removes a single layer of markdown wrapper: a block/list marker
# (`- `, `* `, `1. `, `> `), leading/trailing emphasis or code markers, and
# trailing sentence punctuation. A wrapped reference like
# `- **Closes msgmate-io/open-chat-go-ci#1033**.` needs both layers gone, so
# the pass is applied a fixed number of times (idempotent once clean).
peel() {
  sed -E \
    -e 's/^[[:space:]]*([-*+]|[0-9]+[.)]|>)[[:space:]]+//' \
    -e 's/^[[:space:]]*([*_`]+[[:space:]]*)+//' \
    -e 's/([[:space:]]*[*_`]+)+[[:space:]]*$//' \
    -e 's/[[:space:]]+$//' \
    -e 's/[[:space:]]*[.,;:!]+$//' \
    -e 's/^[[:space:]]+//'
}

# Table rows are split into their own cells first, so a reference occupying a
# single cell (`| Closes ... | yes |`) counts, while a cell holding prose
# around the reference still fails the standalone anchor below. Only real rows
# (lines starting with a pipe) are split, so prose that merely contains a pipe
# is not silently cut into fragments.
sed -E '/^[[:space:]]*\|/ s/\|/\n/g' "${body_file}" \
  | peel | peel | peel \
  | { grep -oiE '^(close[sd]?|fix(e[sd])?|resolve[sd]?):?[[:space:]]+(msgmate-io/open-chat-go-ci#|open-chat-go-ci#|ci#)[0-9]+$' || true; } \
  | sed -E 's/.*#([0-9]+)$/\1/' \
  | sort -n -u
