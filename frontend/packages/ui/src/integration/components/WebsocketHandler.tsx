'use client'

import { useEffect, useRef, useState } from "react";
import useWebSocketModule, { ReadyState } from "react-use-websocket";
import { usePartialMessageStore } from "./chat/PartialMessages";
import { useAppendPartialMessage, useRemovePartialMessage, useAddPartialMessage } from "./chat/PartialMessages.selectors";
import { createPartialDeltaQueue } from "./chat/partialDeltaQueue";
import { revalidateChatData, revalidateChatStates } from "../lib/chat-cache";
import { announceWebsocketReconnected } from "../lib/websocket";

const useWebSocket =
  typeof useWebSocketModule === "function"
    ? useWebSocketModule
    : (useWebSocketModule as any).default;

export function WebsocketHandler() {
    const [socketUrl, setSocketUrl] = useState<string | null>(null);

    useEffect(() => {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        setSocketUrl(`${protocol}//${window.location.host}/ws/connect`);
    }, []);

    // ci#1105: keep the socket alive across transient drops and announce a
    // reconnection so the chat can reload frames it missed while offline.
    // react-use-websocket reconnects by default, but making the policy explicit
    // (and generous) prevents a short network blip from leaving the UI detached
    // from the stream for the rest of the session.
    const hasOpenedRef = useRef(false);
    const { lastMessage, readyState } = useWebSocket(
        socketUrl,
        {
            shouldReconnect: () => true,
            reconnectAttempts: 50,
            reconnectInterval: 1500,
            retryOnError: true,
            onOpen: () => {
                if (hasOpenedRef.current) {
                    announceWebsocketReconnected();
                }
                hasOpenedRef.current = true;
                // ci#1157: the chat list cache may have been populated before
                // the socket was up (or by a stale reload), so refresh it on
                // every open - including reconnects - so a newly created chat
                // shows up without a manual page reload.
                void revalidateChatData();
            },
        },
        socketUrl !== null,
    );

    // ci#1149: the socket only pushes frames while it is connected, so a chat
    // created from another client (terminal TUI, second tab, another device) or
    // a turn that finished during a connection drop left the cached chat list
    // stale until a manual page reload. Refresh the chat caches whenever the tab
    // regains focus or becomes visible again, and keep a slow refresh running
    // while the socket is down, so reconnecting/returning users see new chats,
    // finished turns and tool results without reloading.
    useEffect(() => {
        if (typeof window === 'undefined') {
            return
        }
        const refresh = () => {
            if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
                return
            }
            void revalidateChatData()
        }
        window.addEventListener('focus', refresh)
        document.addEventListener('visibilitychange', refresh)
        return () => {
            window.removeEventListener('focus', refresh)
            document.removeEventListener('visibilitychange', refresh)
        }
    }, [])

    const socketOpen = readyState === ReadyState.OPEN
    useEffect(() => {
        if (socketOpen) {
            return
        }
        // While the socket is not open the UI is blind to new chats and tool
        // frames; poll the chat caches at a low rate until it reconnects.
        const interval = setInterval(() => {
            void revalidateChatData()
        }, 15000)
        return () => clearInterval(interval)
    }, [socketOpen])

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
        // Narrow selectors instead of a whole-store subscription: subscribing to
        // the store object itself re-rendered this handler on every streamed
        // delta of every chat (each action reference is stable, so these
        // subscriptions never cause a re-render at all).
        const addPartialMessage = useAddPartialMessage()
        const removePartialMessage = useRemovePartialMessage()
        const appendPartialMessageAction = useAppendPartialMessage()

        // Deltas are still applied one by one (the store's per-delta session/seq
        // merge rules depend on it), but at most once per animation frame.
        const queueRef = useRef(createPartialDeltaQueue((chatUUID, deltas, sessionId) => {
            for (const delta of deltas) {
                appendPartialMessageAction(chatUUID, delta, sessionId)
            }
        }))

        useEffect(() => () => {
            // Never drop buffered text on teardown.
            queueRef.current.dispose()
        }, [])
      
      
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
                queueRef.current.enqueue(chatUUID, {
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
                // immediately instead of waiting for the next poll. The chat
                // caches are refreshed as well (ci#1149): the first turn of a
                // chat created by another client (TUI, second tab, another
                // device) only ever reaches this socket as a partial frame, so
                // without a revalidate the new chat never pops into the chat
                // list until a manual reload.
                void revalidateChatStates()
                void revalidateChatData(chatUUID)
            }else if(parsedMessage.type === "end_partial_message"){
                // Commit any buffered tail text before the partial is dropped.
                queueRef.current.dispose()
                removePartialMessage(chatUUID, sessionId)
                void revalidateChatStates()
            }else if(parsedMessage.type === "tool_call_started" || parsedMessage.type === "tool_call_updated" || parsedMessage.type === "tool_call_ended"){
                // ci#1157: consume the fine grained tool frames. A running tool
                // call refreshes the chat so the tool run is visible while it
                // streams, and the terminal frame reloads the persisted
                // messages so the tool result renders as a result instead of
                // staying stuck as plain text.
                void revalidateChatData(chatUUID)
            }else if(parsedMessage.type === "new_message"){
                queueRef.current.dispose()
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
