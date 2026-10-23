import { useCallback, useEffect, useState } from "react"
import { Check, Copy, EyeOff, Globe, Link2, Lock, Share2, ShieldCheck, User } from "lucide-react"
import { Button } from "../button"
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "../dialog"
import { Input } from "../input"
import { Text, TextTypes } from "../text"

type ShareMode = "public" | "user"

// ContentMode controls how much of a shared chat is exposed. "censored" (the
// default) strips tool arguments/results, message meta-data and anything that
// looks like a secret; "unsecure" exposes the raw conversation.
type ContentMode = "censored" | "unsecure"

type PublishResponse = {
  chat_uuid?: string
  chat_share_uuid?: string
  access_mode?: string
  content_mode?: string
  target_username?: string
}

const SHARE_QUERY_PARAM = "share"

const buildShareUrl = (chatShareUUID: string) => {
  if (typeof window === "undefined") {
    return `/interaction/${chatShareUUID}`
  }
  return `${window.location.origin}/interaction/${chatShareUUID}`
}

// ShareChatModal publishes a chat and surfaces the resulting share link. A
// chat can be shared publicly (anyone with the link) or scoped to a single
// user. The link is a read-only interaction page under /interaction/<uuid>.
export function ShareChatModal({
  chat,
  open,
  setOpen,
}: {
  chat: any
  open: boolean
  setOpen: (open: boolean) => void
}) {
  const [mode, setMode] = useState<ShareMode>("public")
  const [contentMode, setContentMode] = useState<ContentMode>("censored")
  const [targetUsername, setTargetUsername] = useState("")
  const [creating, setCreating] = useState(false)
  const [revoking, setRevoking] = useState(false)
  const [error, setError] = useState("")
  const [share, setShare] = useState<PublishResponse | null>(null)
  const [copied, setCopied] = useState(false)

  const shareUUID = share?.chat_share_uuid || ""
  const shareUrl = shareUUID ? buildShareUrl(shareUUID) : ""

  // Reflect the open state in the `share` query parameter so a share dialog is
  // directly linkable, e.g. /chat/<uuid>?share=<uuid>.
  const syncShareParam = useCallback((value: string) => {
    if (typeof window === "undefined") {
      return
    }
    const nextUrl = new URL(window.location.href)
    if (value) {
      nextUrl.searchParams.set(SHARE_QUERY_PARAM, value)
    } else {
      nextUrl.searchParams.delete(SHARE_QUERY_PARAM)
    }
    window.history.replaceState(window.history.state, "", `${nextUrl.pathname}${nextUrl.search}${nextUrl.hash}`)
  }, [])

  useEffect(() => {
    if (!open) {
      setError("")
      setCopied(false)
      return
    }
    if (chat?.uuid) {
      syncShareParam(chat.uuid)
    }
  }, [open, chat?.uuid, syncShareParam])

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    if (!nextOpen) {
      syncShareParam("")
      setShare(null)
      setTargetUsername("")
      setMode("public")
      setContentMode("censored")
    }
  }

  const createShare = async () => {
    if (!chat?.uuid) {
      return
    }
    if (mode === "user" && targetUsername.trim() === "") {
      setError("Enter the username to share with")
      return
    }
    setCreating(true)
    setError("")
    try {
      const response = await fetch(`/api/chat/${chat.uuid}/publish`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          access_mode: mode,
          content_mode: contentMode,
          target_username: mode === "user" ? targetUsername.trim() : "",
        }),
      })
      if (!response.ok) {
        setError(response.status === 400 ? "Could not find that user" : "Failed to create share link")
        return
      }
      const data = (await response.json()) as PublishResponse
      setShare(data)
    } catch {
      setError("Failed to create share link")
    } finally {
      setCreating(false)
    }
  }

  const revokeShare = async () => {
    if (!chat?.uuid) {
      return
    }
    setRevoking(true)
    setError("")
    try {
      const response = await fetch(`/api/chat/${chat.uuid}/unpublish`, { method: "POST" })
      if (response.ok) {
        setShare(null)
      } else {
        setError("Failed to revoke share link")
      }
    } catch {
      setError("Failed to revoke share link")
    } finally {
      setRevoking(false)
    }
  }

  const copyLink = async () => {
    if (!shareUrl) {
      return
    }
    try {
      await navigator.clipboard.writeText(shareUrl)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setError("Could not copy the link")
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        data-testid="share-chat-modal"
        className="max-h-[85vh] w-[calc(100%-2rem)] max-w-md gap-0 overflow-y-auto p-4"
      >
        <DialogTitle className="flex items-center gap-2 px-1 pb-2 text-sm font-semibold text-foreground">
          <Share2 className="size-4" />
          Share chat
        </DialogTitle>
        <div className="flex flex-col gap-3 text-sm">
          <div className="grid grid-cols-2 gap-2">
            <button
              type="button"
              onClick={() => {
                setMode("public")
                setShare(null)
              }}
              className={`flex flex-col gap-1 rounded-md border p-2 text-left transition-colors ${
                mode === "public" ? "border-primary bg-primary/5" : "border-border/70 hover:bg-secondary"
              }`}
            >
              <span className="flex items-center gap-1.5 text-xs font-semibold">
                <Globe className="size-3.5" /> Public
              </span>
              <Text type={TextTypes.Body7} color="muted">
                Anyone with the link can view.
              </Text>
            </button>
            <button
              type="button"
              onClick={() => {
                setMode("user")
                setShare(null)
              }}
              className={`flex flex-col gap-1 rounded-md border p-2 text-left transition-colors ${
                mode === "user" ? "border-primary bg-primary/5" : "border-border/70 hover:bg-secondary"
              }`}
            >
              <span className="flex items-center gap-1.5 text-xs font-semibold">
                <User className="size-3.5" /> Specific user
              </span>
              <Text type={TextTypes.Body7} color="muted">
                Only that user can view.
              </Text>
            </button>
          </div>

          {mode === "user" ? (
            <label className="flex flex-col gap-1 px-1">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">Username</span>
              <Input
                value={targetUsername}
                onChange={(event) => setTargetUsername(event.target.value)}
                placeholder="username"
                className="h-8 bg-background"
              />
            </label>
          ) : null}

          <div className="flex flex-col gap-1">
            <span className="px-1 text-[10px] uppercase tracking-wide text-muted-foreground">Content</span>
            <div className="grid grid-cols-2 gap-2">
              <button
                type="button"
                data-testid="share-content-censored"
                onClick={() => {
                  setContentMode("censored")
                  setShare(null)
                }}
                className={`flex flex-col gap-1 rounded-md border p-2 text-left transition-colors ${
                  contentMode === "censored" ? "border-primary bg-primary/5" : "border-border/70 hover:bg-secondary"
                }`}
              >
                <span className="flex items-center gap-1.5 text-xs font-semibold">
                  <ShieldCheck className="size-3.5" /> Censored
                </span>
                <Text type={TextTypes.Body7} color="muted">
                  Messages and tool names only. Parameters, results and meta-data are hidden.
                </Text>
              </button>
              <button
                type="button"
                data-testid="share-content-unsecure"
                onClick={() => {
                  setContentMode("unsecure")
                  setShare(null)
                }}
                className={`flex flex-col gap-1 rounded-md border p-2 text-left transition-colors ${
                  contentMode === "unsecure" ? "border-primary bg-primary/5" : "border-border/70 hover:bg-secondary"
                }`}
              >
                <span className="flex items-center gap-1.5 text-xs font-semibold">
                  <EyeOff className="size-3.5" /> Unsecure
                </span>
                <Text type={TextTypes.Body7} color="muted">
                  Full conversation including tool parameters, results and meta-data.
                </Text>
              </button>
            </div>
            {contentMode === "unsecure" ? (
              <Text type={TextTypes.Body7} color="muted" className="px-1 text-destructive">
                Unsecure shares expose everything in this chat. Only use it when you are sure it contains no secrets.
              </Text>
            ) : null}
          </div>

          {shareUrl ? (
            <div className="flex flex-col gap-2 rounded-md border border-border/70 bg-card p-2">
              <div className="flex items-center gap-1.5">
                <Lock className="size-3.5 text-muted-foreground" />
                <Text type={TextTypes.Body7} color="muted" className="flex-1 truncate">
                  {share?.access_mode === "user"
                    ? `Shared with ${share?.target_username || "a specific user"}`
                    : "Publicly shared"}
                </Text>
              </div>
              <div className="flex items-center gap-1">
                <Input readOnly value={shareUrl} className="h-8 flex-1 text-xs" />
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  aria-label="Copy share link"
                  onClick={() => void copyLink()}
                >
                  {copied ? <Check className="size-4 text-green-500" /> : <Copy className="size-4" />}
                </Button>
              </div>
              <div className="flex items-center justify-between">
                <a
                  href={shareUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-[11px] text-primary hover:underline"
                >
                  <Link2 className="size-3" />
                  Open link
                </a>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs text-destructive"
                  disabled={revoking}
                  onClick={() => void revokeShare()}
                >
                  {revoking ? "Revoking…" : "Revoke link"}
                </Button>
              </div>
            </div>
          ) : (
            <Button
              type="button"
              size="sm"
              className="h-8 self-start px-3 text-xs"
              disabled={creating}
              onClick={() => void createShare()}
            >
              {creating ? "Creating…" : "Create share link"}
            </Button>
          )}

          {error ? (
            <Text type={TextTypes.Body7} className="px-1 text-[11px] text-destructive">
              {error}
            </Text>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
