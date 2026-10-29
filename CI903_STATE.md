# ci#903 — r75 consolidation tracker: delivery state (2026-10-27, closed)

Retired on 2026-10-28 via commit `40669e53` (PR #1221): the r75 follow-up, r76,
r77, and r78 rounds are each accounted for in their own tracker issues, and the
public repo ticked beyond the r75 scope. Kept as the historical record.

## Final scope (r75, merged 2026-10-27)

- PR #1193 `chore/consolidate-2026-10-27-r75` (dev → main consolidation,
  VERSION 0.0.788) **merged into `main`** on 2026-10-27 (merge `130b11d`,
  [ci#903] closes); contents: #1195 node20/24 actions bump, #1196 API version
  pin (ci#897), #1198 VERSION bump to 0.0.787, #1186 db autovacuum + uuid index
  (ci#889), #1194 OAuth provider sync (ci#888), #1192 CI900_STATE.md, #1190
  cached bot reply + 45s self-message defer + queue drain (ci#895-ci#896), plus
  the r75-branch compile fix on the r75 head, CI903_STATE.md, and #1189
  bot JSON-error envelope (ci#865), #1187 fix(admin): token-usage cache-hit,
  and VERSION 0.0.787. This closed ci#900, ci#895, ci#866 (r75 delivery), and
  ci#903.
- **VERSION 0.0.788** (r75) in `backend/api/metrics/handler.go`
- Tracker closed with PR #1193. One follow-up was needed because the r75 PR
  body could not carry every `Closes` in one standalone line.

## Follow-ups after r75 (rolled into #1200, merged 2026-10-27)

- **#1200** `chore/consolidate-2026-10-27-r75-followup` (merged into `main` on
  2026-10-27 as merge `2dbbd40`): VERSION bump 0.0.788 → 0.0.789 so the next
  round (r76, #1197) could bump 0.0.789 → 0.0.790, and absorbed
  `d1d1103` (a `db:indexes` workspace state fix on the r75 base).

## Later rounds (superseded this tracker)

- r76 #1197, r77 #1211/#1214, r78 #1216: each delivered VERSION bumps
  0.0.789 → 0.0.795 and their own leaf issues (noted in their tracker issues
  #903 → superseded by ci#913 and onward). The r75 tracker only accounts for
  exactly the r75 scope above.
- PR CI on the r75 head was green before merge (`Analyze`, all four test
  suites, and the merge gate all passed).
