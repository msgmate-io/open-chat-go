import { describe, expect, it, vi } from "vitest";

import { createPartialDeltaQueue } from "./partialDeltaQueue";

function makeHarness() {
    const commits: Array<{ chatUUID: string; deltas: any[]; sessionId?: string }> = [];
    let frame: (() => void) | null = null;
    let scheduled = 0;

    const queue = createPartialDeltaQueue(
        (chatUUID, deltas, sessionId) => {
            commits.push({ chatUUID, deltas, sessionId });
        },
        {
            schedule: (cb) => {
                scheduled += 1;
                frame = cb;
                return scheduled;
            },
            cancel: () => {
                frame = null;
            },
        },
    );

    return {
        queue,
        commits,
        runFrame() {
            const cb = frame;
            frame = null;
            cb?.();
        },
        get scheduled() {
            return scheduled;
        },
    };
}

describe("createPartialDeltaQueue", () => {
    it("keeps every delta but commits them once per frame", () => {
        const h = makeHarness();

        h.queue.enqueue("chat-a", { text: "Hel" }, "session-1");
        h.queue.enqueue("chat-a", { text: "lo" }, "session-1");
        h.queue.enqueue("chat-a", { text: "!" }, "session-1");

        // nothing committed before the frame runs
        expect(h.commits).toHaveLength(0);
        // and only one frame was scheduled for the whole burst
        expect(h.scheduled).toBe(1);

        h.runFrame();

        expect(h.commits).toHaveLength(1);
        expect(h.commits[0].chatUUID).toBe("chat-a");
        expect(h.commits[0].sessionId).toBe("session-1");
        // the per-delta allocations the seq/session merge logic depends on are kept
        expect(h.commits[0].deltas).toEqual([{ text: "Hel" }, { text: "lo" }, { text: "!" }]);
    });

    it("batches per chat so a delta to chat B cannot re-render chat A", () => {
        const h = makeHarness();

        h.queue.enqueue("chat-a", { text: "a" }, "session-a");
        h.queue.enqueue("chat-b", { text: "b" }, "session-b");
        h.runFrame();

        expect(h.commits.map((c) => c.chatUUID)).toEqual(["chat-a", "chat-b"]);
        expect(h.commits[0].deltas).toEqual([{ text: "a" }]);
        expect(h.commits[1].deltas).toEqual([{ text: "b" }]);
        expect(h.queue.pendingCount("chat-a")).toBe(0);
    });

    it("flushes the previous session before a new session starts in the same frame", () => {
        const h = makeHarness();

        h.queue.enqueue("chat-a", { text: "old" }, "session-1");
        h.queue.enqueue("chat-a", { text: "new" }, "session-2");

        // the session switch forces an immediate commit so the store's session
        // guard cannot reject the old session's deltas
        expect(h.commits).toHaveLength(1);
        expect(h.commits[0].sessionId).toBe("session-1");
        expect(h.commits[0].deltas).toEqual([{ text: "old" }]);

        h.runFrame();
        expect(h.commits).toHaveLength(2);
        expect(h.commits[1].sessionId).toBe("session-2");
        expect(h.commits[1].deltas).toEqual([{ text: "new" }]);
    });

    it("dispose commits buffered deltas instead of dropping them", () => {
        const h = makeHarness();

        h.queue.enqueue("chat-a", { text: "tail" }, "session-1");
        expect(h.queue.pendingCount("chat-a")).toBe(1);

        h.queue.dispose();

        expect(h.commits).toHaveLength(1);
        expect(h.commits[0].deltas).toEqual([{ text: "tail" }]);
        expect(h.queue.pendingCount("chat-a")).toBe(0);
    });

    it("flush is a no-op when nothing is buffered", () => {
        const h = makeHarness();
        const commit = vi.fn();
        h.queue.flush();
        expect(h.commits).toHaveLength(0);
        expect(commit).not.toHaveBeenCalled();
    });
});
