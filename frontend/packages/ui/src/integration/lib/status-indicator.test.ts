import { describe, expect, it } from "vitest";
import {
  BOOTING_STATUS_LABEL,
  QUEUED_STATUS_LABEL,
  isQueuePendingMessage,
  resolveStatusIndicator,
} from "./status-indicator";

const idle = {
  finished: false,
  queuePending: false,
  hasText: false,
  hasThoughts: false,
  toolCallCount: 0,
};

describe("resolveStatusIndicator", () => {
  it("shows the booting placeholder for an empty, in-flight turn", () => {
    expect(resolveStatusIndicator(idle)).toEqual({ kind: "booting", label: BOOTING_STATUS_LABEL });
  });

  it("shows the queued placeholder while the interaction sits in the queue", () => {
    expect(resolveStatusIndicator({ ...idle, queuePending: true })).toEqual({
      kind: "queued",
      label: QUEUED_STATUS_LABEL,
    });
  });

  it("stops showing a placeholder once the turn produced anything", () => {
    expect(resolveStatusIndicator({ ...idle, hasText: true })).toBeNull();
    expect(resolveStatusIndicator({ ...idle, hasThoughts: true })).toBeNull();
    expect(resolveStatusIndicator({ ...idle, toolCallCount: 1 })).toBeNull();
  });

  it("stops showing a placeholder once the turn finished", () => {
    expect(resolveStatusIndicator({ ...idle, finished: true })).toBeNull();
    // A finished, still-queued message also renders nothing.
    expect(resolveStatusIndicator({ ...idle, finished: true, queuePending: true })).toBeNull();
  });
});

describe("isQueuePendingMessage", () => {
  it("recognises the queue_state marker", () => {
    expect(isQueuePendingMessage({ meta_data: { queue_state: "queued" } })).toBe(true);
  });

  it("recognises the synthetic queued placeholder uuid", () => {
    expect(isQueuePendingMessage({ uuid: "queued_1234" })).toBe(true);
  });

  it("is false for a normal message", () => {
    expect(isQueuePendingMessage({ uuid: "msg_1", meta_data: { queue_state: "running" } })).toBe(false);
    expect(isQueuePendingMessage({})).toBe(false);
    expect(isQueuePendingMessage({ meta_data: null })).toBe(false);
  });
});
