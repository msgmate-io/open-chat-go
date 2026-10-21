import { describe, expect, it } from "vitest";
import {
  actionTaskKey,
  applyActionTaskCountDecrement,
  applyActionTaskResolution,
  isActionTasksCountKey,
  isActionTasksKey,
  isChatListKey,
  isChatStatesKey,
  normalizeCacheKey,
  patchActionTasksFeed,
} from "./chat-cache";

// The paginated chat list (ci#579) is fetched with `useSWRInfinite`, which
// stores its aggregated entry under `$inf$<first page key>`. Invalidation must
// still find that entry, otherwise new chats/previews and pin updates only
// appear on the next poll.
describe("chat cache key matching (ci#579)", () => {
  it("strips SWR's infinite marker", () => {
    expect(normalizeCacheKey("$inf$/api/v1/chats/list?page=1&limit=5")).toBe(
      "/api/v1/chats/list?page=1&limit=5",
    );
    expect(normalizeCacheKey("/api/v1/chats/list?page=1")).toBe("/api/v1/chats/list?page=1");
    expect(normalizeCacheKey(undefined)).toBeNull();
    expect(normalizeCacheKey({ not: "a string" })).toBeNull();
  });

  it("matches the aggregated infinite entry and its plain page keys", () => {
    expect(isChatListKey("$inf$/api/v1/chats/list?page=1&limit=5")).toBe(true);
    expect(isChatListKey("/api/v1/chats/list?page=2&limit=5")).toBe(true);
    expect(isChatListKey("/api/v1/chats/states?chat_uuids=a,b")).toBe(false);
  });

  it("keeps the other chat caches distinguishable", () => {
    expect(isChatStatesKey("$inf$/api/v1/chats/states?chat_uuids=a")).toBe(true);
    expect(isActionTasksKey("/api/v1/chats/action-tasks?count_only=1")).toBe(true);
    expect(isChatListKey("/api/v1/chats/action-tasks?count_only=1")).toBe(false);
  });
});

// The pending action-task feed is served under two shapes behind the same
// endpoint: the sidebar badge requests `?count_only=1` (only `{count}`), the
// action-task view requests the plain endpoint (`{count, rows}`). An optimistic
// update must patch the right shape or the badge and the card list drift apart
// (ci#726).
describe("action-task cache keys and optimistic updates (ci#726)", () => {
  it("detects the compact count-only key, including the SWR infinite marker", () => {
    expect(isActionTasksCountKey("/api/v1/chats/action-tasks?count_only=1")).toBe(true);
    expect(isActionTasksCountKey("$inf$/api/v1/chats/action-tasks?count_only=1")).toBe(true);
    expect(isActionTasksCountKey("/api/v1/chats/action-tasks")).toBe(false);
    expect(isActionTasksCountKey("/api/v1/chats/action-tasks?scope=all")).toBe(false);
    expect(isActionTasksCountKey(undefined)).toBe(false);
    // Still an action-task key overall, so invalidation covers both shapes.
    expect(isActionTasksKey("/api/v1/chats/action-tasks?count_only=1")).toBe(true);
  });

  it("drops exactly one resolved row from the full feed and re-derives the count", () => {
    const feed = {
      count: 3,
      rows: [{ task_key: "a" }, { task_key: "b" }, { task_key: "c" }],
    };
    const next = applyActionTaskResolution(feed, "b");
    expect(next?.rows?.map((row) => row.task_key)).toEqual(["a", "c"]);
    expect(next?.count).toBe(2);
    // The input is never mutated in place: SWR hands the same object to every
    // matched key, and a shared by-reference edit would corrupt other entries.
    expect(feed.rows).toHaveLength(3);
  });

  it("is a no-op when the row is absent, the key is missing or the shape is unknown", () => {
    const feed = { count: 2, rows: [{ task_key: "a" }, { task_key: "b" }] };
    expect(applyActionTaskResolution(feed, "zzz")).toBe(feed);
    expect(applyActionTaskResolution(feed, null)).toBe(feed);
    expect(applyActionTaskResolution(undefined, "a")).toBeUndefined();
  });

  it("falls back to the row count when the feed carries no explicit count", () => {
    const next = applyActionTaskResolution({ rows: [{ task_key: "a" }, { task_key: "b" }] }, "a");
    expect(next?.count).toBe(1);
  });

  it("decrements the badge count and clamps at zero", () => {
    expect(applyActionTaskCountDecrement({ count: 5 })).toEqual({ count: 4 });
    expect(applyActionTaskCountDecrement({ count: 1 })).toEqual({ count: 0 });
    expect(applyActionTaskCountDecrement({ count: 0 })).toEqual({ count: 0 });
    const countOnly = { count: 2 };
    expect(applyActionTaskCountDecrement(countOnly)).not.toBe(countOnly);
    // No numeric count -> nothing to patch, return the entry untouched.
    expect(applyActionTaskCountDecrement(undefined)).toBeUndefined();
    expect(applyActionTaskCountDecrement({} as { count?: number })).toEqual({});
  });

  it("routes each cache shape to the matching updater", () => {
    // `mutate` passes the same updater every matched key, so the shape decides.
    expect(patchActionTasksFeed({ count: 4 }, "task-1")).toEqual({ count: 3 });
    expect(patchActionTasksFeed({ count: 2, rows: [{ task_key: "task-1" }] }, "task-1")).toEqual({
      count: 1,
      rows: [],
    });
    expect(patchActionTasksFeed(undefined, "task-1")).toBeUndefined();
  });

  it("keeps the paged feed's authoritative total in step with the rows (ci#801)", () => {
    // The view windows the feed (`?limit=&offset=`) and shows `total` as the
    // pending count, so an optimistic resolve must decrement it as well or the
    // badge keeps claiming a task that is already gone.
    const feed = {
      count: 12,
      total: 12,
      limit: 5,
      offset: 0,
      has_more: true,
      rows: [{ task_key: "a" }, { task_key: "b" }],
    };
    const next = applyActionTaskResolution(feed, "a");
    expect(next?.total).toBe(11);
    expect(next?.count).toBe(11);
    expect(next?.rows?.map((row) => row.task_key)).toEqual(["b"]);
    // The paging metadata survives the patch untouched.
    expect(next?.limit).toBe(5);
    expect(next?.has_more).toBe(true);

    // The count-only badge entry has no `total`; it must not grow a bogus one.
    const badge = applyActionTaskCountDecrement({ count: 3 });
    expect(badge).toEqual({ count: 2 });
    expect("total" in (badge ?? {})).toBe(false);
  });

  it("derives the backend task key the same way the API does", () => {
    // backend/api/chats/ActionTasks.go `actionTaskKey`: lowercase + trim, joined
    // with a colon. A mismatched casing would silently drop the wrong (no) row.
    expect(actionTaskKey("CHAT-1", "Msg-2")).toBe("chat-1:msg-2");
    expect(actionTaskKey(" chat-1 ", " msg-2 ")).toBe("chat-1:msg-2");
    expect(actionTaskKey("", "msg-2")).toBeNull();
    expect(actionTaskKey("chat-1", null)).toBeNull();
    expect(actionTaskKey(undefined, undefined)).toBeNull();
  });
});
