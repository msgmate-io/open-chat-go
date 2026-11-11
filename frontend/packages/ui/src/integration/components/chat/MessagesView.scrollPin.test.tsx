import { describe, expect, it } from "vitest";

import { createScrollPin } from "./scrollPin";

/*
 * ci#1031 item 4: the transcript is pinned to the newest row by ONE
 * rAF-coalesced scroll write instead of a `scrollTop` write per state update,
 * and the pin only runs while the user is still at the bottom.
 */
function makeElement(scrollHeight = 1000, clientHeight = 400, scrollTop = 0) {
  return { scrollHeight, clientHeight, scrollTop };
}

function makeFrames() {
  const queue = new Map<number, () => void>();
  let next = 1;
  return {
    requestFrame: (cb: () => void) => {
      const handle = next;
      next += 1;
      queue.set(handle, cb);
      return handle;
    },
    cancelFrame: (handle: number) => {
      queue.delete(handle);
    },
    runFrame() {
      const run = [...queue.values()];
      queue.clear();
      run.forEach((cb) => cb());
    },
    pending() {
      return queue.size;
    },
  };
}

describe("MessagesView scroll pin (ci#1031)", () => {
  it("coalesces a burst of updates into one scroll write per frame", () => {
    const frames = makeFrames();
    const element = makeElement();
    let writes = 0;
    const pin = createScrollPin({
      getElement: () => element,
      shouldPin: () => true,
      requestFrame: frames.requestFrame,
      cancelFrame: frames.cancelFrame,
    });

    // 30 streamed deltas within one frame
    for (let i = 0; i < 30; i += 1) pin.schedule();
    expect(frames.pending()).toBe(1);

    // grow the content before the frame runs, then observe a single write
    element.scrollHeight = 1600;
    const setterCalls: number[] = [];
    Object.defineProperty(element, "scrollTop", {
      get: () => 0,
      set: (value: number) => {
        writes += 1;
        setterCalls.push(value);
      },
    });

    frames.runFrame();

    expect(writes).toBe(1);
    expect(setterCalls).toEqual([1200]);
    expect(pin.isScheduled()).toBe(false);
  });

  it("never schedules while the user has scrolled away from the bottom", () => {
    const frames = makeFrames();
    const element = makeElement();
    let pinned = false;
    const pin = createScrollPin({
      getElement: () => element,
      shouldPin: () => pinned,
      requestFrame: frames.requestFrame,
      cancelFrame: frames.cancelFrame,
    });

    expect(pin.schedule()).toBe(false);
    expect(frames.pending()).toBe(0);

    // a pinned transcript schedules, and honours the pin state at frame time
    pinned = true;
    expect(pin.schedule()).toBe(true);
    pinned = false;
    const before = element.scrollTop;
    frames.runFrame();
    expect(element.scrollTop).toBe(before);
  });

  it("re-pins on the first update after the user returns to the bottom", () => {
    const frames = makeFrames();
    const element = makeElement(900, 400);
    let pinned = false;
    const pin = createScrollPin({
      getElement: () => element,
      shouldPin: () => pinned,
      requestFrame: frames.requestFrame,
      cancelFrame: frames.cancelFrame,
    });

    expect(pin.schedule()).toBe(false);
    pinned = true;
    expect(pin.schedule()).toBe(true);
    frames.runFrame();
    expect(element.scrollTop).toBe(500);
  });

  it("cancels a queued frame on unmount so no write happens afterwards", () => {
    const frames = makeFrames();
    const element = makeElement();
    const pin = createScrollPin({
      getElement: () => element,
      shouldPin: () => true,
      requestFrame: frames.requestFrame,
      cancelFrame: frames.cancelFrame,
    });

    pin.schedule();
    pin.cancel();
    expect(pin.isScheduled()).toBe(false);

    let writes = 0;
    Object.defineProperty(element, "scrollTop", {
      get: () => 0,
      set: () => {
        writes += 1;
      },
    });
    frames.runFrame();
    expect(writes).toBe(0);
  });

  it("tolerates a missing element while the transcript is still mounting", () => {
    const pin = createScrollPin({
      getElement: () => null,
      shouldPin: () => true,
      requestFrame: () => 1,
      cancelFrame: () => {},
    });
    expect(() => pin.schedule()).not.toThrow();
  });
});
