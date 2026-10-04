'use client'

import { useEffect, useState } from "react";
import useWebSocketModule, { ReadyState } from "react-use-websocket";
import { usePartialMessageStore } from "./chat/PartialMessages";
import { revalidateChatData, revalidateChatStates } from "../lib/chat-cache";

const useWebSocket =
  typeof useWebSocketModule === "function"
    ? useWebSocketModule
    : useWebSocketModule.default;

export function WebsocketHandler() {
    const [socketUrl, setSocketUrl] = useState<string | null>(null);

    useEffect(() => {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        setSocketUrl(`${protocol}//${window.location.host}/ws/connect`);
    }, []);

    const { lastMessage, readyState } = useWebSocket(
        socketUrl,
        undefined,
        socketUrl !== null,
    );

    if (!socketUrl) {
        return null;
    }

    return (
        <WebsocketHandlerBase
            lastMessage={lastMessage}
            readyState={readyState}
        />
    );
}

export function WebsocketHandlerBase({
    lastMessage,
    readyState
}: {
    lastMessage: MessageEvent<any> | null
    readyState: number
}) {
        const { addPartialMessage, appendPartialMessage, removePartialMessage } = usePartialMessageStore()
      
      
        useEffect(() => {
          if (lastMessage !== null) {
            let parsedMessage: any
            try {
                parsedMessage = JSON.parse(lastMessage.data)
            } catch {
                return
            }

            const chatUUID = parsedMessage?.content?.chat_uuid
            const sessionId = parsedMessage?.content?.session_id
            if (!chatUUID) {
                return
            }

            if(parsedMessage.type === "new_partial_message"){
                // Skip deltas that were already included in a hydrated
                // streaming snapshot (page reloaded mid-stream).
                const deltaSeq = parsedMessage?.content?.seq || 0;
                const currentPartial = usePartialMessageStore.getState().partialMessages[chatUUID];
                if (
                    deltaSeq > 0 &&
                    currentPartial?.session_id === sessionId &&
                    typeof currentPartial?.seq === "number" &&
                    deltaSeq <= currentPartial.seq
                ) {
                    return;
                }
                appendPartialMessage(chatUUID, {
                    text: parsedMessage?.content?.text,
                    thoughts: parsedMessage?.content?.reasoning,
                    meta_data: parsedMessage?.content?.meta_data,
                    tool_calls: parsedMessage?.content?.tool_calls,
                    session_id: sessionId,
                }, sessionId)
            }else if(parsedMessage.type === "start_partial_message"){
                addPartialMessage(chatUUID, {
                    text: parsedMessage?.content?.text || "",
                    thoughts: parsedMessage?.content?.reasoning || [],
                    meta_data: parsedMessage?.content?.meta_data || {},
                    tool_calls: parsedMessage?.content?.tool_calls || [],
                    session_id: sessionId,
                }, sessionId)
                // A bot reply just started: flip the sidebar state dot to active
                // immediately instead of waiting for the next poll.
                void revalidateChatStates()
            }else if(parsedMessage.type === "end_partial_message"){
                removePartialMessage(chatUUID, sessionId)
                void revalidateChatStates()
            }else if(parsedMessage.type === "new_message"){
                removePartialMessage(chatUUID, sessionId)
                // The final message changes the chat preview, state dots, pending
                // action tasks and (for the open chat) the message list. The list
                // is keyed with query params, so revalidate every chat cache by
                // prefix rather than mutating a single bare key.
                void revalidateChatData(chatUUID)
            }
          }
        }, [lastMessage]);
      
        const connectionStatus = {
          [ReadyState.CONNECTING]: 'Connecting',
          [ReadyState.OPEN]: 'Open',
          [ReadyState.CLOSING]: 'Closing',
          [ReadyState.CLOSED]: 'Closed',
          [ReadyState.UNINSTANTIATED]: 'Uninstantiated',
        }[readyState];
        
        return <>{false && <div className="absolute top0 left0 z50 bgbase200 p2">
            <div className="flex flexcol">
                <div className="flex flexcol">
                    <div className="textsm">Debug connection status: {connectionStatus}</div>
                </div>
            </div>
        </div>}</>
    }
