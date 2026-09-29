import { useState } from "react";
import { usePageContext } from "vike-react/usePageContext";
import { ChatBase } from "@open-chat-go/ui";
import { MessagesView } from "@open-chat-go/ui";
import { navigate } from 'vike/client/router'
import { WebsocketHandler } from "@open-chat-go/ui";

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
