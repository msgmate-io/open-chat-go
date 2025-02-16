import { create } from "zustand";
import { devtools } from "zustand/middleware";

export interface PartialMessage {
    text: string;
    thoughts: string[];
    meta_data: any;
    tool_calls: any[];
}

interface ToolCall {
    name: string;
    arguments: string;
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
                partialMessages: { ...state.partialMessages, [uuid]: { ...message, thoughts: [], meta_data: {}, tool_calls: [] } } 
            })),
            appendPartialMessage: (uuid: string, message: Partial<PartialMessage>) => set((state) => {
                const currentMessage = state.partialMessages[uuid] || { text: "", thoughts: [], meta_data: {}, tool_calls: [] };
                let newToolCalls = [...currentMessage.tool_calls];
                
                if (message.tool_calls) {
                    for (const toolCall of message.tool_calls) {
                        const existingCallIndex = newToolCalls.findIndex(
                            existing => existing.name === toolCall.name
                        );
                        
                        if (existingCallIndex === -1) {
                            newToolCalls.push(toolCall);
                        } else {
                            newToolCalls[existingCallIndex] = {
                                ...newToolCalls[existingCallIndex],
                                arguments: newToolCalls[existingCallIndex].arguments + toolCall.arguments
                            };
                        }
                    }
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
                            meta_data: message.meta_data || currentMessage.meta_data,
                            tool_calls: newToolCalls
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
