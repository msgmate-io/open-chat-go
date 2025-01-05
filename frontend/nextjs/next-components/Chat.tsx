"use client"

import { useParams } from 'next/navigation'
import { ChatBase } from '@/components/chat/ChatBase'
import { MessagesView } from '@/components/chat/MessagesView'
import { WebsocketHandler } from '@/components/WebsocketHandler'
import { useRouter } from "next/navigation";

export default function Chat() {
  const params = useParams()
  const chatUUID = Array.isArray(params.chat_uuid) ? params.chat_uuid[0] : ( params.chat_uuid || null )
  const router = useRouter();

  return <>
    <WebsocketHandler />
    <ChatBase chatUUID={chatUUID} navigateTo={(to: string) => {router.push(to)}}>
    <MessagesView 
        chatUUID={chatUUID} 
        leftPannelCollapsed={false} 
        onToggleCollapse={() => {}}/>
    </ChatBase>
    </>
}