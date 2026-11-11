import { beforeEach, describe, expect, it, vi } from "vitest";

import { usePartialMessageStore, type PartialMessage } from "./PartialMessages";
import {
  createPartialMessageSelector,
  createPartialMessagesForSelector,
  selectAddPartialMessage,
  selectAppendPartialMessage,
  selectRemovePartialMessage,
} from "./PartialMessages.selectors";

const partial = (text: string, sessionId?: string): PartialMessage => ({
  text,
  thoughts: [],
  meta_data: {},
  tool_calls: [],
  session_id: sessionId,
});

describe("partial-message selectors (ci#1031)", () => {
  beforeEach(() => {
    usePartialMessageStore.setState({ partialMessages: {} });
  });

  it("keeps the action selectors referentially stable", () => {
    const before = selectAppendPartialMessage(usePartialMessageStore.getState());
    usePartialMessageStore
      .getState()
      .appendPartialMessage("chat-a", { text: "delta" }, "session-1");
    const after = selectAppendPartialMessage(usePartialMessageStore.getState());

    // A stable action reference cannot trigger a re-render: this is what lets
    // WebsocketHandler subscribe to the store without a whole-store
    // subscription.
    expect(after).toBe(before);
    expect(selectAddPartialMessage(usePartialMessageStore.getState())).toBe(
      usePartialMessageStore.getState().addPartialMessage,
    );
    expect(selectRemovePartialMessage(usePartialMessageStore.getState())).toBe(
      usePartialMessageStore.getState().removePartialMessage,
    );
  });

  it("selects one chat instead of the whole record", () => {
    const chatA = partial("a");
    usePartialMessageStore.setState({ partialMessages: { "chat-a": chatA } });

    const select = createPartialMessageSelector("chat-a");
    const before = select(usePartialMessageStore.getState());
    const recordBefore = usePartialMessageStore.getState().partialMessages;
    usePartialMessageStore.setState({
      partialMessages: { "chat-a": chatA, "chat-b": partial("b") },
    });

    expect(before?.text).toBe("a");
    // one entry keeps its identity ...
    expect(select(usePartialMessageStore.getState())).toBe(before);
    // ... while the whole record, by contrast, is a new object
    expect(usePartialMessageStore.getState().partialMessages).not.toBe(recordBefore);
  });

  it("does not re-render chat A's transcript when chat B streams (render-count regression)", () => {
    usePartialMessageStore.setState({
      partialMessages: { "chat-a": partial("hello", "session-a") },
    });

    const selectA = createPartialMessageSelector("chat-a");
    // Emulate what a subscriber does: re-run the selector on every store write
    // and re-render only when the selection changed identity.
    let rendersOfChatA = 0;
    let selection = selectA(usePartialMessageStore.getState());
    const unsubscribe = usePartialMessageStore.subscribe((state) => {
      const next = selectA(state);
      if (next !== selection) {
        selection = next;
        rendersOfChatA += 1;
      }
    });

    // 20 streamed deltas into chat B, none into chat A
    for (let i = 0; i < 20; i += 1) {
      usePartialMessageStore
        .getState()
        .appendPartialMessage("chat-b", { text: `b${i}` }, "session-b");
    }
    expect(rendersOfChatA).toBe(0);

    // A delta into chat A does re-render it.
    usePartialMessageStore
      .getState()
      .appendPartialMessage("chat-a", { text: " world" }, "session-a");
    expect(rendersOfChatA).toBe(1);
    expect(selection?.text).toBe("hello world");

    unsubscribe();
  });

  it("returns undefined for a missing chat and for a missing uuid", () => {
    expect(createPartialMessageSelector("nope")(usePartialMessageStore.getState())).toBeUndefined();
    expect(createPartialMessageSelector(undefined)(usePartialMessageStore.getState())).toBeUndefined();
  });

  it("picks only the requested chats for a multi-chat selector", () => {
    const state = usePartialMessageStore.getState();
    const picked = createPartialMessagesForSelector(["chat-a", "chat-c"])({
      ...state,
      partialMessages: { "chat-a": partial("a"), "chat-b": partial("b") },
    });

    expect(Object.keys(picked)).toEqual(["chat-a"]);
    expect(picked["chat-a"].text).toBe("a");
  });
});

describe("partial-message store session guard", () => {
  it("ignores a delta from a stale session", () => {
    const store = usePartialMessageStore.getState();
    store.appendPartialMessage("chat-a", { text: "one" }, "session-1");
    const spy = vi.fn();
    const unsubscribe = usePartialMessageStore.subscribe(spy);

    store.appendPartialMessage("chat-a", { text: "stale" }, "session-2");
    unsubscribe();

    expect(usePartialMessageStore.getState().partialMessages["chat-a"].text).toBe("one");
  });
});
