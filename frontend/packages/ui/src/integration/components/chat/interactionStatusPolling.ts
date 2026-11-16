// Cadence of the interaction-status poll that drives the chat header badge and
// the active -> inactive revalidate of the transcript. The poll used to run at
// the live cadence (800ms) for the whole lifetime of a chat, and every tick
// re-renders the whole transcript, so a long coder session got slower the
// longer it stayed open (ci#1089). Only poll fast while a turn is running.

export const INTERACTION_STATUS_ACTIVE_INTERVAL_MS = 800;

export const INTERACTION_STATUS_IDLE_INTERVAL_MS = 5000;

// A hidden tab does not need any polling at all; SWR revalidates on focus.
export const INTERACTION_STATUS_HIDDEN_INTERVAL_MS = 0;

// After the user sends a message the interaction has not flipped to active yet,
// so a backed-off poll would miss the active window of a short turn - and with
// it the active -> inactive refetch that lands the final bot message. Stay on
// the live cadence for this bounded window instead of forever.
export const INTERACTION_STATUS_POST_SEND_FAST_WINDOW_MS = 60_000;

export type InteractionStatusLike = { is_active?: boolean } | null | undefined;

export type InteractionStatusPollOptions = {
    /** The tab is in the background, so nothing needs to be polled. */
    documentHidden?: boolean;
    /** Timestamp (ms) until which a just-sent message keeps the live cadence. */
    postSendFastUntil?: number;
    /** Injectable clock, for tests. */
    now?: number;
};

// interactionStatusRefreshInterval maps the last known status, the tab
// visibility and the post-send window to a poll cadence: fast while a turn is
// running, while the status is still unknown, or briefly after a send; slow
// once the interaction went inactive; and off while the tab is in the
// background.
export function interactionStatusRefreshInterval(
    status: InteractionStatusLike,
    options: InteractionStatusPollOptions = {},
): number {
    const { documentHidden = false, postSendFastUntil, now = Date.now() } = options;
    if (documentHidden) {
        return INTERACTION_STATUS_HIDDEN_INTERVAL_MS;
    }
    if (postSendFastUntil !== undefined && postSendFastUntil > now) {
        return INTERACTION_STATUS_ACTIVE_INTERVAL_MS;
    }
    if (status && status.is_active === false) {
        return INTERACTION_STATUS_IDLE_INTERVAL_MS;
    }
    return INTERACTION_STATUS_ACTIVE_INTERVAL_MS;
}
