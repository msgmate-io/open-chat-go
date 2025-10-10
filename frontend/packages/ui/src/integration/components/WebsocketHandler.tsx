'use client'

import { useEffect, useState } from "react";
import useWebSocketModule, { ReadyState } from "react-use-websocket";
import { mutate } from "swr";
import { usePartialMessageStore } from "./chat/PartialMessages";

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
            }else if(parsedMessage.type === "end_partial_message"){
                removePartialMessage(chatUUID, sessionId)
            }else if(parsedMessage.type === "new_message"){
                removePartialMessage(chatUUID, sessionId)
                mutate(`/api/v1/chats/${chatUUID}/messages/list`, async (data: any) => {
                    // Handle case when data is undefined
                    if (!data) {
                        return {
                            rows: [{
                                text: parsedMessage?.content?.text,
                                sender_uuid: parsedMessage?.content?.sender_uuid,
                                chat_uuid: chatUUID,
                                uuid: parsedMessage?.content?.uuid,
                                tool_calls: parsedMessage?.content?.tool_calls,
                                reasoning: parsedMessage?.content?.reasoning,
                                meta_data: parsedMessage?.content?.meta_data,
                                data_type: parsedMessage?.content?.data_type,
                            }]
                        }
                    }
                    
                    // Original logic when data exists
                    const newRows = data.rows.filter((row: any) => row.uuid !== 'partial_message')
                    return {
                        ...data,
                        rows: [{
                            text: parsedMessage?.content?.text,
                            sender_uuid: parsedMessage?.content?.sender_uuid,
                            chat_uuid: chatUUID,
                            uuid: parsedMessage?.content?.uuid,
                            tool_calls: parsedMessage?.content?.tool_calls,
                            reasoning: parsedMessage?.content?.reasoning,
                            meta_data: parsedMessage?.content?.meta_data,
                            data_type: parsedMessage?.content?.data_type,
                        }, ...newRows]
                    }
                })
                mutate(`/api/v1/chats/list`, async (data: any) => {
                    return {
                        ...data,
                        rows: data.rows.map((row: any) => row.uuid === chatUUID ? { ...row, latest_message: parsedMessage?.content } : row)
                    }
                })
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
