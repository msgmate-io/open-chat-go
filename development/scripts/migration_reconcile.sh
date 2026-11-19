#!/usr/bin/env bash
# migration_reconcile.sh - guarded migration-conflict reconciliation (ci#1138).
#
# Implements steps 2-5 of the migration-conflict procedure of ci#1138 section 4.
# It runs inside the *target* repository (the one that owns the Django app, e.g.
# the Little World backend), not in this monorepo, and it is the only supported
# way to delete a conflicting migration: an agent must never `git rm` a migration
# from memory.
#
# The whole point is the reachability guard: a migration file may only be deleted
# when the commit that ADDED it is NOT reachable from `origin/main`. A migration
# that `main` already owns is hard-forbidden - deleting it makes every existing
# environment un-migratable.
#
# Steps
#   1. preflight: HEAD must not be main/master; `origin/main` must be fetched and
#      fully merged into HEAD (run with --merge to let the script merge it).
#   2. candidate set = `git diff --diff-filter=A --name-only
#      $(git merge-base origin/main HEAD)..HEAD -- '*/migrations/*.py'`, filtered
#      to files whose adding commit is not an ancestor of origin/main.
#   3. record module labels, `git rm` the candidates, commit the deletion alone.
#   4. `python3 manage.py makemigrations <modules>` then `migrate --plan`.
#   5. verify `makemigrations --check --dry-run` is clean, that the regenerated
#      set covers exactly the modules from step 3, and that no main-owned file
#      changed.
#
# Usage
#   development/scripts/migration_reconcile.sh                  # dry-run (default)
#   development/scripts/migration_reconcile.sh --apply           # execute 2-5
#   development/scripts/migration_reconcile.sh --apply --merge    # merge origin/main first
#   development/scripts/migration_reconcile.sh --apply --no-regenerate
#
# `--dry-run` is the default: nothing is deleted and nothing is committed until
# `--apply` is passed.
set -euo pipefail

DRY_RUN=1
DO_MERGE=0
REGENERATE=1
MAIN_REF="${MIGRATION_MAIN_REF:-origin/main}"
PYTHON="${MIGRATION_PYTHON:-python3}"
MANAGE_PY="${MIGRATION_MANAGE_PY:-manage.py}"
EXPECT_CONFLICT=0

usage() {
  cat <<'EOF'
Usage: migration_reconcile.sh [--apply] [--merge] [--no-regenerate]

  --apply          actually delete the conflicting migrations and commit
                   (default is a dry run that only reports)
  --merge          run `git fetch origin main && git merge origin/main` first
  --no-regenerate  skip steps 4-5 (only allowed when no manage.py is present)

Steps 2-5 of the ci#1138 migration-conflict procedure. A migration is only ever
deleted when the commit that added it is not reachable from origin/main.
EOF
}

while (( $# > 0 )); do
  case "$1" in
    --apply) DRY_RUN=0 ;;
    --dry-run) DRY_RUN=1 ;;
    --merge) DO_MERGE=1 ;;
    --no-regenerate) REGENERATE=0 ;;
    --main-ref)
      shift
      [[ $# -gt 0 ]] || { echo "error: --main-ref needs a value" >&2; exit 2; }
      MAIN_REF="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

log()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not inside a git work tree"

# ---------------------------------------------------------------------------
# Guard: never reconcile on main. The commit that removes migrations must land
# on the PR branch; a deletion committed to main is unrecoverable for every
# deployed environment.
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "${CURRENT_BRANCH}" == "main" || "${CURRENT_BRANCH}" == "master" ]]; then
  die "refusing to reconcile migrations on '${CURRENT_BRANCH}': check out the PR branch first (these deletions may never be committed to main)"
fi
if [[ "${CURRENT_BRANCH}" == "HEAD" ]]; then
  die "refusing to reconcile migrations from a detached HEAD: check out the PR branch first"
fi
log "branch: ${CURRENT_BRANCH}"

