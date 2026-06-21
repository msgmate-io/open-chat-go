// Windowed consumption of the paged action-tasks feed (ci#801 frontend slice).
//
// `GET /api/v1/chats/action-tasks` serializes the whole backlog by default, so
// the actions-required view used to download and parse every pending row on
// every 5s poll even though it renders one card at a time. The backend now
// accepts `limit`/`offset` and answers with an authoritative `total` plus
// `has_more`; this module holds the (browser-free) maths that turns that
// response into "which window do I ask for next".
//
// Kept pure and exported from the integration surface so the paging logic is
// unit-testable without a mounted SWR cache.

// Small enough that one poll only serializes a handful of rows, large enough to
// keep the two stacked "peek" cards behind the current one populated.
export const ACTION_TASKS_PAGE_SIZE = 5;

export const ACTION_TASKS_ENDPOINT = "/api/v1/chats/action-tasks";

const clampNonNegativeInt = (value: unknown, fallback: number): number =>
  typeof value === "number" && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : fallback;

// The SWR key for one page. Every variant keeps the plain endpoint prefix so
// `revalidateActionTasks`/`optimisticResolveActionTask` still match it.
export function actionTasksFeedKey(
  offset = 0,
  limit: number = ACTION_TASKS_PAGE_SIZE,
): string {
  const safeLimit = clampNonNegativeInt(limit, ACTION_TASKS_PAGE_SIZE);
  const safeOffset = clampNonNegativeInt(offset, 0);
  return `${ACTION_TASKS_ENDPOINT}?limit=${safeLimit}&offset=${safeOffset}`;
}

// The paged response shape the view cares about. `count` stays equal to `total`
// on the backend, so the older count-only shape keeps working unchanged.
export type ActionTasksPageMeta = {
  count?: number;
  total?: number;
  limit?: number;
  offset?: number;
  has_more?: boolean;
};

// The authoritative pending count: prefer `total`, fall back to `count`, then to
// the rows the client happens to hold. Never negative.
export function actionTasksPendingTotal(
  data?: ActionTasksPageMeta | null,
  fallback = 0,
): number {
  const total = data?.total ?? data?.count;
  if (typeof total === "number" && Number.isFinite(total) && total >= 0) {
    return Math.floor(total);
  }
  return fallback > 0 ? fallback : 0;
}

export type NextActionTasksOffset = {
  // Offset of the window currently loaded.
  offset: number;
  // Page size the window was loaded with.
  limit: number;
  // Authoritative pending total from the response.
  total: number;
  // `has_more` from the response: whether the server holds rows past this window.
  hasMore: boolean;
  // Rows still renderable in the loaded window (resolved/dismissed ones dropped).
  windowRemaining: number;
};

// The offset to load next, or null when the loaded window is still usable.
//
// Only advances once the current window is drained (every row resolved, or the
// user skipped past them) and the server reported more rows; `offset` grows
// monotonically, so a drained-window signal cannot loop back and forth.
export function nextActionTasksOffset({
  offset,
  limit,
  total,
  hasMore,
  windowRemaining,
}: NextActionTasksOffset): number | null {
  if (windowRemaining > 0 || !hasMore) {
    return null;
  }
  const step = clampNonNegativeInt(limit, ACTION_TASKS_PAGE_SIZE);
  const next = Math.max(0, Math.floor(offset)) + step;
  return next < total ? next : null;
}

// Whether a loaded window has fallen past the end of the feed, which happens
// when tasks are resolved elsewhere and `total` shrinks under the cursor. The
// view re-anchors at offset 0 instead of rendering an empty "all caught up"
// screen while the badge still reports pending work.
export function shouldReanchorActionTasksOffset(offset: number, total: number): boolean {
  return offset > 0 && total <= offset;
}
