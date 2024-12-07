"use client"

import { useSearchParams } from 'next/navigation'
import { useParams } from 'next/navigation'
import { ChatBase } from '@/components/chat/ChatBase'
import { MessagesView } from '@/components/chat/MessagesView'
import useSWR from 'swr'
const fetcher = (...args) => fetch(...args).then(res => res.json())

export default function ChatPage() {
  const params = useParams()
  const chatUUID = Array.isArray(params.chat_uuid) ? params.chat_uuid[0] : ( params.chat_uuid || null )
  const { data: messages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
  
  console.log("Messages", messages)

  return <ChatBase chatUUID={chatUUID}>
      <MessagesView 
        chatUUID={chatUUID} 
        leftPannelCollapsed={false} 
        onToggleCollapse={() => {}}/>
    </ChatBase>
}
