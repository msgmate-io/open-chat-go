import { create } from "zustand";
import { devtools } from "zustand/middleware";

export interface PartialMessage {
    text: string;
    thoughts: string[];
    meta_data: any;
    tool_calls: any[];
    session_id?: string;
    // seq is the partial-message sequence number of the last applied delta.
    // When the entry was hydrated from the streaming snapshot after a page
    // reload, live deltas with seq <= this value were already included in the
    // snapshot and must be skipped.
    seq?: number;
}

export interface PartialMessageState {
    // uuid > partial message
    partialMessages: Record<string, PartialMessage>
    addPartialMessage: (uuid: string, message: PartialMessage, sessionId?: string) => void
    appendPartialMessage: (uuid: string, message: Partial<PartialMessage>, sessionId?: string) => void
    removePartialMessage: (uuid: string, sessionId?: string) => void
}

export const usePartialMessageStore = create<PartialMessageState>()(
    devtools(
        (set) => ({
            partialMessages: {},
            addPartialMessage: (uuid: string, message: PartialMessage, sessionId?: string) => set((state) => {
                const currentMessage = state.partialMessages[uuid];
                if (sessionId && currentMessage?.session_id === sessionId) {
                    return { partialMessages: state.partialMessages };
                }

                return {
                    partialMessages: {
                        ...state.partialMessages,
                        [uuid]: {
                            ...message,
                            session_id: sessionId || message.session_id,
                        },
                    },
                };
            }),
            appendPartialMessage: (uuid: string, message: Partial<PartialMessage>, sessionId?: string) => set((state) => {
                const currentMessage = state.partialMessages[uuid] || { text: "", thoughts: [], meta_data: {}, tool_calls: [] };
                if (currentMessage.session_id && sessionId && currentMessage.session_id !== sessionId) {
                    return { partialMessages: state.partialMessages };
                }

                return { 
                    partialMessages: { 
                        ...state.partialMessages, 
                        [uuid]: {
                            text: message.text ? currentMessage.text + message.text : currentMessage.text,
                            thoughts: message.thoughts 
                                ? currentMessage.thoughts.map((thought, index) => 
                                    index < (message.thoughts?.length || 0) 
                                        ? thought + (message.thoughts?.[index] || '')
                                        : thought
                                  ).concat(message.thoughts?.slice(currentMessage.thoughts.length) || [])
                                : currentMessage.thoughts,
                            meta_data: message.meta_data
                                ? { ...(currentMessage.meta_data || {}), ...message.meta_data }
                                : currentMessage.meta_data,
                            tool_calls: message.tool_calls !== undefined ? message.tool_calls : currentMessage.tool_calls,
                            session_id: sessionId || currentMessage.session_id,
                            seq: message.seq ?? currentMessage.seq,
                        }
                    } 
                }
            }),
            removePartialMessage: (uuid: string, sessionId?: string) => set((state) => {
                const currentMessage = state.partialMessages[uuid];
                if (!currentMessage) {
                    return { partialMessages: state.partialMessages };
                }
                if (sessionId && currentMessage.session_id && currentMessage.session_id !== sessionId) {
                    return { partialMessages: state.partialMessages };
                }

                return {
                    partialMessages: Object.fromEntries(
                        Object.entries(state.partialMessages)
                            .filter(([key]) => key !== uuid)
                    )
                };
            }),
        })
    )
)
