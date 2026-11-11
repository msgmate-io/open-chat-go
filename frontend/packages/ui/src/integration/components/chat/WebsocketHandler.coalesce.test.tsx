import { beforeEach, describe, expect, it } from "vitest";

import { usePartialMessageStore } from "./PartialMessages";
import { createPartialDeltaQueue } from "./partialDeltaQueue";

/*
 * ci#1031 item 3: the coalescing pipeline WebsocketHandler uses for
 * `new_partial_message` frames.
 *
 * Websocket frames arrive far faster than the browser paints. The handler still
 * applies every delta individually (the store's per-delta session/seq merge
 * rules depend on that), but they are batched into at most one commit per
 * animation frame, so React commits once per frame instead of once per delta.
 *
 * This exercises the same wiring the handler builds (queue + store actions)
 * with a manual frame scheduler, which keeps it a pure Node test.
 */
function makeFrames() {
  let queued: Array<() => void> = [];
  return {
    requestFrame: (cb: () => void) => {
      queued.push(cb);
      return queued.length;
    },
    cancelFrame: () => {},
    runFrame() {
      const run = queued;
      queued = [];
      run.forEach((cb) => cb());
    },
    queuedCount() {
      return queued.length;
    },
  };
}

function flushCommit(chatUUID: string, deltas: Array<{ text?: string; seq?: number }>, sessionId?: string) {
  for (const delta of deltas) {
    usePartialMessageStore.getState().appendPartialMessage(chatUUID, delta, sessionId);
  }
  commitCalls.push({ chatUUID, count: deltas.length });
}

let commitCalls: Array<{ chatUUID: string; count: number }> = [];

describe("WebsocketHandler delta coalescing (ci#1031)", () => {
  beforeEach(() => {
    usePartialMessageStore.setState({ partialMessages: {} });
  });

  it("commits many deltas of one frame in a single commit", () => {
    commitCalls = [];
    const frames = makeFrames();
    const queue = createPartialDeltaQueue(flushCommit, {
      schedule: frames.requestFrame,
      cancel: frames.cancelFrame,
    });

    for (const chunk of ["a", "b", "c", "d", "e"]) {
      queue.enqueue("chat-a", { text: chunk }, "session-1");
    }

    // nothing reaches the store before the frame runs
    expect(queue.pendingCount("chat-a")).toBe(5);
    expect(commitCalls).toHaveLength(0);
    expect(frames.queuedCount()).toBe(1);

    frames.runFrame();

    // one commit for the whole frame's worth of text; every delta is still
    // applied individually so the session/seq merge rules keep working
    expect(commitCalls).toEqual([{ chatUUID: "chat-a", count: 5 }]);
    expect(usePartialMessageStore.getState().partialMessages["chat-a"].text).toBe("abcde");
    expect(queue.pendingCount("chat-a")).toBe(0);
  });

  it("keeps chats in separate batches and coalesces one frame per burst", () => {
    const frames = makeFrames();
    const queue = createPartialDeltaQueue(flushCommit, {
      schedule: frames.requestFrame,
      cancel: frames.cancelFrame,
    });

    queue.enqueue("chat-a", { text: "a" }, "session-a");
    queue.enqueue("chat-b", { text: "b" }, "session-b");
    // a second burst inside the same frame must not schedule another frame
    queue.enqueue("chat-a", { text: "a2" }, "session-a");
    expect(frames.queuedCount()).toBe(1);

    frames.runFrame();
    const state = usePartialMessageStore.getState().partialMessages;
    expect(state["chat-a"].text).toBe("aa2");
    expect(state["chat-b"].text).toBe("b");
  });

  it("commits a pending batch before a new session starts inside the frame", () => {
    const frames = makeFrames();
    const queue = createPartialDeltaQueue(flushCommit, {
      schedule: frames.requestFrame,
      cancel: frames.cancelFrame,
    });

    queue.enqueue("chat-a", { text: "old" }, "session-old");
    queue.enqueue("chat-a", { text: "new" }, "session-new");
    // the old session was committed eagerly, the new one is still buffered
    expect(usePartialMessageStore.getState().partialMessages["chat-a"].text).toBe("old");
    expect(queue.pendingCount("chat-a")).toBe(1);

    frames.runFrame();
    // the store's session guard rejects the delta from the replaced session
    expect(usePartialMessageStore.getState().partialMessages["chat-a"].text).toBe("old");
  });

  it("flushes the buffered tail instead of dropping it on teardown", () => {
    const frames = makeFrames();
    const queue = createPartialDeltaQueue(flushCommit, {
      schedule: frames.requestFrame,
      cancel: frames.cancelFrame,
    });

    queue.enqueue("chat-a", { text: "tail" }, "session-1");
    queue.dispose();

    expect(usePartialMessageStore.getState().partialMessages["chat-a"].text).toBe("tail");
    expect(queue.pendingCount("chat-a")).toBe(0);
  });
});
