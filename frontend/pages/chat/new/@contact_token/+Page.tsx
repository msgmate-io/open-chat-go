import React from "react"
import { ChatBase } from "@open-chat-go/ui";
import { navigate } from 'vike/client/router'
import { StartChat } from "@/components/chat/StartChat";
import { usePageContext } from "vike-react/usePageContext";

export default function Page() {
  const pageContext = usePageContext()

  return <ChatBase chatUUID={null} hideMobileShortcut navigateTo={(to: string) => {navigate(to)}}>
        <StartChat contactToken={pageContext.routeParams.contact_token} navigateTo={(to: string) => {navigate(to)}} />
    </ChatBase>
}
