import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ChatStateDot } from "./ChatItem";
import {
  CHAT_LIST_FETCH_BATCH_SIZE,
  CHAT_LIST_INITIAL_BATCHES,
  CHAT_LIST_PAGE_SIZE,
  CHAT_STATES_ACTIVE_REFRESH_MS,
  CHAT_STATES_IDLE_REFRESH_MS,
  CHAT_STATES_MAX_CHATS,
  batchesForPaintedPages,
  buildChatListQuery,
  buildChatListUrl,
  chatStatesRefreshInterval,
  chatStatesRequestUuids,
  collectAutomatedChatUuids,
  hasMoreChats,
  mergeChatPages,
  paintedRowBudget,
  applyPinnedToChatListValue,
} from "./chat-list-helpers";

describe("chat list fetch-pagination (ci#579)", () => {
  it("uses a bounded first page", () => {
    expect(CHAT_LIST_PAGE_SIZE).toBeGreaterThan(0);
    expect(CHAT_LIST_PAGE_SIZE).toBeLessThanOrEqual(50);
  });

  it("paints the first window and grows it a page at a time", () => {
    expect(paintedRowBudget(1)).toBe(CHAT_LIST_PAGE_SIZE);
    expect(paintedRowBudget(2)).toBe(CHAT_LIST_PAGE_SIZE * 2);
    // A non-positive page count still paints the first window instead of
    // collapsing the list.
    expect(paintedRowBudget(0)).toBe(CHAT_LIST_PAGE_SIZE);
  });

  it("fetches the window in small incremental batches", () => {
    expect(CHAT_LIST_FETCH_BATCH_SIZE).toBeGreaterThan(0);
    expect(CHAT_LIST_FETCH_BATCH_SIZE).toBeLessThan(CHAT_LIST_PAGE_SIZE);
    // A single request is one batch, so the first paint needs several requests.
    expect(batchesForPaintedPages(1)).toBe(CHAT_LIST_INITIAL_BATCHES);
    expect(CHAT_LIST_INITIAL_BATCHES).toBeGreaterThan(1);
    // Growing the painted window pulls at least one more batch, never fewer.
    expect(batchesForPaintedPages(2)).toBeGreaterThanOrEqual(batchesForPaintedPages(1));
    expect(batchesForPaintedPages(0)).toBeGreaterThanOrEqual(1);
  });

  it("reports more chats while the total exceeds the loaded rows", () => {
    expect(hasMoreChats({ total: 100, rowCount: 20, limit: 20 })).toBe(true);
    expect(hasMoreChats({ total: 20, rowCount: 20, limit: 20 })).toBe(false);
    // The pinned top-up can push the row count past the limit without there
    // being older chats left to load.
    expect(hasMoreChats({ total: 25, rowCount: 25, limit: 20 })).toBe(false);
  });

  it("falls back to the full-page heuristic when the backend omits a total", () => {
    expect(hasMoreChats({ total: undefined, rowCount: 20, limit: 20 })).toBe(true);
    expect(hasMoreChats({ total: undefined, rowCount: 7, limit: 20 })).toBe(false);
  });
});

describe("chat list page merging (ci#579)", () => {
  it("concatenates the fetched batches newest-first", () => {
    const merged = mergeChatPages([
      { rows: [{ uuid: "a" }, { uuid: "b" }], total: 4 },
      { rows: [{ uuid: "c" }, { uuid: "d" }] },
    ]);
    expect(merged.rows.map((row) => row.uuid)).toEqual(["a", "b", "c", "d"]);
    expect(merged.total).toBe(4);
  });

  it("de-duplicates the pinned top-up that reappears in a later batch", () => {
    const merged = mergeChatPages([
      { rows: [{ uuid: "pin" }, { uuid: "a" }], total: 3 },
      // Page two's offset window can overlap with the pins page one added.
      { rows: [{ uuid: "a" }, { uuid: "b" }] },
    ]);
    expect(merged.rows.map((row) => row.uuid)).toEqual(["pin", "a", "b"]);
  });

  it("handles an empty or missing response", () => {
    expect(mergeChatPages(undefined)).toEqual({ rows: [], total: undefined });
    expect(mergeChatPages([undefined as never])).toEqual({ rows: [], total: undefined });
  });
});

describe("batch chat-state request (ci#579)", () => {
  it("caps the requested uuids at what the endpoint can resolve", () => {
    expect(CHAT_STATES_MAX_CHATS).toBeGreaterThan(0);
    const uuids = Array.from({ length: CHAT_STATES_MAX_CHATS + 25 }, (_, index) => `chat-${index}`);
    const requested = chatStatesRequestUuids(uuids);
    expect(requested).toHaveLength(CHAT_STATES_MAX_CHATS);
    // Newest first: the tail beyond the cap is dropped, not the head.
    expect(requested[0]).toBe("chat-0");
  });

  it("keeps every uuid when the list fits", () => {
    expect(chatStatesRequestUuids(["a", "b"], 10)).toEqual(["a", "b"]);
    expect(chatStatesRequestUuids(["a"], 0)).toEqual([]);
  });
});

describe("chat state indicator polling (ci#579)", () => {
  it("polls fast only while an interaction is live", () => {
    expect(chatStatesRefreshInterval([{ state: "active" }], false)).toBe(CHAT_STATES_ACTIVE_REFRESH_MS);
    expect(chatStatesRefreshInterval([{ state: "needs_confirmation" }], false)).toBe(
      CHAT_STATES_ACTIVE_REFRESH_MS,
    );
  });

  it("backs off when every interaction is settled or nothing is listed", () => {
    expect(chatStatesRefreshInterval([{ state: "idle" }, { state: "finished" }], false)).toBe(
      CHAT_STATES_IDLE_REFRESH_MS,
    );
    expect(chatStatesRefreshInterval(undefined, false)).toBe(CHAT_STATES_IDLE_REFRESH_MS);
  });

  it("stops polling entirely while the tab is hidden", () => {
    expect(chatStatesRefreshInterval([{ state: "active" }], true)).toBe(0);
  });
});

