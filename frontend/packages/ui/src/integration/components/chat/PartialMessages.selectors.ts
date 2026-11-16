import { useShallow } from "zustand/react/shallow";
import { usePartialMessageStore, type PartialMessage } from "./PartialMessages";

/**
 * Stable actions / narrow slices of the partial-message store.
 *
 * Every action inside the store is created once at store-creation time, so a
 * selector for a single action is referentially stable: components that only
 * need the action never re-render when streamed deltas land.
 */
export const useAppendPartialMessage = () => usePartialMessageStore((s) => s.appendPartialMessage);
export const useAddPartialMessage = () => usePartialMessageStore((s) => s.addPartialMessage);
export const useRemovePartialMessage = () => usePartialMessageStore((s) => s.removePartialMessage);

/**
 * The partial message of ONE chat.
 *
 * Never select `s.partialMessages` (the whole record): its identity changes on
 * every delta of every chat, which re-rendered every subscriber - including
 * transcripts of unrelated chats.
 */
export function usePartialMessage(chatUUID: string | undefined): PartialMessage | undefined {
    return usePartialMessageStore((s) => (chatUUID ? s.partialMessages[chatUUID] : undefined));
}

/** Whether a chat currently has a streamed (in-flight) partial message. */
export function useHasPartialMessage(chatUUID: string | undefined): boolean {
    return usePartialMessageStore((s) => Boolean(chatUUID && s.partialMessages[chatUUID]));
}

/** Tuple-safe selector: `useShallow` keeps the returned tuple referentially stable. */
export function usePartialMessagesFor(chatUUIDs: string[]): Record<string, PartialMessage> {
    return usePartialMessageStore(
        useShallow((s) => {
            const picked: Record<string, PartialMessage> = {};
            for (const uuid of chatUUIDs) {
                const partial = s.partialMessages[uuid];
                if (partial) picked[uuid] = partial;
            }
            return picked;
        }),
    );
}
