# ci#936 delivery record — CLI browser-auth state-takeover fix (CVSS 8.0)

Security fix: `fix(user): harden CLI browser-auth against state takeover`.

## Core fix — already on `dev` (integration trunk, ci#833)

- Core fix commit `cb8527fc` merged to `dev` via consolidation PR
  [#1301](https://github.com/msgmate-io/open-chat-go/pull/1301)
  (`chore/consolidate-2026-11-02-dev`), merged 2026-11-02T10:57:00Z
  (merge commit `b4c37dc6` — current head of `dev`).

## Leaf-PR tail — in transit via r3 consolidation (#1313)

- PR #1311 (`fix/cli-auth-session-binding`, base `dev`) is the leaf PR carrying
  `Closes msgmate-io/open-chat-go-ci#936`. Its head is 2 commits ahead of
  `dev`:
  - `c9c99d00` docs(user): cli-auth poll requires the paired secret
  - `c359b882` ci: pin-freshness opt-out for the product_landing_pages drift
- That head is already merged into consolidation PR
  [#1313](https://github.com/msgmate-io/open-chat-go/pull/1313)
  (`chore/consolidate-2026-11-02-dev-r3`, VERSION 0.0.804, head `aef68ed3`),
  which is **open** and pending merge to `dev`.

## Closure (runner-owned, per DEVELOPMENT.md)

- Once #1313 merges to `dev`, PR #1311 is 0 commits ahead / fully absorbed and
  the consolidation runner closes it, then closes the linked leaf issue
  `msgmate-io/open-chat-go-ci#936` (GitHub cannot auto-close a cross-repo
  issue; see DEVELOPMENT.md "One leaf issue per pull request"). No further
  action is needed from the leaf branch — do not force-push or retarget it.