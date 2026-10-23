import { describe, expect, it } from "vitest";
import {
  ACTION_TASKS_ENDPOINT,
  ACTION_TASKS_PAGE_SIZE,
  actionTasksFeedKey,
  actionTasksPendingTotal,
  nextActionTasksOffset,
  shouldReanchorActionTasksOffset,
} from "./action-tasks-page";

// ci#801 frontend slice: the actions-required view must ask the backend for one
// page per poll instead of downloading the whole pending backlog, and it must
// show the server-side pending total (not the rows it happens to hold) so the
// counter cannot drift while the window walks forward.
describe("action-tasks feed key", () => {
  it("always carries an explicit window and keeps the endpoint prefix", () => {
    expect(actionTasksFeedKey(0)).toBe(`${ACTION_TASKS_ENDPOINT}?limit=5&offset=0`);
    expect(actionTasksFeedKey(10, 5)).toBe(`${ACTION_TASKS_ENDPOINT}?limit=5&offset=10`);
    // The plain prefix is what `isActionTasksKey` matches, so revalidation and
    // optimistic resolution keep hitting every window in the cache.
    expect(actionTasksFeedKey(15).startsWith(ACTION_TASKS_ENDPOINT)).toBe(true);
  });

  it("falls back to safe defaults for a missing or bogus window", () => {
    expect(actionTasksFeedKey(-3, 0)).toBe(
      `${ACTION_TASKS_ENDPOINT}?limit=${ACTION_TASKS_PAGE_SIZE}&offset=0`,
    );
    expect(actionTasksFeedKey(Number.NaN, Number.POSITIVE_INFINITY)).toBe(
      `${ACTION_TASKS_ENDPOINT}?limit=${ACTION_TASKS_PAGE_SIZE}&offset=0`,
    );
  });
});

describe("action-tasks pending total", () => {
  it("prefers the authoritative total over the locally loaded rows", () => {
    expect(actionTasksPendingTotal({ total: 42, count: 42 }, 5)).toBe(42);
    expect(actionTasksPendingTotal({ count: 7 }, 1)).toBe(7);
    expect(actionTasksPendingTotal({ total: 0, count: 0 }, 5)).toBe(0);
  });

  it("falls back to the loaded row count when the server omits both", () => {
    expect(actionTasksPendingTotal({}, 3)).toBe(3);
    expect(actionTasksPendingTotal(null, 2)).toBe(2);
    expect(actionTasksPendingTotal(undefined)).toBe(0);
    // A negative/garbage total never leaks into the badge.
    expect(actionTasksPendingTotal({ total: -1 }, 4)).toBe(4);
  });
});

describe("action-tasks window advance", () => {
  const base = { offset: 0, limit: 5, total: 12, hasMore: true };

  it("stays put while the loaded window still has renderable rows", () => {
    expect(nextActionTasksOffset({ ...base, windowRemaining: 3 })).toBeNull();
    expect(nextActionTasksOffset({ ...base, windowRemaining: 1 })).toBeNull();
  });

  it("advances by one page once the window is drained and more rows exist", () => {
    expect(nextActionTasksOffset({ ...base, windowRemaining: 0 })).toBe(5);
    expect(
      nextActionTasksOffset({ ...base, offset: 5, windowRemaining: 0, total: 20 }),
    ).toBe(10);
  });

  it("stops when the server reported no more rows or the cursor passed the end", () => {
    expect(nextActionTasksOffset({ ...base, windowRemaining: 0, hasMore: false })).toBeNull();
    // Cursor + page size would land past the last row.
    expect(
      nextActionTasksOffset({ ...base, offset: 10, windowRemaining: 0, total: 12 }),
    ).toBeNull();
  });
});

describe("action-tasks cursor re-anchor", () => {
  it("re-anchors only when the feed shrank below the loaded cursor", () => {
    expect(shouldReanchorActionTasksOffset(10, 3)).toBe(true);
    expect(shouldReanchorActionTasksOffset(10, 10)).toBe(true);
    expect(shouldReanchorActionTasksOffset(10, 11)).toBe(false);
    // Already at the first window: nothing to re-anchor.
    expect(shouldReanchorActionTasksOffset(0, 0)).toBe(false);
  });
});