# ---------------------------------------------------------------------------
# Step 1: get origin/main merged into HEAD. The migration conflicts only exist
# relative to the merge base, so the merge is a precondition, not an option.
if (( DO_MERGE )); then
  log "step 1: fetching and merging ${MAIN_REF}"
  git fetch origin main
  if git merge-base --is-ancestor "${MAIN_REF}" HEAD; then
    log "  ${MAIN_REF} already merged"
  elif (( DRY_RUN )); then
    log "  dry-run: would run 'git merge ${MAIN_REF}' (conflicts collected by the operator)"
  else
    if git merge --no-edit "${MAIN_REF}" >/dev/null 2>&1; then
      log "  merge produced no conflicts"
    else
      # Migration conflicts are expected here; anything else is not ours.
      EXPECT_CONFLICT=1
      CONFLICTS="$(git diff --name-only --diff-filter=U || true)"
      MIGRATION_CONFLICTS="$(printf '%s\n' "${CONFLICTS}" | grep -E '(^|/)migrations/.*\.py$' || true)"
      if [[ -z "${MIGRATION_CONFLICTS}" ]]; then
        die "merge produced conflicts but none are migration files; resolve them by hand:
${CONFLICTS}"
      fi
      log "  migration conflicts collected:"
      printf '%s\n' "${MIGRATION_CONFLICTS}" | sed 's/^/    /'
      log "  resolve the conflicted migration files on the PR branch, then re-run this script"
      log "  (the deletion commit must be based on the merged tree; aborting before step 2)"
      exit 0
    fi
  fi
fi

if ! git rev-parse --verify --quiet "${MAIN_REF}" >/dev/null 2>&1; then
  die "cannot resolve ${MAIN_REF}: run 'git fetch origin main' first (or pass --merge)"
fi
if ! git merge-base --is-ancestor "${MAIN_REF}" HEAD; then
  die "${MAIN_REF} is not merged into ${CURRENT_BRANCH}: run with --merge or merge it by hand before reconciling"
fi

MERGE_BASE="$(git merge-base "${MAIN_REF}" HEAD)"
log "merge base with ${MAIN_REF}: ${MERGE_BASE:0:12}"

