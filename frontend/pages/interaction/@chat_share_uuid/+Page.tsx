import useSWR from "swr"
import { usePageContext } from "vike-react/usePageContext"
import logoUrl from "@/assets/logo.png"
import { fetcher } from "@/lib/utils"
import { MessageItem } from "@open-chat-go/ui"
import { InteractionChatInfo } from "@open-chat-go/ui"
import { SharedInteractionWebsocketHandler } from "@/components/chat/SharedInteractionWebsocketHandler"
import { usePartialMessageStore } from "@open-chat-go/ui"
import { BotDisplay, Button, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui"
import { ExternalLink, LogIn } from "lucide-react"

type SharedInteractionAccessResponse = {
  authenticated?: boolean
  has_access?: boolean
  chat_uuid?: string
}

type SharedChatResponse = {
  uuid: string
  chat_share_uuid: string
  chat_type: string
  published_at?: string
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

export default function SharedInteractionPage() {
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

  return (
    <div className="flex h-screen w-full flex-col items-center px-4">
      <div className="absolute left-0 top-0 z-40 ml-3 mt-3 flex items-center gap-2 rounded-xl border border-border/60 bg-card/90 px-2 py-1 shadow-sm backdrop-blur-sm">
        <BotDisplay selectedModel={renderedChat?.config?.model} />
      </div>

      <div className="absolute right-0 top-0 z-40 mr-3 mt-3 flex items-center gap-2">
        {access?.has_access && access.chat_uuid ? (
          <Button asChild size="sm" className="gap-1.5">
            <a href={`/chat/${access.chat_uuid}?chat_type=interaction`}>
              <ExternalLink className="size-4" />
              View full interaction
            </a>
          </Button>
        ) : access?.authenticated === false ? (
          <Button asChild size="sm" variant="outline" className="gap-1.5">
            <a href="/login">
              <LogIn className="size-4" />
              Sign in
            </a>
          </Button>
        ) : null}
      </div>

      <div className="chat-messages-column">
        <InteractionChatInfo chat={renderedChat} isSharedView />
        <div className="flex flex-1 flex-col gap-3 overflow-y-auto px-4 pb-4 pt-12">
          {[...(messages.rows || [])].reverse().map((message) => {
            const isBotSender = message.sender_is_automated === true || (botUUID !== "" && message.sender_uuid === botUUID)
            return (
              <MessageItem
                key={message.uuid}
                message={{
                  uuid: message.uuid,
                  text: message.text || "",
                  send_at: message.send_at || "",
                  thoughts: message.reasoning || [],
                  tool_calls: message.tool_calls || [],
                  meta_data: message.meta_data || {},
                }}
                chat={renderedChat}
                selfIsSender={!isBotSender}
                isBotChat
              />
            )
          })}
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
            />
          ) : null}
        </div>
      </div>

      <div className="mb-6 mt-3 flex items-center justify-center gap-2 opacity-80">
        <img src={logoUrl} alt="Open Chat" className="h-6 w-6 rounded-full" />
        <Text type={TextTypes.Body7} color="muted">
          Powered by Open Chat{chat.published_at ? ` · shared ${new Date(chat.published_at).toLocaleDateString()}` : ""}
        </Text>
      </div>

      <SharedInteractionWebsocketHandler shareUUID={shareUUID} />
    </div>
  )
}
