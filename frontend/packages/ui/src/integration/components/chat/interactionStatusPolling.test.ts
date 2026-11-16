import { describe, expect, it } from "vitest";
import {
    INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
    INTERACTION_STATUS_HIDDEN_INTERVAL_MS,
    INTERACTION_STATUS_IDLE_INTERVAL_MS,
    INTERACTION_STATUS_POST_SEND_FAST_WINDOW_MS,
    interactionStatusRefreshInterval,
} from "./interactionStatusPolling";

// Regression test for ci#1089: the transcript poll ran at the live cadence
// (800ms) for the whole lifetime of the chat, re-fetching and re-rendering the
// whole transcript forever, which made a long coder session slower and slower.
// Before the fix there was no cadence hand-off at all: every state - including
// a finished interaction and a backgrounded tab - resolved to 800ms.
describe("interactionStatusRefreshInterval", () => {
    it("polls fast while a turn is running", () => {
        expect(interactionStatusRefreshInterval({ is_active: true })).toBe(
            INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
        );
    });

    it("polls fast while the status is not known yet", () => {
        expect(interactionStatusRefreshInterval(null)).toBe(
            INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
        );
        expect(interactionStatusRefreshInterval(undefined)).toBe(
            INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
        );
    });

    it("backs off once the interaction finished", () => {
        expect(interactionStatusRefreshInterval({ is_active: false })).toBe(
            INTERACTION_STATUS_IDLE_INTERVAL_MS,
        );
        // The regression: an idle chat must not keep the live cadence, or the
        // poll re-renders the growing transcript every 800ms forever.
        expect(interactionStatusRefreshInterval({ is_active: false })).not.toBe(
            INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
        );
        expect(INTERACTION_STATUS_IDLE_INTERVAL_MS).toBeGreaterThan(
            INTERACTION_STATUS_ACTIVE_INTERVAL_MS,
        );
    });

    it("stops polling while the tab is hidden", () => {
        expect(
            interactionStatusRefreshInterval(
                { is_active: true },
                { documentHidden: true },
            ),
        ).toBe(INTERACTION_STATUS_HIDDEN_INTERVAL_MS);
        expect(INTERACTION_STATUS_HIDDEN_INTERVAL_MS).toBe(0);
    });

    it("keeps the live cadence for a bounded window after a send", () => {
        const now = 1_000_000;
        const fastUntil = now + INTERACTION_STATUS_POST_SEND_FAST_WINDOW_MS;

        // A just-sent message must still see the active window of a short turn.
        expect(
            interactionStatusRefreshInterval(
                { is_active: false },
                { postSendFastUntil: fastUntil, now },
            ),
        ).toBe(INTERACTION_STATUS_ACTIVE_INTERVAL_MS);

        // ...but only for a bounded window, then it backs off again.
        expect(
            interactionStatusRefreshInterval(
                { is_active: false },
                { postSendFastUntil: fastUntil, now: fastUntil + 1 },
            ),
        ).toBe(INTERACTION_STATUS_IDLE_INTERVAL_MS);

        // The window never overrides a hidden tab.
        expect(
            interactionStatusRefreshInterval(
                { is_active: true },
                { postSendFastUntil: fastUntil, documentHidden: true, now },
            ),
        ).toBe(INTERACTION_STATUS_HIDDEN_INTERVAL_MS);
    });
});
