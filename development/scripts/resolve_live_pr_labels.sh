#!/usr/bin/env bash
# Resolve the pull request's *live* label set into a file (ci#659).
#
# Label-driven CI decisions must not read `$GITHUB_EVENT_PATH`. `gh pr create
# -l a -l b ...` applies the labels in a second API call, and a burst of label
# edits produces one webhook payload per edit, so the `opened` payload (and
# every partial `labeled` payload) can carry an empty or incomplete label set.
# A check that decides from that snapshot is wrong for exactly the PRs the
# checks exist to gate: `PR Test Merge Requirement` reported "PR is missing the
# required test label." on the r23 consolidation PR even though it was created
# with `test`.
#
# Usage:
#   resolve-live-pr-labels.sh <output-file> [<pr-number>]
#
# Reads the labels from the REST API using LABEL_TOKEN (falling back to
# GITHUB_TOKEN) and writes one label name per line, sorted, to <output-file>.
# Exits 0 with an empty file for non-pull-request events so callers can use it
# unconditionally. Never prints the token; only label names are echoed.
#
# The PR number comes from the argument, then from GITHUB_EVENT_PATH when the
# event carries one (a pull_request payload always does). The event file is
# only ever used to *locate* the PR, never to read its labels.
#
# NOTE (ci#659 follow-up): label-driven jobs that run BEFORE `actions/checkout`
# cannot call this script, so they inline the same curl+jq call in their first
# step. Keep the two in sync; this file is the canonical implementation for
# steps that run after a checkout.
set -euo pipefail

OUT="${1:?usage: resolve-live-pr-labels.sh <output-file> [<pr-number>]}"
PR_NUMBER="${2:-}"

if [ -z "$PR_NUMBER" ]; then
  if [ -n "${GITHUB_EVENT_PATH:-}" ] && [ -f "${GITHUB_EVENT_PATH}" ]; then
    PR_NUMBER="$(jq -r '.pull_request.number // .issue.number // ""' "$GITHUB_EVENT_PATH" 2>/dev/null || true)"
  fi
fi

if [ -z "$PR_NUMBER" ]; then
  echo "resolve-live-pr-labels: not a pull-request event; writing an empty label set."
  : > "$OUT"
  exit 0
fi

TOKEN="${LABEL_TOKEN:-${GITHUB_TOKEN:-}}"
if [ -z "$TOKEN" ]; then
  echo "::error::resolve-live-pr-labels: no API token available (LABEL_TOKEN/GITHUB_TOKEN)."
  exit 1
fi

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "$REPO" ]; then
  echo "::error::resolve-live-pr-labels: GITHUB_REPOSITORY is not set."
  exit 1
fi

curl -fsSL \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  "https://api.github.com/repos/${REPO}/issues/${PR_NUMBER}/labels?per_page=100" \
  | jq -r '.[].name' | sort > "$OUT"

echo "Live PR labels (${PR_NUMBER}): $(tr '\n' ' ' < "$OUT")"
