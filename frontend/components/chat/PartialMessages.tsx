import { create } from "zustand";
import { devtools } from "zustand/middleware";

export interface PartialMessage {
    text: string;
    thoughts: string[];
}

export interface PartialMessageState {
    // uuid > partial message
    partialMessages: Record<string, PartialMessage>
    addPartialMessage: (uuid: string, message: PartialMessage) => void
    appendPartialMessage: (uuid: string, message: Partial<PartialMessage>) => void
    removePartialMessage: (uuid: string) => void
}

export const usePartialMessageStore = create<PartialMessageState>()(
    devtools(
        (set) => ({
            partialMessages: {},
            addPartialMessage: (uuid: string, message: PartialMessage) => set((state) => ({ 
                partialMessages: { ...state.partialMessages, [uuid]: message } 
            })),
            appendPartialMessage: (uuid: string, message: Partial<PartialMessage>) => set((state) => {
                const currentMessage = state.partialMessages[uuid] || { text: "", thoughts: [] };
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
                                : currentMessage.thoughts
                        }
                    } 
                }
            }),
            removePartialMessage: (uuid: string) => set((state) => ({ 
                partialMessages: Object.fromEntries(
                    Object.entries(state.partialMessages)
                    .filter(([key]) => key !== uuid)
                )
            })),
        })
    )
)
