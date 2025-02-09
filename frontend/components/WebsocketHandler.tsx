'use client'

import { useEffect, useState } from "react";
import useWebSocket, { ReadyState } from "react-use-websocket";
import { mutate } from "swr";
import { usePartialMessageStore } from "@/components/chat/PartialMessages";

export function WebsocketHandler(){
        const [socketUrl, setSocketUrl] = useState('ws://localhost:1984/ws/connect');
        const [messageHistory, setMessageHistory] = useState<MessageEvent<any>[]>([]);
        const { partialMessages, addPartialMessage, appendPartialMessage, removePartialMessage } = usePartialMessageStore()
      
        const { sendMessage, lastMessage, readyState } = useWebSocket(socketUrl);
      
        useEffect(() => {
          if (lastMessage !== null) {
            setMessageHistory((prev) => prev.concat(lastMessage));
            const parsedMessage = JSON.parse(lastMessage.data)
            console.log("parsedMessage", parsedMessage)
            if(parsedMessage.type === "new_partial_message"){
                appendPartialMessage(parsedMessage?.content?.chat_uuid, parsedMessage?.content?.text)
            }else if(parsedMessage.type === "start_partial_message"){
                addPartialMessage(parsedMessage?.content?.chat_uuid, "")
            }else if(parsedMessage.type === "end_partial_message"){
                removePartialMessage(parsedMessage?.content?.chat_uuid)
                mutate(`/api/v1/chats/${parsedMessage?.content?.chat_uuid}/messages/list`, async (data: any) => {
                    return {
                        ...data,
                        rows: [{
                            text: partialMessages[parsedMessage?.content?.chat_uuid],
                            sender_uuid: "msgmate",
                            chat_uuid: parsedMessage?.content?.chat_uuid,
                            uuid: "partial_message"
                        }, ...data.rows]
                    }
                })
            }else if(parsedMessage.type === "new_message"){
                mutate(`/api/v1/chats/${parsedMessage?.content?.chat_uuid}/messages/list`, async (data: any) => {
                    return {
                        ...data,
                        rows: [{
                            text: parsedMessage?.content?.text,
                            sender_uuid: parsedMessage?.content?.sender_uuid,
                            chat_uuid: parsedMessage?.content?.chat_uuid,
                            uuid: parsedMessage?.content?.uuid
                        }, ...data.rows]
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
        
        return <div className="absolute top0 left0 z50 bgbase200 p2">
            <div className="flex flexcol">
                <div className="flex flexcol">
                    <div className="textsm">Debug connection status: {connectionStatus}</div>
                </div>
            </div>
        </div>
    }