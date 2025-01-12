import React from "react"
import { ChatBase } from "@/components/chat/ChatBase";
import { navigate } from 'vike/client/router'

export default function Page() {
  return <ChatBase chatUUID={null} navigateTo={(to: string) => {navigate(to)}}>
        <div>Start a new chat:</div>
    </ChatBase>
}