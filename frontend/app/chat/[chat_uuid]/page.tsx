"use client"

import { useSearchParams } from 'next/navigation'
import { useParams } from 'next/navigation'

export default function ChatPage() {
  const params = useParams()
  const searchParams = useSearchParams()
  const chatUUID = params.chat_uuid

  return <>
    Selected Chat {chatUUID}
  </>
}
