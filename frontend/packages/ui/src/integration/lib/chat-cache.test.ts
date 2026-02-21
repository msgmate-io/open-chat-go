import { describe, expect, it } from "vitest";
import {
  isActionTasksKey,
  isChatListKey,
  isChatStatesKey,
  normalizeCacheKey,
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
