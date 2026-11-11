import { useShallow } from "zustand/react/shallow";
import {
    usePartialMessageStore,
    type PartialMessage,
    type PartialMessageState,
} from "./PartialMessages";

/*
 * Stable actions / narrow slices of the partial-message store.
 *
 * Every action inside the store is created once at store-creation time, so a
 * selector for a single action is referentially stable: components that only
 * need the action never re-render when streamed deltas land.
 *
 * The selector *functions* are exported separately from their hooks so they can
 * be unit tested (and reused) without a React renderer.
 */

export const selectAppendPartialMessage = (state: PartialMessageState) =>
    state.appendPartialMessage;
export const selectAddPartialMessage = (state: PartialMessageState) =>
    state.addPartialMessage;
export const selectRemovePartialMessage = (state: PartialMessageState) =>
    state.removePartialMessage;

/**
 * The partial message of ONE chat.
 *
 * Never select `s.partialMessages` (the whole record): its identity changes on
 * every delta of every chat, which re-rendered every subscriber - including
 * transcripts of unrelated chats. Selecting one entry keeps the result
 * referentially identical while another chat streams, so a memoised transcript
 * of chat A is skipped entirely when a delta only touches chat B (ci#1031).
 */
export const createPartialMessageSelector =
    (chatUUID: string | undefined) => (state: PartialMessageState): PartialMessage | undefined =>
        chatUUID ? state.partialMessages[chatUUID] : undefined;

/** Whether a chat currently has a streamed (in-flight) partial message. */
export const createHasPartialMessageSelector =
    (chatUUID: string | undefined) => (state: PartialMessageState): boolean =>
        Boolean(chatUUID && state.partialMessages[chatUUID]);

/** One selector over several chats (see `usePartialMessagesFor`). */
export const createPartialMessagesForSelector = (chatUUIDs: string[]) =>
    (state: PartialMessageState): Record<string, PartialMessage> => {
        const picked: Record<string, PartialMessage> = {};
        for (const uuid of chatUUIDs) {
            const partial = state.partialMessages[uuid];
            if (partial) picked[uuid] = partial;
        }
        return picked;
    };

export const useAppendPartialMessage = () => usePartialMessageStore(selectAppendPartialMessage);
export const useAddPartialMessage = () => usePartialMessageStore(selectAddPartialMessage);
export const useRemovePartialMessage = () => usePartialMessageStore(selectRemovePartialMessage);

/** The partial message of ONE chat (see `createPartialMessageSelector`). */
export function usePartialMessage(chatUUID: string | undefined): PartialMessage | undefined {
    return usePartialMessageStore(createPartialMessageSelector(chatUUID));
}

/** Whether a chat currently has a streamed (in-flight) partial message. */
export function useHasPartialMessage(chatUUID: string | undefined): boolean {
    return usePartialMessageStore(createHasPartialMessageSelector(chatUUID));
}

/** Tuple-safe selector: `useShallow` keeps the returned record referentially stable. */
export function usePartialMessagesFor(chatUUIDs: string[]): Record<string, PartialMessage> {
    return usePartialMessageStore(
        useShallow(createPartialMessagesForSelector(chatUUIDs)),
    );
}
