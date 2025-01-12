import React from "react"
import { ChatBase } from "@/components/chat/ChatBase";
import { navigate } from 'vike/client/router'
import { StartChat } from "@/components/chat/StartChat";
import { usePageContext } from "vike-react/usePageContext";

export default function Page() {
const pageContext = usePageContext()

  return <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
        <StartChat contactToken={pageContext.routeParams.contact_token} leftPannelCollapsed={false} onToggleCollapse={() => {}} navigateTo={(to: string) => {navigate(to)}} />
    </ChatBase>
}