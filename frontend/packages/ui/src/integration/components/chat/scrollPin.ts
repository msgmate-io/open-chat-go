interface ScrollableElement {
    scrollHeight: number;
    clientHeight: number;
    scrollTop: number;
}

export interface ScrollPinOptions {
    getElement: () => ScrollableElement | null;
    shouldPin: () => boolean;
    requestFrame?: (cb: () => void) => number;
    cancelFrame?: (handle: number) => void;
}

function defaultRequestFrame(cb: () => void): number {
    if (typeof requestAnimationFrame === "function") {
        return requestAnimationFrame(() => cb());
    }
    return setTimeout(cb, 16) as unknown as number;
}

function defaultCancelFrame(handle: number) {
    if (typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(handle);
        return;
    }
    clearTimeout(handle as unknown as ReturnType<typeof setTimeout>);
}

/**
 * Keeps the transcript pinned to the newest row without running a scroll write
 * per streamed token.
 *
 * Streaming produces many state updates per second; each one used to force a
 * synchronous `scrollTop` write (a layout-flushing read+write). Scheduling at
 * most one write per animation frame lets the browser lay out once and paint
 * once. Nothing is scheduled at all while the user has scrolled away from the
 * bottom, so reading history is never interrupted.
 */
export function createScrollPin(options: ScrollPinOptions) {
    const requestFrame = options.requestFrame ?? defaultRequestFrame;
    const cancelFrame = options.cancelFrame ?? defaultCancelFrame;

    let handle: number | null = null;

    const run = () => {
        handle = null;
        if (!options.shouldPin()) return;
        const element = options.getElement();
        if (!element) return;
        element.scrollTop = Math.max(0, element.scrollHeight - element.clientHeight);
    };

    return {
        /** Schedule a pin scroll for the next frame; returns whether one was queued. */
        schedule() {
            if (!options.shouldPin()) return false;
            if (handle !== null) return false;
            handle = requestFrame(run);
            return true;
        },
        cancel() {
            if (handle === null) return;
            cancelFrame(handle);
            handle = null;
        },
        isScheduled() {
            return handle !== null;
        },
    };
}
