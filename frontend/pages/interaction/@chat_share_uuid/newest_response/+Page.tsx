import useSWR from "swr"
import { usePageContext } from "vike-react/usePageContext"
import { fetcher } from "@/lib/utils"
import { MessageItem } from "@open-chat-go/ui"
import { SharedInteractionWebsocketHandler } from "@/components/chat/SharedInteractionWebsocketHandler"
import { usePartialMessageStore } from "@open-chat-go/ui"
import { Button, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui"
import { ExternalLink } from "lucide-react"

type SharedInteractionAccessResponse = {
  authenticated?: boolean
  has_access?: boolean
  chat_uuid?: string
}

type SharedChatResponse = {
  uuid: string
  chat_share_uuid: string
  chat_type: string
  partner?: {
    uuid?: string
    name?: string
    is_automated?: boolean
  }
  config?: {
    model?: string
    backend?: string
    max_tokens?: number
    temperature?: number
    context?: number
    tools?: string[]
    tool_init?: Record<string, unknown>
    system_prompt?: string
  }
  interaction_details?: {
    model?: string
    backend?: string
    max_tokens?: number
    temperature?: number
    context?: number
    tools?: string[]
    tool_init?: Record<string, unknown>
    system_prompt?: string
  }
}

type SharedMessagesResponse = {
  rows: Array<{
    uuid: string
    sender_uuid: string
    sender_is_automated?: boolean
    text: string
    send_at: string
    reasoning?: string[]
    tool_calls?: Array<{ name: string; arguments: unknown; result?: unknown }>
    meta_data?: Record<string, unknown>
  }>
}

export default function SharedInteractionNewestResponsePage() {
  const pageContext = usePageContext()
  const shareUUID = pageContext.routeParams.chat_share_uuid

  const { data: chat, error: chatError } = useSWR<SharedChatResponse>(`/api/interaction/${shareUUID}`, fetcher)
  const { data: messages, error: messageError } = useSWR<SharedMessagesResponse>(`/api/interaction/${shareUUID}/messages?page=1&limit=200`, fetcher)
  const { data: access } = useSWR<SharedInteractionAccessResponse>(`/api/interaction/${shareUUID}/access`, fetcher)
  const partialMessage = usePartialMessageStore((state) => {
    if (!chat?.uuid) {
      return undefined
    }
    return state.partialMessages[chat.uuid]
  })

  if (chatError || messageError) {
    return (
      <div className="flex min-h-screen items-center justify-center px-4 text-center">
        <div>
          <Text type={TextTypes.Heading5} bold>Shared chat not found</Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-2">This shared interaction may have been unpublished.</Text>
        </div>
      </div>
    )
  }

  if (!chat || !messages) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoadingSpinner />
      </div>
    )
  }

  const renderedChat = {
    ...chat,
    config: chat.interaction_details || chat.config,
    partner: {
      name: chat.partner?.name || "Assistant",
      is_bot: true,
    },
  }
  const botUUID = chat.partner?.uuid || ""
  const newestMessage = messages.rows?.[0]
  const newestIsBotSender = newestMessage
    ? newestMessage.sender_is_automated === true || (botUUID !== "" && newestMessage.sender_uuid === botUUID)
    : true

  return (
    <div className="flex min-h-screen w-full flex-col px-4 py-4">
      {access?.has_access && access.chat_uuid ? (
        <div className="mb-2 flex justify-end">
          <Button asChild size="sm" className="gap-1.5">
            <a href={`/chat/${access.chat_uuid}?chat_type=interaction`}>
              <ExternalLink className="size-4" />
              View full interaction
            </a>
          </Button>
        </div>
      ) : null}
      <div className="flex flex-1 flex-col gap-3 overflow-y-auto">
        {chat.uuid && partialMessage ? (
          <MessageItem
            key={`partial_${chat.uuid}`}
            message={{
              uuid: `partial_${chat.uuid}`,
              text: partialMessage.text || "",
              thoughts: partialMessage.thoughts || [],
              meta_data: partialMessage.meta_data || {},
              tool_calls: partialMessage.tool_calls || [],
              is_generating: true,
            }}
            chat={renderedChat}
            selfIsSender={false}
            isBotChat
            showToolbar={false}
            showBotIdentity={false}
          />
        ) : newestMessage ? (
          <MessageItem
            key={newestMessage.uuid}
            message={{
              uuid: newestMessage.uuid,
              text: newestMessage.text || "",
              send_at: newestMessage.send_at || "",
              thoughts: newestMessage.reasoning || [],
              tool_calls: newestMessage.tool_calls || [],
              meta_data: newestMessage.meta_data || {},
            }}
            chat={renderedChat}
            selfIsSender={!newestIsBotSender}
            isBotChat
            showToolbar={false}
            showBotIdentity={false}
          />
        ) : (
          <Text type={TextTypes.Body6} color="muted">No responses yet.</Text>
        )}
      </div>

      <SharedInteractionWebsocketHandler shareUUID={shareUUID} />
    </div>
  )
}
