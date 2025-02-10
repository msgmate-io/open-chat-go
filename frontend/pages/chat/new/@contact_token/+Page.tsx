import React from "react"
import { ChatBase } from "@/components/chat/ChatBase";
import { navigate } from 'vike/client/router'
import { StartChat } from "@/components/chat/StartChat";
import useSWR, { mutate } from "swr"
import { usePageContext } from "vike-react/usePageContext";
import { fetcher } from "@/lib/utils";

export async function onBeforePrerenderStart() {
  return [`/chat/new/{contact_token}`]
}

export default function Page() {
  const pageContext = usePageContext()

  return <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
        <StartChat contactToken={pageContext.routeParams.contact_token} navigateTo={(to: string) => {navigate(to)}} />
    </ChatBase>
}