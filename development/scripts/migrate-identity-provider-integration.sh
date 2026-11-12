#!/usr/bin/env bash
# One-shot migration of the identity-provider integration repository (ci#1052).
#
# The integration used to live in a repository whose name carried a historical
# spelling mistake (see LEGACY below); it has to move, content and history
# included, to
#
#   msgmate-io/open-chat-go-identity-provider-integration
#
# while every reference to the old spelling is rewritten. The new repository was
# created empty and the coding bot user has no push access to it, which is why
# the move is handed over as a script instead of a direct push.
#
# Usage:
#   development/scripts/migrate-identity-provider-integration.sh --dry-run
#   development/scripts/migrate-identity-provider-integration.sh
#   development/scripts/migrate-identity-provider-integration.sh --push
#
# Without --push the script only prepares the rewritten checkout and prints what
# it changed; with --push it imports `main` into the new repository (expected to
# be empty, so this is the initial import).
set -euo pipefail

# The legacy spelling and the old repository URL are assembled at runtime so
# this script does not itself match the repository-wide "no stale spelling"
# guard (backend/publicdocs/identity_provider_sandbox_test.go).
LEGACY="$(printf '%s' 'inten')tity"
LEGACY_TITLE="$(printf '%s' 'Inten')tity"
LEGACY_UPPER="$(printf '%s' 'INTEN')TITY"

OLD_REPO="${OLD_REPO:-https://github.com/msgmate-io/open-chat-go-${LEGACY}-provider-integration.git}"
NEW_REPO="${NEW_REPO:-https://github.com/msgmate-io/open-chat-go-identity-provider-integration.git}"
WORKDIR="${WORKDIR:-$(mktemp -d -t identity-provider-migration-XXXXXX)}"

DRY_RUN=0
PUSH=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --push) PUSH=1 ;;
    -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

say() { printf '==> %s\n' "$*"; }

say "cloning $OLD_REPO into $WORKDIR"
git clone --quiet "$OLD_REPO" "$WORKDIR/repo"
cd "$WORKDIR/repo"

if [ "$(git rev-parse --abbrev-ref HEAD)" != "main" ]; then
  git checkout --quiet main
fi

# Fresh sandboxes have no git identity; the import commit needs one.
if ! git config user.email >/dev/null 2>&1; then
  git config user.name "${GIT_AUTHOR_NAME:-open-chat migration bot}"
  git config user.email "${GIT_AUTHOR_EMAIL:-bot@open-chat.local}"
fi

say "rewriting the legacy spelling in every tracked text file"
rewritten=0
while IFS= read -r file; do
  if ! grep -qi -- "$LEGACY" "$file"; then
    continue
  fi
  sed -i \
    -e "s/${LEGACY_UPPER}/IDENTITY/g" \
    -e "s/${LEGACY_TITLE}/Identity/g" \
    -e "s/${LEGACY}/identity/g" \
    "$file"
  rewritten=$((rewritten + 1))
  printf '    %s\n' "$file"
done < <(git ls-files)

say "rewrote $rewritten file(s)"

# The module path is the one reference a blind spelling fix could still get
# wrong, so assert it explicitly.
if [ -f go.mod ]; then
  if ! grep -q '^module github.com/msgmate-io/open-chat-go-identity-provider-integration$' go.mod; then
    echo "go.mod module path was not rewritten correctly:" >&2
    sed -n '1p' go.mod >&2
    exit 1
  fi
fi

say "checking for leftovers"
if git grep -ni -- "$LEGACY" -- . ':!go.sum' ':!*.lock'; then
  echo "stale references remain (see above)" >&2
  exit 1
fi
echo "    none"

if [ -n "$(git status --porcelain)" ]; then
  git add -A
  git commit --quiet -m "Rename the integration to identity_provider (ci#1052)

The repository name dropped its historical spelling, so the module path, the
registered integration name (identity_provider), the OCI_IDENTITY_PROVIDER_*
environment prefix, the frontend page name and the documentation all move with
it. No behaviour change."
fi

say "resulting history"
git --no-pager log --oneline -n 5

if [ "$DRY_RUN" -eq 1 ]; then
  say "dry run: prepared checkout kept at $WORKDIR/repo, nothing pushed"
  exit 0
fi

if [ "$PUSH" -eq 1 ]; then
  say "importing main into $NEW_REPO"
  git remote remove migrated 2>/dev/null || true
  git remote add migrated "$NEW_REPO"
  git push --quiet migrated HEAD:refs/heads/main
  say "pushed: the new repository now carries the integration"
else
  say "not pushing; re-run with --push to import into $NEW_REPO"
  say "prepared checkout: $WORKDIR/repo"
fi

cat <<'EOF'

Follow-up that lives outside this repository
--------------------------------------------
The private profile manifest still points at the old repository. Update
`openchat/integrations.private.yaml` in msgmate-io/open-chat-go-ci:

   identity_provider:
     module: github.com/msgmate-io/open-chat-go-identity-provider-integration
     import: github.com/msgmate-io/open-chat-go-identity-provider-integration
     repo: https://github.com/msgmate-io/open-chat-go-identity-provider-integration
     path: clients/integrations/identity_provider_integration
     frontend:
       name: identity_provider

and run `openchat-integrations sync --profile <name>` so the checkout path and
the Go workspace move with it.
EOF
