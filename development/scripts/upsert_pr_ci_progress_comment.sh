#!/usr/bin/env bash
# ci#745: single owner of the PR-CI progress comment.
#
# Upserts the bot's `<!-- open-chat-pr-ci-progress -->` comment on a pull request:
# it looks the comment up by marker and PATCHes it, otherwise it POSTs a new one.
# That keeps the "one progress comment per PR" contract that the three inline curl
# blocks in `.github/workflows/run-pr-ci-tests-and-deployments.yaml` used to
# duplicate.
#
# Usage:
#   upsert_pr_ci_progress_comment.sh --repo <owner/name> --pr <number> \
#     --body-file <path> --token <token> [--bot-user <login>] [--marker <marker>]
#
# The comment target must be complete: with an empty repository or PR number the
# helper logs a warning and exits 0, so a skipped upstream job can never turn the
# calling step red with `repos//issues//comments` -> HTTP 404 (curl exit 22).
set -euo pipefail

MARKER='<!-- open-chat-pr-ci-progress -->'
BOT_USER='cur1ousdude'
REPO=''
PR_NUMBER=''
BODY_FILE=''
TOKEN=''

usage() {
  cat <<'USAGE'
Usage: upsert_pr_ci_progress_comment.sh --repo <owner/name> --pr <number> \
         --body-file <path> --token <token> [--bot-user <login>] [--marker <marker>]

Upserts the bot's progress comment (identified by --marker) on the given PR.
An incomplete --repo/--pr target warns and exits 0 without calling the API.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) REPO="${2:-}"; shift 2 ;;
    --pr) PR_NUMBER="${2:-}"; shift 2 ;;
    --body-file) BODY_FILE="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --bot-user) BOT_USER="${2:-}"; shift 2 ;;
    --marker) MARKER="${2:-}"; shift 2 ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "::error::upsert_pr_ci_progress_comment: unknown argument '$1'." >&2
      usage >&2
      exit 2
      ;;
  esac
done

# ci#745 fail-safe: never build a URL from an empty target.
if [ -z "${REPO}" ] || [ -z "${PR_NUMBER}" ]; then
  echo "::warning::Skipping progress comment: repository='${REPO}' pr_number='${PR_NUMBER}' are empty."
  exit 0
fi

if [ -z "${TOKEN}" ]; then
  echo "::error::upsert_pr_ci_progress_comment: --token is required." >&2
  exit 2
fi

if [ ! -f "${BODY_FILE}" ]; then
  echo "::error::upsert_pr_ci_progress_comment: body file '${BODY_FILE}' does not exist." >&2
  exit 2
fi

payload="$(jq -n --rawfile body "${BODY_FILE}" '{body: $body}')"

comment_id="$(
  curl -fsSL \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${REPO}/issues/${PR_NUMBER}/comments" \
    | jq -r --arg bot_user "${BOT_USER}" --arg marker "${MARKER}" \
      '[.[] | select(.user.login == $bot_user and (.body | contains($marker)))][0].id // ""'
)"

if [ -n "${comment_id}" ]; then
  echo "Updating progress comment ${comment_id} on ${REPO}#${PR_NUMBER}."
  curl -fsSL -X PATCH \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${REPO}/issues/comments/${comment_id}" \
    -d "${payload}" >/dev/null
else
  echo "Creating progress comment on ${REPO}#${PR_NUMBER}."
  curl -fsSL -X POST \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${REPO}/issues/${PR_NUMBER}/comments" \
    -d "${payload}" >/dev/null
fi
