import { create } from "zustand";
import { devtools } from "zustand/middleware";

export interface PartialMessageState {
        // uuid > partial message
        partialMessages: Record<string, string>
        addPartialMessage: (uuid: string, message: string) => void
        appendPartialMessage: (uuid: string, message: string) => void
        removePartialMessage: (uuid: string) => void
    }
    
export const usePartialMessageStore = create<PartialMessageState>()(
    devtools(
        (set) => ({
            partialMessages: {},
            addPartialMessage: (uuid: string, message: string) => set((state) => ({ 
                partialMessages: { ...state.partialMessages, [uuid]: message } 
            })),
            appendPartialMessage: (uuid: string, message: string) => set((state) => {
                const currentMessage = state.partialMessages[uuid] || ""
                return { 
                    partialMessages: { ...state.partialMessages, [uuid]: currentMessage + message } 
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
