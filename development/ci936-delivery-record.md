# ci#936 delivery record — CLI browser-auth state-takeover fix (CVSS 8.0)

Security fix: `fix(user): harden CLI browser-auth against state takeover`.

## Merged into `dev` (integration trunk, ci#833)

- Core fix commit `cb8527fc` merged to `dev` via consolidation PR
  [#1301](https://github.com/msgmate-io/open-chat-go/pull/1301)
  (`chore/consolidate-2026-11-02-dev`), merged 2026-11-02T10:57:00Z
  (merge commit `b4c37dc6`).
- Docs follow-up (`c9c99d00`) and pin-freshness opt-out (`c359b882`)
  merged to `dev` via consolidation PR
  [#1313](https://github.com/msgmate-io/open-chat-go/pull/1313)
  (`chore/consolidate-2026-11-02-dev-r3`, VERSION 0.0.804).

## Leaves the codebase (this PR)

- PR #1311 (`fix/cli-auth-session-binding`, base `dev`), the leaf PR carrying
  the `Closes msgmate-io/open-chat-go-ci#936` line, is now fully absorbed into
  `dev` (0 commits ahead; its commits are ancestors of both consolidations)
  and will be closed by the consolidation runner.
- The remaining linkage is `msgmate-io/open-chat-go-ci#936` itself, which the
  runner closes once every linked leaf is merged (GitHub cannot auto-close a
  cross-repo issue; see DEVELOPMENT.md "One leaf issue per pull request").