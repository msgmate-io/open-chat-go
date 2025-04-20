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

commit_and_push_subrepo_if_changed() {
  local relative_path="$1"
  local repo_path="${REPO_ROOT}/${relative_path}"

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
    git add -A

    if git diff --cached --quiet; then
      echo "No staged changes in ${relative_path} after add; skipping"
      exit 0
    fi

    git commit -m "${COMMIT_MESSAGE}"
    git push
  )
}

commit_and_push_root_if_changed() {
  echo "Checking root repo changes (backend/*, development/*, subrepo pointers)"
  (
    cd "${REPO_ROOT}"

    git add backend development frontend clients/oc_python_client

    if git diff --cached --quiet; then
      echo "No root changes to commit in selected paths; skipping"
      exit 0
    fi

    git commit -m "${COMMIT_MESSAGE}"
    git push
  )
}

commit_and_push_subrepo_if_changed "frontend"
commit_and_push_subrepo_if_changed "clients/oc_python_client"
commit_and_push_subrepo_if_changed "development/ci"
commit_and_push_root_if_changed

echo "Done."
