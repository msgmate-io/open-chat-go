#!/usr/bin/env bash
# ci#1055 - close the `dev -> main` promotion hop, then verify the deployment.
#
# Merging the `[Dev promotion]` PR is what deploys ci.msgmate.io, so while that
# merge stayed a maintainer click nothing reached production and every feature
# ticket sat as "implemented but not live". The lead may merge it once all
# required checks on it are green, and must then verify the deployment. This
# wrapper only *collects observations*; every rule lives in `promotion_hop.py`,
# which is unit-tested offline (`test_promotion_hop.py`, run by PR CI).
#
#   ./development/scripts/promotion_hop.sh merge            # 0 merge, 1 wait, 2 refuse, 3 nothing to promote
#   ./development/scripts/promotion_hop.sh merge --apply    # ... and merge it when green
#   ./development/scripts/promotion_hop.sh deploy <VERSION>  # 0 healthy, 1 broken
#
# Env (never echoed):
#   GH_TOKEN / PR_TOKEN  bot token for the GitHub API and `gh`
#   REPOSITORY           default msgmate-io/open-chat-go
#   PROMOTION_PR         explicit PR number (else discovered: open, base=main, head=dev)
#   NAMESPACE, DEPLOYMENT  where ci.msgmate.io runs (default ci / open-chat-backend)
#   HEALTH_URL           default https://ci.msgmate.io/api/v1/metrics/health
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:-merge}"
shift || true

apply=false
expected_version=""
for arg in "$@"; do
  case "$arg" in
    --apply) apply=true ;;
    -*) echo "unknown option ${arg}" >&2; exit 64 ;;
    *) expected_version="$arg" ;;
  esac
done

REPOSITORY="${REPOSITORY:-msgmate-io/open-chat-go}"
HEALTH_URL="${HEALTH_URL:-https://ci.msgmate.io/api/v1/metrics/health}"
NAMESPACE="${NAMESPACE:-ci}"
DEPLOYMENT="${DEPLOYMENT:-open-chat-backend}"
token="${GH_TOKEN:-${PR_TOKEN:-}}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
observations="${workdir}/observations.json"

curl_auth=()
if [ -n "$token" ]; then
  curl_auth=(-H "Authorization: Bearer ${token}")
fi

api() { # api <path> [curl args...] -> JSON on stdout
  local path="$1"
  shift || true
  curl -fsSL "${curl_auth[@]+"${curl_auth[@]}"}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "$@" \
    "${GITHUB_API_URL:-https://api.github.com}/repos/${REPOSITORY}${path}"
}

collect_merge() {
  local pr_number="${PROMOTION_PR:-}"
  if [ -z "$pr_number" ]; then
    pr_number="$(api "/pulls?state=open&base=main&per_page=50" \
      | jq -r '[.[] | select(.head.ref == "dev")][0].number // ""')"
  fi
  if [ -z "$pr_number" ]; then
    echo "no open dev -> main promotion PR: the hop is already complete" >&2
    exit 3
  fi
  printf 'promotion PR: %s\n' "$pr_number" >&2
  printf '%s' "$pr_number" > "${workdir}/pr_number"

  api "/pulls/${pr_number}" > "${workdir}/pr.json"
  local sha
  sha="$(jq -r '.head.sha' "${workdir}/pr.json")"
  printf '%s' "$sha" > "${workdir}/head_sha"
  api "/commits/${sha}/check-runs?per_page=100" > "${workdir}/checks.json"

  # Only the contexts `main` protection actually requires can veto the hop.
  if ! api "/branches/main/protection" 2>/dev/null \
    | jq -r '[.required_status_checks.contexts[]?] | .[]' > "${workdir}/required.txt"; then
    : > "${workdir}/required.txt"
    printf 'branch protection unreadable; falling back to every check on the commit\n' >&2
  fi

  jq -n \
    --slurpfile pr "${workdir}/pr.json" \
    --slurpfile checks "${workdir}/checks.json" \
    --rawfile required "${workdir}/required.txt" \
    '{
      pull_request: {
        title: $pr[0].title,
        head_ref: $pr[0].head.ref,
        base_ref: $pr[0].base.ref,
        draft: $pr[0].draft,
        mergeable: $pr[0].mergeable,
        merge_state_status: ($pr[0].mergeable_state // ""),
        head_sha: $pr[0].head.sha
      },
      required_checks: ($required | split("\n") | map(select(length > 0))),
      check_runs: ($checks[0].check_runs // [])
    }' > "$observations"
}

collect_deploy() {
  local rollout_complete=false rollout_status="" ready=0 desired=0
  if command -v kubectl >/dev/null 2>&1; then
    rollout_status="$(kubectl -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT}" --timeout=180s 2>&1 || true)"
    case "$rollout_status" in
      *"successfully rolled out"*) rollout_complete=true ;;
    esac
    ready="$(kubectl -n "$NAMESPACE" get "deployment/${DEPLOYMENT}" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
    desired="$(kubectl -n "$NAMESPACE" get "deployment/${DEPLOYMENT}" -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
  else
    rollout_status="kubectl unavailable: rollout state not observable, so the deployment cannot count as verified"
  fi

  local response code body
  response="$(curl -sSL -w '\n%{http_code}' "$HEALTH_URL" 2>&1 || true)"
  code="$(printf '%s' "$response" | tail -n1)"
  body="$(printf '%s' "$response" | sed '$d')"

  jq -n \
    --argjson rollout_complete "$rollout_complete" \
    --arg rollout_status "$rollout_status" \
    --argjson ready "${ready:-0}" \
    --argjson desired "${desired:-0}" \
    --arg expected_version "$expected_version" \
    --arg url "$HEALTH_URL" \
    --argjson code "${code:-0}" \
    --arg body "$body" \
    '{
      rollout_complete: $rollout_complete,
      rollout_status: $rollout_status,
      pods_ready: $ready,
      pods_desired: $desired,
      expected_version: $expected_version,
      health: {url: $url, status_code: $code, body: $body}
    }' > "$observations"
}

case "$mode" in
  merge) collect_merge ;;
  deploy) collect_deploy ;;
  *) echo "usage: $0 {merge [--apply]|deploy <expected-version>}" >&2; exit 64 ;;
esac

set +e
python3 "$here/promotion_hop.py" "$mode" --observations-json "@${observations}"
status=$?
set -e

if [ "$mode" = "merge" ] && [ "$apply" = true ] && [ "$status" -eq 0 ]; then
  # --match-head-commit makes the merge atomic with the verified commit: if the
  # promotion PR moved since the verdict, the merge is rejected instead of
  # shipping a tree nobody checked.
  pr_number="$(cat "${workdir}/pr_number")"
  sha="$(cat "${workdir}/head_sha")"
  echo "merging promotion PR #${pr_number} at ${sha}" >&2
  if command -v gh >/dev/null 2>&1; then
    gh pr merge "$pr_number" --repo "$REPOSITORY" --merge --match-head-commit "$sha"
  else
    api "/pulls/${pr_number}/merge" -X PUT \
      -d "$(jq -n --arg sha "$sha" '{merge_method:"merge", sha:$sha}')" >/dev/null \
      && echo "merged via API" >&2
  fi
  status=$?
fi

exit "$status"
