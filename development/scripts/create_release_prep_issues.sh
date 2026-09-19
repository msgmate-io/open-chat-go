#!/usr/bin/env bash
set -euo pipefail

REPO="${REPO:-msgmate-io/open-chat-go}"
ASSIGNEE="${ASSIGNEE:-cur1ousdude}"
LABEL="${LABEL:-release}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ISSUE_DIR="${SCRIPT_DIR}/../release/issues"

gh label create "$LABEL" --repo "$REPO" --color "0E8A16" \
  --description "Release preparation and distribution" 2>/dev/null || true

for file in "$ISSUE_DIR"/*.md; do
  [ -e "$file" ] || continue
  title="$(grep -m1 '^# ' "$file" | sed 's/^# //')"
  [ -n "$title" ] || { echo "No '# title' in $file" >&2; exit 1; }

  existing="$(gh issue list --repo "$REPO" --state open --search "$title in:title" \
    --json number,title --jq ".[] | select(.title == \"$title\") | .number" | head -n1)"
  if [ -n "$existing" ]; then
    echo "exists: #$existing $title"
    continue
  fi

  url="$(gh issue create --repo "$REPO" --title "$title" --body-file "$file" \
    --label "$LABEL" --assignee "$ASSIGNEE")"
  echo "created: #${url##*/} $title"
done
