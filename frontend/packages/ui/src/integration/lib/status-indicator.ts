// Assistant "booting" / "queued" status resolution.
//
// While a bot turn has produced no text, no reasoning and no tool calls yet, the
// transcript shows a placeholder status: "queued" while the interaction sits in
// the provider queue, "booting" once it is being picked up. The core renders a
// default indicator; a chat-backend integration can replace it with a compact,
// on-brand one (see `ChatUIExtension.StatusIndicator`). The decision is pure so
// it can be unit-tested without a DOM.

export type StatusIndicatorKind = "booting" | "queued";

export interface StatusIndicatorState {
  kind: StatusIndicatorKind;
  label: string;
}

export interface StatusIndicatorInput {
  /** True once the message is marked finished (no placeholder anymore). */
  finished: boolean;
  /** True while the interaction is held in the provider queue. */
  queuePending: boolean;
  /** True when the turn already produced assistant text. */
  hasText: boolean;
  /** True when the turn already produced reasoning/thoughts. */
  hasThoughts: boolean;
  /** Number of tool calls the turn already emitted. */
  toolCallCount: number;
}

export const BOOTING_STATUS_LABEL = "Msgmate Booting";
export const QUEUED_STATUS_LABEL = "Msgmate Agent Queued";

// resolveStatusIndicator returns the placeholder to show, or null once the turn
// has anything to render (text, thoughts or a tool call) or has finished.
export function resolveStatusIndicator(input: StatusIndicatorInput): StatusIndicatorState | null {
  if (input.finished) {
    return null;
  }
  if (input.hasText || input.hasThoughts || input.toolCallCount > 0) {
    return null;
  }
  return input.queuePending
    ? { kind: "queued", label: QUEUED_STATUS_LABEL }
    : { kind: "booting", label: BOOTING_STATUS_LABEL };
}

// isQueuePendingMessage mirrors the server's queue markers: a live queue_state
// of "queued", or the synthetic `queued_<chatUUID>` placeholder message.
export function isQueuePendingMessage(message: {
  uuid?: string;
  meta_data?: { queue_state?: string } | null;
}): boolean {
  const queueState = message?.meta_data?.queue_state;
  const uuid = typeof message?.uuid === "string" ? message.uuid : "";
  return queueState === "queued" || uuid.startsWith("queued_");
}