describe("automated chat detection", () => {
  it("collects only automated chats with a uuid", () => {
    const set = collectAutomatedChatUuids([
      { uuid: "a", partner: { is_automated: true } },
      { uuid: "b", partner: { is_automated: false } },
      { uuid: "c" },
      { partner: { is_automated: true } },
    ]);
    expect([...set]).toEqual(["a"]);
  });
});

describe("pin patching of the cached chat list (ci#579)", () => {
  it("patches a single cached page", () => {
    const patched: any = applyPinnedToChatListValue(
      { rows: [{ uuid: "a", pinned: false }, { uuid: "b" }], total: 2 },
      "a",
      true,
    );
    expect(patched.rows[0]).toEqual({ uuid: "a", pinned: true });
    expect(patched.rows[1]).toEqual({ uuid: "b" });
    expect(patched.total).toBe(2);
  });

  it("patches every page of the paginated (infinite) cache", () => {
    const patched: any = applyPinnedToChatListValue(
      [
        { rows: [{ uuid: "pin" }], total: 3 },
        { rows: [{ uuid: "pin" }, { uuid: "a" }] },
      ],
      "pin",
      true,
    );
    expect(patched[0].rows[0].pinned).toBe(true);
    expect(patched[1].rows[0].pinned).toBe(true);
    expect(patched[1].rows[1]).toEqual({ uuid: "a" });
  });

  it("is a no-op for values without rows", () => {
    expect(applyPinnedToChatListValue(undefined, "a", true)).toBeUndefined();
    expect(applyPinnedToChatListValue(null, "a", true)).toBeNull();
    expect(applyPinnedToChatListValue(42, "a", true)).toBe(42);
    const untouched = { page: 1 };
    expect(applyPinnedToChatListValue(untouched, "a", true)).toBe(untouched);
  });
});

describe("chat list state filter is part of the request key (ci#761)", () => {
  it("omits the state parameter when no state is selected", () => {
    const query = buildChatListQuery({ page: 1, limit: 10, chatTypes: "bot" });
    expect(query).not.toContain("state=");
    // Pinned chats are ordinary rows, so the default request must not change
    // shape just because state filtering exists.
    expect(buildChatListUrl({ page: 1, limit: 10 })).toBe("/api/v1/chats/list?page=1&limit=10");
  });

  it("serializes the selected state into the request URL", () => {
    const url = buildChatListUrl({ page: 2, limit: 5, state: "needs_confirmation" });
    expect(url).toContain("state=needs_confirmation");
    expect(new URLSearchParams(url.split("?")[1]).get("state")).toBe("needs_confirmation");
  });

  it("produces a different key per state so SWR refetches page one", () => {
    const base = { page: 1, limit: CHAT_LIST_FETCH_BATCH_SIZE };
    const keys = ["active", "needs_confirmation", "finished", "failed", "idle", null].map((state) =>
      buildChatListUrl({ ...base, state }),
    );
    // Six distinct keys: every state (plus "no filter") forces its own fetch
    // instead of reusing a cached page loaded under another filter.
    expect(new Set(keys).size).toBe(keys.length);
    expect(new Set(keys.map((key) => key.split("?")[1]?.includes("state=") ?? false)).size).toBe(2);
  });

  it("keeps the state filter alongside the other server-side filters", () => {
    const params = new URLSearchParams(
      buildChatListQuery({
        page: 3,
        limit: 20,
        chatTypes: "bot",
        search: "hello",
        tags: ["a", "b"],
        excludeTags: ["automation"],
        timeFrom: "2024-01-01T00:00:00Z",
        state: "failed",
        scope: "all",
      }),
    );
    expect(params.get("state")).toBe("failed");
    expect(params.get("chat_types")).toBe("bot");
    expect(params.get("q")).toBe("hello");
    expect(params.get("tags")).toBe("a,b");
    expect(params.get("exclude_tags")).toBe("automation");
    expect(params.get("time_from")).toBe("2024-01-01T00:00:00Z");
    expect(params.get("scope")).toBe("all");
    expect(params.get("page")).toBe("3");
    expect(params.get("limit")).toBe("20");
  });
});

describe("ChatStateDot", () => {
  it("renders nothing for a non-tracked chat", () => {
    const html = renderToStaticMarkup(<ChatStateDot state="active" />);
    expect(html).toBe("");
  });

  it("reserves a placeholder slot while an automated chat state loads", () => {
    const html = renderToStaticMarkup(<ChatStateDot tracked loading />);
    expect(html).toContain('data-chat-state-placeholder="true"');
    expect(html).toContain("animate-pulse");
  });

  it("keeps the slot reserved for an idle automated chat after loading", () => {
    const html = renderToStaticMarkup(<ChatStateDot state="idle" tracked />);
    expect(html).toContain("chat-state-dot");
    expect(html).not.toContain("data-chat-state-placeholder");
  });

  it("colors the dot for a resolved live state", () => {
    const html = renderToStaticMarkup(<ChatStateDot state="active" tracked />);
    expect(html).toContain("bg-yellow-400");
    expect(html).toContain("Bot interaction running");
  });
});
