import { MessageItem } from "@open-chat-go/ui"
import { BotDisplay, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui"
import { useEffect, useMemo, useState } from "react"

type RenderedMessage = {
  uuid: string
  send_at?: string
  sender_is_automated?: boolean
  data_type?: string
  text?: string
  reasoning?: string[]
  tool_calls?: Array<{ name: string; arguments: unknown; result?: unknown }>
  meta_data?: Record<string, unknown>
}

type InteractionExport = {
  generated_at?: string
  chat_uuid?: string
  state?: string
  prompt?: string
  messages?: RenderedMessage[]
}

function sanitizeUrlInput(raw: string | null): string {
  if (!raw) return ""
  let url = raw.replace(/[^a-zA-Z0-9:/?#[\]@!$&'()*+,;=~_.%-]/g, "")
  try {
    url = decodeURIComponent(url)
  } catch {
    /* keep raw */
  }
  if (!url.startsWith("https://")) {
    return ""
  }
  return url
}

export default function InteractionRendererPage() {
  const requestedUrlRaw = useMemo(() => {
    if (typeof window === "undefined") return null
    return new URLSearchParams(window.location.search).get("url")
  }, [])
  const targetUrl = sanitizeUrlInput(requestedUrlRaw)
  const [data, setData] = useState<InteractionExport | null>(null)
  const [error, setError] = useState<string>("")
  const [loading, setLoading] = useState<boolean>(!!targetUrl)

  useEffect(() => {
    if (!targetUrl) {
      setLoading(false)
      if (requestedUrlRaw) setError("Only https:// URLs pointing to a public JSON file are supported.")
      return
    }
    let cancelled = false
    setLoading(true)
    fetch(targetUrl, { credentials: "omit", mode: "cors" })
      .then((resp) => {
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
        return resp.json()
      })
      .then((parsed: InteractionExport) => {
        if (cancelled) return
        if (!parsed || !Array.isArray(parsed.messages)) throw new Error("not a bundle interaction export")
        setData(parsed)
      })
      .catch((err) => {
        if (!cancelled) setError(err?.message || "Failed to load interaction JSON.")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [targetUrl])

  if (!targetUrl || (!loading && (error || !data))) {
    return (
      <div className="flex min-h-screen items-center justify-center px-4 text-center">
        <div>
          <Text type={TextTypes.Heading5} bold>Interaction renderer</Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-2 max-w-md">
            Append <code>?url=https://…/interaction.json</code> to render a sandbox benchmark interaction chat
            (the JSON is fetched directly from the given public URL).
          </Text>
          {error || targetUrl === "" ? (
            <Text type={TextTypes.Body7} color="muted" className="mt-2">{error || "Missing ?url parameter."}</Text>
          ) : null}
        </div>
      </div>
    )
  }

  if (loading || !data) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoadingSpinner />
      </div>
    )
  }

  const opencodeMeta = (data.messages || [])
    .filter((m) => m.meta_data && (m.meta_data as Record<string, unknown>).opencode)
    .map((m) => ((m.meta_data as Record<string, unknown>).opencode as Record<string, unknown>))[0]

  const textMessages = (data.messages || []).filter(
    (m) => ((m.data_type || "text") as string).toLowerCase() !== "event" && (m.text || "").trim() !== "",
  )

  return (
    <div className="flex h-screen w-full flex-col items-center px-4">
      <div className="absolute left-0 top-0 z-40 ml-3 mt-3 flex items-center gap-2 rounded-xl border border-border/60 bg-card/90 px-2 py-1 shadow-sm backdrop-blur-sm">
        <BotDisplay
          selectedModel={
            opencodeMeta?.provider && opencodeMeta?.model
              ? `${String(opencodeMeta.provider)}/${String(opencodeMeta.model)}`
              : undefined
          }
        />
      </div>

      <div className="chat-messages-column w-full max-w-3xl">
        <div className="flex flex-1 flex-col gap-3 overflow-y-auto px-4 pb-4 pt-14">
          {[...textMessages].reverse().map((message) => (
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
              chat={{ uuid: data.chat_uuid || "export", partner: { name: "Benchmark Bot", is_bot: true } } as any}
              selfIsSender={message.sender_is_automated === false}
              isBotChat
            />
          ))}
        </div>
      </div>

      <div className="mb-6 mt-3 flex items-center justify-center gap-2 opacity-80">
        <Text type={TextTypes.Body7} color="muted">
          Rendered sandbox benchmark interaction · state: {data.state || "unknown"}
          {opencodeMeta?.duration_ms ? ` · ${String(opencodeMeta.duration_ms)}ms` : ""}
        </Text>
      </div>
    </div>
  )
}
