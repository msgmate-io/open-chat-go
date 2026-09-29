'use client'

import { useEffect, useState } from "react"
import useWebSocketModule, { ReadyState } from "react-use-websocket"
import { mutate } from "swr"
import { usePartialMessageStore } from "@open-chat-go/ui"

const useWebSocket =
  typeof useWebSocketModule === "function"
    ? useWebSocketModule
    : useWebSocketModule.default

export function SharedInteractionWebsocketHandler({
  shareUUID,
}: {
  shareUUID: string
}) {
  const [socketUrl, setSocketUrl] = useState<string | null>(null)

  useEffect(() => {
    if (!shareUUID) {
      setSocketUrl(null)
      return
    }
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    setSocketUrl(`${protocol}//${window.location.host}/ws/interaction/${shareUUID}`)
  }, [shareUUID])

  const { lastMessage, readyState } = useWebSocket(
    socketUrl,
    undefined,
    socketUrl !== null,
  )

  if (!socketUrl) {
    return null
  }

  return (
    <SharedInteractionWebsocketHandlerBase
      shareUUID={shareUUID}
      lastMessage={lastMessage}
      readyState={readyState}
    />
  )
}

function SharedInteractionWebsocketHandlerBase({
  shareUUID,
  lastMessage,
  readyState,
}: {
  shareUUID: string
  lastMessage: MessageEvent<any> | null
  readyState: number
}) {
  const { addPartialMessage, appendPartialMessage, removePartialMessage } = usePartialMessageStore()

  useEffect(() => {
    if (lastMessage === null) {
      return
    }

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

    if (parsedMessage.type === "new_partial_message") {
      appendPartialMessage(chatUUID, {
        text: parsedMessage?.content?.text,
        thoughts: parsedMessage?.content?.reasoning,
        meta_data: parsedMessage?.content?.meta_data,
        tool_calls: parsedMessage?.content?.tool_calls,
        session_id: sessionId,
      }, sessionId)
      return
    }

    if (parsedMessage.type === "start_partial_message") {
      addPartialMessage(chatUUID, {
        text: parsedMessage?.content?.text || "",
        thoughts: parsedMessage?.content?.reasoning || [],
        meta_data: parsedMessage?.content?.meta_data || {},
        tool_calls: parsedMessage?.content?.tool_calls || [],
        session_id: sessionId,
      }, sessionId)
      return
    }

    if (parsedMessage.type === "end_partial_message") {
      removePartialMessage(chatUUID, sessionId)
      return
    }

    if (parsedMessage.type === "new_message") {
      removePartialMessage(chatUUID, sessionId)
      mutate(`/api/interaction/${shareUUID}/messages?page=1&limit=200`)
    }
  }, [addPartialMessage, appendPartialMessage, lastMessage, removePartialMessage, shareUUID])

  const connectionStatus = {
    [ReadyState.CONNECTING]: 'Connecting',
    [ReadyState.OPEN]: 'Open',
    [ReadyState.CLOSING]: 'Closing',
    [ReadyState.CLOSED]: 'Closed',
    [ReadyState.UNINSTANTIATED]: 'Uninstantiated',
  }[readyState]

  return <>{false && connectionStatus}</>
}
