import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ChatStateDot } from "./ChatItem";
import {
  CHAT_LIST_PAGE_SIZE,
  CHAT_STATES_ACTIVE_REFRESH_MS,
  CHAT_STATES_IDLE_REFRESH_MS,
  chatStatesRefreshInterval,
  collectAutomatedChatUuids,
  hasMoreChats,
} from "./chat-list-helpers";

describe("chat list fetch-pagination (ci#579)", () => {
  it("uses a bounded first page", () => {
    expect(CHAT_LIST_PAGE_SIZE).toBeGreaterThan(0);
    expect(CHAT_LIST_PAGE_SIZE).toBeLessThanOrEqual(50);
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
