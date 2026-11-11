import type { PartialMessage } from "./PartialMessages";

export type PartialDelta = Partial<PartialMessage>;

type FrameHandle = number | ReturnType<typeof setTimeout>;

function defaultSchedule(cb: () => void): FrameHandle {
    if (typeof requestAnimationFrame === "function") {
        return requestAnimationFrame(() => cb());
    }
    return setTimeout(cb, 16);
}

function defaultCancel(handle: FrameHandle) {
    if (typeof cancelAnimationFrame === "function" && typeof handle === "number") {
        cancelAnimationFrame(handle);
        return;
    }
    clearTimeout(handle as ReturnType<typeof setTimeout>);
}

export interface PartialDeltaQueueOptions {
    schedule?: (cb: () => void) => FrameHandle;
    cancel?: (handle: FrameHandle) => void;
}

interface PendingBatch {
    deltas: PartialDelta[];
    sessionId?: string;
}

/**
 * Coalesces streamed partial-message deltas into at most one commit per
 * animation frame.
 *
 * Websocket frames arrive far faster than the browser can paint: committing
 * every delta straight into the partial-message store triggers a React render
 * per delta. The deltas are still kept individually (the store's per-delta
 * session/seq merge rules depend on that), they are just applied in one `set`
 * at the end of the frame instead of one `set` per delta.
 */
export function createPartialDeltaQueue(
    commit: (chatUUID: string, deltas: PartialDelta[], sessionId?: string) => void,
    options: PartialDeltaQueueOptions = {},
) {
    const schedule = options.schedule ?? defaultSchedule;
    const cancel = options.cancel ?? defaultCancel;

    let pending = new Map<string, PendingBatch>();
    let handle: FrameHandle | null = null;

    const flush = () => {
        handle = null;
        if (pending.size === 0) return;
        const batch = pending;
        pending = new Map();
        for (const [chatUUID, entry] of batch) {
            commit(chatUUID, entry.deltas, entry.sessionId);
        }
    };

    const enqueue = (chatUUID: string, delta: PartialDelta, sessionId?: string) => {
        const existing = pending.get(chatUUID);
        if (existing && existing.sessionId !== sessionId) {
            // A new session started inside the same frame: commit what we have
            // so far before the session guard starts rejecting the new deltas.
            pending.delete(chatUUID);
            commit(chatUUID, existing.deltas, existing.sessionId);
        }
        const batch = pending.get(chatUUID);
        if (batch) {
            batch.deltas.push(delta);
        } else {
            pending.set(chatUUID, { deltas: [delta], sessionId });
        }
        if (handle === null) {
            handle = schedule(flush);
        }
    };

    return {
        enqueue,
        flush,
        /** Drop a pending frame (unmount): the deltas are committed, not lost. */
        dispose() {
            if (handle !== null) {
                cancel(handle);
                handle = null;
            }
            flush();
        },
        /** Number of deltas buffered for a chat (test/debug affordance). */
        pendingCount(chatUUID: string) {
            return pending.get(chatUUID)?.deltas.length ?? 0;
        },
    };
}