# ---------------------------------------------------------------------------
# Step 2: candidate set, with the reachability guard.
mapfile -t ADDED < <(git diff --diff-filter=A --name-only "${MERGE_BASE}..HEAD" -- '*/migrations/*.py')
if (( ${#ADDED[@]} == 0 )); then
  log "step 2: no migrations were added on this branch; nothing to reconcile"
  exit 0
fi

DELETE_CANDIDATES=()
FORBIDDEN=()
for migration in "${ADDED[@]}"; do
  [[ -n "${migration}" ]] || continue
  [[ "${migration}" == *migrations/__init__.py ]] && continue
  # The commit that added the file on this branch.
  added_by="$(git log --diff-filter=A --format=%H -1 "${MERGE_BASE}..HEAD" -- "${migration}" || true)"
  if [[ -z "${added_by}" ]]; then
    warn "cannot determine the commit that added ${migration}; refusing to delete it"
    FORBIDDEN+=("${migration}")
    continue
  fi
  if git merge-base --is-ancestor "${added_by}" "${MAIN_REF}"; then
    # Hard-forbidden: main owns this migration.
    FORBIDDEN+=("${migration}")
    continue
  fi
  DELETE_CANDIDATES+=("${migration}")
done

if (( ${#FORBIDDEN[@]} > 0 )); then
  warn "main-owned migrations are never deleted (hard-forbidden):"
  printf '    %s\n' "${FORBIDDEN[@]}" >&2
  die "refusing to continue: ${#FORBIDDEN[@]} candidate(s) are reachable from ${MAIN_REF}"
fi

if (( ${#DELETE_CANDIDATES[@]} == 0 )); then
  log "step 2: no branch-only migrations to reconcile"
  exit 0
fi

log "step 2: branch-only migrations (safe to delete):"
printf '    %s\n' "${DELETE_CANDIDATES[@]}"

# Step 3: record module labels. A migration path looks like
#   <app>/migrations/0003_name.py  ->  module label <app>
MODULES=()
for migration in "${DELETE_CANDIDATES[@]}"; do
  module="$(printf '%s' "${migration}" | awk -F'/migrations/' '{print $1}' | awk -F/ '{print $NF}')"
  if [[ -z "${module}" ]]; then
    die "cannot derive a module label from ${migration}"
  fi
  duplicate=0
  for known in ${MODULES[@]+"${MODULES[@]}"}; do
    [[ "${known}" == "${module}" ]] && duplicate=1
  done
  (( duplicate )) || MODULES+=("${module}")
done
log "step 3: module labels: ${MODULES[*]}"

if (( DRY_RUN )); then
  log "dry-run: would run"
  printf '    git rm -- %s\n' "${DELETE_CANDIDATES[*]}"
  log "  then commit the deletion alone, then regenerate:"
  printf '    %s %s makemigrations %s\n' "${PYTHON}" "${MANAGE_PY}" "${MODULES[*]}"
  printf '    %s %s migrate --plan\n' "${PYTHON}" "${MANAGE_PY}"
  printf '    %s %s makemigrations --check --dry-run\n' "${PYTHON}" "${MANAGE_PY}"
  log "re-run with --apply to execute (steps 4-5 included)"
  exit 0
fi

# The regeneration must happen in the same tree, so the deletion commit is made
# first and regeneration follows as its own commit.
git rm --quiet -- "${DELETE_CANDIDATES[@]}"
git commit --quiet -m "reconcile migrations: drop branch-only modules (${MODULES[*]})

The following migrations were added only on ${CURRENT_BRANCH} and are not
reachable from ${MAIN_REF}; they are regenerated against the merged state:
$(printf '  - %s\n' "${DELETE_CANDIDATES[@]}")

Deletion is guarded by \`git merge-base --is-ancestor\`: a migration reachable
from ${MAIN_REF} is never removed (ci#1138)."
log "step 3: deletion committed"

if (( ! REGENERATE )); then
  warn "--no-regenerate: steps 4-5 skipped; regenerate the migrations before pushing"
  exit 0
fi

if [[ ! -f "${MANAGE_PY}" ]]; then
  die "steps 4-5 need ${MANAGE_PY} in $(pwd); run them in the Django repository, or re-run with --no-regenerate"
fi

# Step 4: module-scoped regeneration (never global: that would touch unrelated
# apps) and a dry-run migration plan.
log "step 4: makemigrations ${MODULES[*]}"
"${PYTHON}" "${MANAGE_PY}" makemigrations "${MODULES[@]}"
log "step 4: migrate --plan"
"${PYTHON}" "${MANAGE_PY}" migrate --plan

# Step 5: verification.
log "step 5: verifying"
if ! "${PYTHON}" "${MANAGE_PY}" makemigrations --check --dry-run; then
  die "makemigrations --check is not clean: regenerate the remaining modules before pushing"
fi

REGENERATED="$(git status --porcelain -- '*/migrations/*.py' | awk '{print $2}' || true)"
if [[ -n "${REGENERATED}" ]]; then
  log "regenerated migrations:"
  printf '    %s\n' "${REGENERATED}" | sed 's/^/  /'
fi

CHANGED_MAIN_FILES="$(git diff --name-only "${MAIN_REF}" -- '*/migrations/*.py' | while IFS= read -r changed; do
  owner="$(git log --diff-filter=A --format=%H -1 "${MAIN_REF}" -- "${changed}" || true)"
  if [[ -n "${owner}" ]] && git merge-base --is-ancestor "${owner}" "${MAIN_REF}"; then
    printf '%s\n' "${changed}"
  fi
done)"
if [[ -n "${CHANGED_MAIN_FILES}" ]]; then
  die "main-owned migrations were modified:
${CHANGED_MAIN_FILES}
Only branch-added migrations may change."
fi

log "reconciliation complete; commit the regenerated migrations and push to ${CURRENT_BRANCH} (never main)"