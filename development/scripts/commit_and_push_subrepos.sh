#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

COMMIT_MESSAGE="${1:-}"
if [[ -z "${COMMIT_MESSAGE}" ]]; then
  read -r -p "Commit message: " COMMIT_MESSAGE
fi

if [[ -z "${COMMIT_MESSAGE}" ]]; then
  echo "Error: commit message is required." >&2
  exit 1
fi

echo "Bumping backend version before commits"
"${REPO_ROOT}/development/scripts/bump_backend_version.sh"

SUBREPO_PATHS=()
if [[ -f "${REPO_ROOT}/.gitmodules" ]]; then
  while read -r _config_key path; do
    SUBREPO_PATHS+=("${path}")
  done < <(git -C "${REPO_ROOT}" config -f .gitmodules --get-regexp path || true)
fi

commit_and_push_subrepo_if_changed() {
  local relative_path="$1"
  local repo_path="${REPO_ROOT}/${relative_path}"
  local current_branch=""
  local remote_head=""
  local target_branch=""
  local upstream_ref=""

  if [[ ! -d "${repo_path}" ]]; then
    echo "Skipping ${relative_path}: directory not found"
    return 0
  fi

  if ! git -C "${repo_path}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "Skipping ${relative_path}: not a git repository"
    return 0
  fi

  if [[ -z "$(git -C "${repo_path}" status --porcelain)" ]]; then
    echo "No changes in ${relative_path}; skipping"
    return 0
  fi

  echo "Committing and pushing ${relative_path}"
  (
    cd "${repo_path}"

    current_branch="$(git symbolic-ref --quiet --short HEAD || true)"
    if [[ -z "${current_branch}" ]]; then
      remote_head="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD || true)"
      target_branch="${remote_head#origin/}"
      if [[ -z "${target_branch}" || "${target_branch}" == "${remote_head}" ]]; then
        target_branch="main"
      fi

      echo "${relative_path} is detached; switching to branch ${target_branch}"
      if git show-ref --verify --quiet "refs/heads/${target_branch}"; then
        git checkout "${target_branch}"
      elif git show-ref --verify --quiet "refs/remotes/origin/${target_branch}"; then
        git checkout -b "${target_branch}" --track "origin/${target_branch}"
      else
        git checkout -b "${target_branch}"
      fi

      current_branch="${target_branch}"
    fi

    git add -A

    if git diff --cached --quiet; then
      echo "No staged changes in ${relative_path} after add; skipping"
      exit 0
    fi

    git commit -m "${COMMIT_MESSAGE}"

    upstream_ref="$(git rev-parse --abbrev-ref --symbolic-full-name "@{u}" 2>/dev/null || true)"
    if [[ -n "${upstream_ref}" ]]; then
      git push
    else
      git push --set-upstream origin "${current_branch}"
    fi
  )
}

commit_and_push_root_if_changed() {
  echo "Checking root repo changes (backend/*, development/*, subrepo pointers)"
  (
    cd "${REPO_ROOT}"

    git add backend development

    if (( ${#SUBREPO_PATHS[@]} > 0 )); then
      git add "${SUBREPO_PATHS[@]}"
    fi

    if git diff --cached --quiet; then
      echo "No root changes to commit in selected paths; skipping"
      exit 0
    fi

    git commit -m "${COMMIT_MESSAGE}"
    git push
  )
}

for subrepo_path in "${SUBREPO_PATHS[@]}"; do
  commit_and_push_subrepo_if_changed "${subrepo_path}"
done

commit_and_push_root_if_changed

echo "Done."
