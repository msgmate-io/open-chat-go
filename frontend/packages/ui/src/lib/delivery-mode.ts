/**
 * ci#656 — bot reply delivery mode, frontend side.
 *
 * The backend accepts `delivery_mode` on `POST /chats/<uuid>/messages` and
 * persists a `queue_state: "queued"` marker on messages parked behind a running
 * bot turn (see `backend/database/queued_marker.go`). This module is the single
 * place the UI decides *how* a send is delivered and *how* a parked message is
 * labelled, so the composer, the send path and the transcript all agree on one
 * rule instead of re-deriving it.
 *
 * Pure and dependency free on purpose: it is unit tested without a DOM.
 */

export type BotReplyDeliveryMode = "queued" | "interrupt";

/** Accepted `delivery_mode` values, mirroring the server's validation. */
export const BOT_REPLY_DELIVERY_MODES: readonly BotReplyDeliveryMode[] = [
  "queued",
  "interrupt",
];

export function isBotReplyDeliveryMode(
  value: unknown,
): value is BotReplyDeliveryMode {
  return (
    typeof value === "string" &&
    (BOT_REPLY_DELIVERY_MODES as readonly string[]).includes(value)
  );
}

/** Label of the "send immediately" affordance shown while the bot is replying. */
export const SEND_NOW_LABEL = "Send now";

/** Label rendered under a message that is persisted but still waiting its turn. */
export const QUEUED_REPLY_HINT_LABEL = "Queued — sends after the current reply";

/** `meta_data.queue_state` value the server writes for a parked message. */
export const QUEUED_MARKER_STATE = "queued";

/** Prefix of the synthetic placeholder message for a parked reply. */
export const SYNTHETIC_QUEUED_PREFIX = "queued_";

export type SendMessageAttachment = { file_id: string; display_name?: string };

export type SendMessageBodyInput = {
  text?: string;
  attachments?: SendMessageAttachment[];
  toolInit?: Record<string, unknown> | null;
  deliveryMode?: BotReplyDeliveryMode | string | null;
};

export type SendMessageBody = {
  text?: string;
  attachments?: SendMessageAttachment[];
  toolInit?: Record<string, unknown>;
  delivery_mode?: BotReplyDeliveryMode;
};

/**
 * Builds the request body for a send.
 *
 * Empty fields stay omitted so the payload shape is unchanged for every
 * existing caller, and an unknown `deliveryMode` is dropped rather than
 * forwarded (the server rejects unknown values with `400 Invalid delivery_mode`;
 * silently omitting keeps the old default `queued` behaviour instead of failing
 * the send).
 */
export function buildSendMessageBody(
  input: SendMessageBodyInput,
): SendMessageBody {
  const body: SendMessageBody = {};

  const text = typeof input.text === "string" ? input.text : "";
  if (text.length > 0) body.text = text;

  if (input.attachments && input.attachments.length > 0)
    body.attachments = input.attachments;

  if (input.toolInit && Object.keys(input.toolInit).length > 0) {
    body.toolInit = input.toolInit;
  }

  if (isBotReplyDeliveryMode(input.deliveryMode)) {
    body.delivery_mode = input.deliveryMode;
  }

  return body;
}

/**
 * Whether to offer "Send now" instead of a plain send.
 *
 * Only meaningful while a bot turn is live *and* there is something sendable:
 * an empty composer must not surface the affordance.
 */
export function shouldOfferSendNow(input: {
  isBotResponding: boolean;
  hasSendableContent: boolean;
}): boolean {
  return Boolean(input.isBotResponding && input.hasSendableContent);
}

/**
 * Hint for a message that is persisted but not delivered yet, or `null`.
 *
 * Mirrors `isQueuePendingMessage` in `integration/lib/status-indicator.ts`: a
 * live `queue_state === "queued"` marker, or the synthetic `queued_<uuid>`
 * placeholder the server uses before the real message exists.
 */
export function resolveQueuedReplyHint(
  message: {
    uuid?: string;
    meta_data?: { queue_state?: string } | null;
  } | null,
): string | null {
  if (!message) return null;

  const queueState = message.meta_data?.queue_state;
  const uuid = typeof message.uuid === "string" ? message.uuid : "";
  const pending =
    queueState === QUEUED_MARKER_STATE ||
    uuid.startsWith(SYNTHETIC_QUEUED_PREFIX);

  return pending ? QUEUED_REPLY_HINT_LABEL : null;
}
