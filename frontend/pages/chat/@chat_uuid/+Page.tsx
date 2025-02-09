import { useState } from "react";
import { usePageContext } from "vike-react/usePageContext";
import useWebSocket, { ReadyState } from "react-use-websocket";
import { ChatBase } from "@/components/chat/ChatBase";
import { MessagesView } from "@/components/chat/MessagesView";
import { navigate } from 'vike/client/router'
import { WebsocketHandler } from "@/components/WebsocketHandler";

const routeBase = "/chat"

export async function onBeforePrerenderStart() {
  return [routeBase, `${routeBase}/{chat_uuid}`]
}

export default function ChatPage() {
    const pageContext = usePageContext();
    const chatUUID = pageContext.routeParams.chat_uuid;

  return <>
      <ChatBase chatUUID={chatUUID} navigateTo={(to: string) => {navigate(to)}}>
        <MessagesView chatUUID={chatUUID} />
      </ChatBase>
      <WebsocketHandler />
    </>
}