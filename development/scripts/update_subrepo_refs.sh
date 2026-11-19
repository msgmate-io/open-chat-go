#!/usr/bin/env bash
# update_subrepo_refs.sh - the single owner of the sub-repo ref refresh (ci#1138).
#
# Why this exists
# ---------------
# The monorepo pins every sub-repo (`.gitmodules` path) to an exact commit
# (gitlink). Hand-editing `.gitmodules` or a gitlink in a `git add`/`git commit`
# dance is the source of every "the super-project pins a commit that no longer
# exists" incident: the pin silently drops a sub-repo commit that is only on an
# open PR branch, or it points at a commit that was force-pushed away.
#
# This script is the ONLY supported way to move a sub-repo pin. For each
# sub-repo it decides, per gitlink:
#
#   1. already at the tip of the sub-repo `main`      -> no-op
#   2. the pinned commit belongs to an open PR branch -> check/refresh that
#      branch against `main` and keep the pin on the branch head
#   3. otherwise                                      -> fast-forward to the
#      sub-repo `main` tip
#
# and then `git add`s only the gitlinks it moved and refreshes
# `integrations.lock.json` (via `development/scripts/build_tools.sh sync`) so the
# lockfile never pins an unreachable commit.
#
# Usage
#   development/scripts/update_subrepo_refs.sh            # apply
#   development/scripts/update_subrepo_refs.sh --dry-run   # report only
#   development/scripts/update_subrepo_refs.sh --no-fetch  # use local refs only
#
# Never hand-edit `.gitmodules` or a gitlink: run this script instead.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

DRY_RUN=0
FETCH=1
PROFILE="${INTEGRATION_PROFILE:-full}"
LOCKFILE="integrations.lock.json"

usage() {
  cat <<'EOF'
Usage: update_subrepo_refs.sh [--dry-run] [--no-fetch] [--profile <name>]

Refreshes every sub-repo gitlink pinned by .gitmodules:
  * no-op when the pin is already the sub-repo `main` tip,
  * follows an open sub-repo PR branch (checked/refreshed against `main`) when
    the pin belongs to such a branch,
  * fast-forwards to the sub-repo `main` tip otherwise.
Then stages the moved gitlinks and refreshes integrations.lock.json.
EOF
}

