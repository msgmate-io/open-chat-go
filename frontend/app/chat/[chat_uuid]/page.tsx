"use client"

import { useSearchParams } from 'next/navigation'
import { useParams } from 'next/navigation'
import { ChatBase } from '@/components/chat/ChatBase'
import { MessagesView } from '@/components/chat/MessagesView'
import useSWR from 'swr'
const fetcher = (...args) => fetch(...args).then(res => res.json())

export default function ChatPage() {
  const params = useParams()
  const chatUUID = params.chat_uuid
  const { data: messages } = useSWR(`/api/v1/chats/${chatUUID}/messages/list`, fetcher)
  
  console.log("Messages", messages)

  return <ChatBase><MessagesView chatUUID={chatUUID}/></ChatBase>
}
