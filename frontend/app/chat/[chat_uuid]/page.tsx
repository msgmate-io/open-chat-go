"use client"

import { useParams } from 'next/navigation'
import { ChatBase } from '@/components/chat/ChatBase'
import { MessagesView } from '@/components/chat/MessagesView'

export default function ChatPage() {
  const params = useParams()
  const chatUUID = Array.isArray(params.chat_uuid) ? params.chat_uuid[0] : ( params.chat_uuid || null )

  return <ChatBase chatUUID={chatUUID}>
      <MessagesView 
        chatUUID={chatUUID} 
        leftPannelCollapsed={false} 
        onToggleCollapse={() => {}}/>
    </ChatBase>
}