while (( $# > 0 )); do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --no-fetch) FETCH=0 ;;
    --profile)
      shift
      [[ $# -gt 0 ]] || { echo "error: --profile needs a value" >&2; exit 2; }
      PROFILE="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

log()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# worktree_root <path>
#   The correct `-C` directory for a sub-repo. A materialized integration uses a
#   `.git` *file* (gitdir: ...), so `-d .git` would miss it; `rev-parse` is the
#   reliable probe.
worktree_root() {
  local path="$1"
  git -C "$path" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 1
  printf '%s' "$path"
}

# gitmodules_paths
#   Every `submodule.<name>.path` declared in .gitmodules.
gitmodules_paths() {
  [[ -f .gitmodules ]] || return 0
  git config -f .gitmodules --get-regexp '^submodule\..*\.path$' | awk '{print $2}'
}

# pinned_commit <path>
#   The commit the super-project currently pins for `path` (HEAD gitlink, or the
#   index gitlink when `.gitmodules` predates an unstaged change).
pinned_commit() {
  local path="$1" commit
  commit="$(git rev-parse "HEAD:${path}" 2>/dev/null || true)"
  [[ -n "$commit" ]] || commit="$(git rev-parse ":${path}" 2>/dev/null || true)"
  printf '%s' "$commit"
}

# main_tip <path>
#   The sub-repo `main` head, preferring `origin/main` when a remote exists: the
#   pinned commit must be reachable from the *published* main, not just a stale
#   local branch.
main_tip() {
  local path="$1" ref tip
  if git -C "$path" rev-parse --verify --quiet origin/main >/dev/null 2>&1; then
    ref="origin/main"
  elif git -C "$path" rev-parse --verify --quiet refs/heads/main >/dev/null 2>&1; then
    ref="refs/heads/main"
  else
    return 1
  fi
  # Keep the local main aligned with the fetched remote tip so the fast-forward
  # below is a real fast-forward and not a merge.
  if [[ "$ref" == "origin/main" ]] && git -C "$path" rev-parse --verify --quiet refs/heads/main >/dev/null 2>&1; then
    if git -C "$path" merge --ff-only --quiet origin/main refs/heads/main >/dev/null 2>&1; then
      :
    fi
  fi
  tip="$(git -C "$path" rev-parse "$ref")"
  printf '%s' "$tip"
}

# pr_branch_for_commit <path> <commit>
#   Prints the local branch whose PR is open and which contains `commit`.
#
#   An "open PR branch" is modelled as a sub-repo branch other than `main` that
#   carries the commit and whose name is advertised in
#   `MSG_REPO_PR_BRANCHES` (comma separated, set by the calling workflow from the
#   git integration) or, absent that hint, a local branch named
#   `feat/*`, `fix/*` or `chore/*` that contains the commit. This keeps the
#   script hermetic: it never talks to GitHub itself.
pr_branch_for_commit() {
  local path="$1" commit="$2" branches branch
  if [[ -n "${MSG_REPO_PR_BRANCHES:-}" ]]; then
    IFS=',' read -r -a branches <<<"${MSG_REPO_PR_BRANCHES}"
  else
    branches=()
    while IFS= read -r branch; do
      branches+=("$branch")
    done < <(git -C "$path" for-each-ref --format='%(refname:short)' refs/heads)
  fi
  for branch in "${branches[@]}"; do
    branch="${branch// /}"
    [[ -n "$branch" ]] || continue
    [[ "$branch" == "main" || "$branch" == "master" ]] && continue
    # Only feature-shaped branches are treated as PR branches unless the caller
    # explicitly advertised the branch name.
    if [[ -z "${MSG_REPO_PR_BRANCHES:-}" ]]; then
      case "$branch" in
        feat/*|fix/*|chore/*) ;;
        *) continue ;;
      esac
    fi
    git -C "$path" rev-parse --verify --quiet "refs/heads/${branch}" >/dev/null 2>&1 || continue
    if git -C "$path" merge-base --is-ancestor "$commit" "refs/heads/${branch}" 2>/dev/null; then
      printf '%s' "$branch"
      return 0
    fi
  done
  return 1
}

# refresh_pr_branch <path> <branch>
#   Checks the PR branch out and refreshes it against the sub-repo `main`: a
#   clean fast-forward when the branch is behind, otherwise the branch head is
#   kept as-is (rebasing someone's open PR is never this script's job).
refresh_pr_branch() {
  local path="$1" branch="$2" tip
  tip="$(main_tip "$path")" || return 0
  if git -C "$path" merge-base --is-ancestor "$tip" "refs/heads/${branch}" 2>/dev/null; then
    log "    branch ${branch} already contains ${tip:0:12} of main"
    return 0
  fi
  if (( DRY_RUN )); then
    log "    would fast-forward branch ${branch} to ${tip:0:12} (dry-run)"
    return 0
  fi
  if [[ -n "$(git -C "$path" status --porcelain)" ]]; then
    warn "sub-repo ${path}: worktree dirty, leaving branch ${branch} unrefreshed"
    return 0
  fi
  if git -C "$path" merge --ff-only --quiet "$tip" "refs/heads/${branch}" >/dev/null 2>&1; then
    log "    fast-forwarded branch ${branch} to ${tip:0:12}"
  else
    warn "sub-repo ${path}: branch ${branch} diverged from main; rebase it by hand"
  fi
}

MOVED=()
UP_TO_DATE=()
SKIPPED=()

while IFS= read -r subrepo_path; do
  [[ -n "$subrepo_path" ]] || continue
  if ! worktree_root "$subrepo_path" >/dev/null; then
    warn "sub-repo ${subrepo_path} is not checked out; skipping"
    SKIPPED+=("${subrepo_path}")
    continue
  fi

  if (( FETCH )); then
    git -C "$subrepo_path" fetch --quiet --prune origin '+refs/heads/*:refs/remotes/origin/*' >/dev/null 2>&1 || \
      warn "sub-repo ${subrepo_path}: fetch failed; using cached remote refs"
  fi

  pinned="$(pinned_commit "$subrepo_path")"
  if [[ -z "$pinned" ]]; then
    warn "sub-repo ${subrepo_path} has no gitlink pin; skipping"
    SKIPPED+=("${subrepo_path}")
    continue
  fi

  tip="$(main_tip "$subrepo_path" || true)"
  if [[ -z "$tip" ]]; then
    warn "sub-repo ${subrepo_path} has no main branch; skipping"
    SKIPPED+=("${subrepo_path}")
    continue
  fi

  log "sub-repo ${subrepo_path}: pinned ${pinned:0:12}, main tip ${tip:0:12}"

  if [[ "$pinned" == "$tip" ]]; then
    log "    already at the main tip; no-op"
    UP_TO_DATE+=("${subrepo_path}")
    continue
  fi

  target=""
  if pr_branch="$(pr_branch_for_commit "$subrepo_path" "$pinned")"; then
    log "    pin belongs to the open PR branch ${pr_branch}"
    refresh_pr_branch "$subrepo_path" "$pr_branch"
    target="$(git -C "$subrepo_path" rev-parse "refs/heads/${pr_branch}")"
    if [[ "$target" != "$pinned" ]]; then
      warn "sub-repo ${subrepo_path}: branch ${pr_branch} head ${target:0:12} differs from the pin ${pinned:0:12}"
    fi
  else
    if git -C "$subrepo_path" merge-base --is-ancestor "$pinned" "$tip" 2>/dev/null; then
      log "    pin is behind main; fast-forwarding to the main tip"
      target="$tip"
    else
      warn "sub-repo ${subrepo_path}: pin ${pinned:0:12} is not reachable from main; moving to the main tip ${tip:0:12}"
      target="$tip"
    fi
  fi

  if [[ -z "$target" || "$target" == "$pinned" ]]; then
    log "    pin unchanged"
    UP_TO_DATE+=("${subrepo_path}")
    continue
  fi

  if (( DRY_RUN )); then
    log "    would move pin ${pinned:0:12} -> ${target:0:12} (dry-run)"
    MOVED+=("${subrepo_path}")
    continue
  fi

  git update-index --cacheinfo "160000,${target},${subrepo_path}"
  log "    pin moved ${pinned:0:12} -> ${target:0:12}"
  MOVED+=("${subrepo_path}")
done < <(gitmodules_paths)

echo
log "summary: ${#MOVED[@]} moved, ${#UP_TO_DATE[@]} up to date, ${#SKIPPED[@]} skipped"

if (( ${#MOVED[@]} == 0 )); then
  log "nothing to stage; ${LOCKFILE} untouched"
  exit 0
fi

if (( DRY_RUN )); then
  log "dry-run: would stage the gitlinks above and refresh ${LOCKFILE}"
  exit 0
fi

# Only the moved gitlinks are staged: a blanket `git add -A` would sweep
# unrelated worktree changes into the ref-update commit.
for subrepo_path in "${MOVED[@]}"; do
  git add -- "${subrepo_path}"
done

log "staged gitlinks: ${MOVED[*]}"

# Refresh the lockfile so it never pins an unreachable commit. A missing tool
# build is a warning, not a failure: the script's primary contract (the gitlink
# refresh) has already succeeded.
if [[ -f "development/scripts/build_tools.sh" ]]; then
  log "refreshing ${LOCKFILE} (profile ${PROFILE})"
  if CLI_PATH="$(bash development/scripts/build_tools.sh 2>/dev/null)"; then
    if ! "$CLI_PATH" sync --profile "$PROFILE"; then
      warn "${LOCKFILE} refresh failed; the gitlinks are staged but the lockfile may be stale"
    fi
  else
    warn "could not build the integration CLI; ${LOCKFILE} not refreshed"
  fi
else
  warn "development/scripts/build_tools.sh missing; ${LOCKFILE} not refreshed"
fi

if ! git diff --cached --quiet -- "${LOCKFILE}" 2>/dev/null; then
  log "${LOCKFILE} staged"
fi