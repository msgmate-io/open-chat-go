import { useEffect, useState, type ComponentProps, type ComponentType, type ReactNode } from "react"
import { mutate } from "swr"
import { Pin, PinOff, Plus, Share2, Trash2, X } from "lucide-react"
import { Button } from "../button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "../dropdown-menu"
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogTrigger,
} from "../dialog"
import { Input } from "../input"
import { Icon } from "../icon"
import { ShareChatModal } from "./ShareChatModal"
import { useIsSmallScreen } from "../../hooks/use-mobile"
import { fetcher as apiFetch } from "../../integration/lib/utils"

const normalizeTag = (tag: string) => tag.trim().toLowerCase()

const invalidateChatCaches = (chatUUID?: string) =>
  mutate(
    (key) =>
      typeof key === "string" &&
      (key.startsWith("/api/v1/chats/list") ||
        key === "/api/v1/chats/tags" ||
        (chatUUID ? key === `/api/v1/chats/${chatUUID}` : false)),
    undefined,
    { revalidate: true }
  )

type SettingsTrigger = ComponentType<ComponentProps<typeof DropdownMenuTrigger>>

function SettingsSeparator() {
  return <div className="my-1 h-px shrink-0 bg-border" role="separator" aria-hidden="true" />
}

export function ChatSettings({
  chat,
  open,
  setOpen,
  onDeleted,
  compact: compactProp,
  children,
}: {
  chat: any
  open: boolean
  setOpen: (open: boolean) => void
  onDeleted?: (chatUUID: string) => void
  /**
   * Force the compact (icon + modal) presentation. When omitted the surface
   * collapses automatically on very small / short viewports.
   */
  compact?: boolean
  /**
   * Render-prop receiving the trigger component for the active presentation
   * (`DropdownMenuTrigger` on desktop, `DialogTrigger` on small screens) so the
   * caller can wrap its own button without knowing the mode.
   */
  children: (props: { Trigger: SettingsTrigger }) => ReactNode
}) {
  const isSmallScreen = useIsSmallScreen()
  const compact = compactProp ?? isSmallScreen
  const Trigger: SettingsTrigger = compact ? DialogTrigger : DropdownMenuTrigger

  const [extraName, setExtraName] = useState(chat?.settings?.title || "")
  const [tags, setTags] = useState<string[]>(Array.isArray(chat?.tags) ? chat.tags : [])
  const [newTag, setNewTag] = useState("")
  const [savingTags, setSavingTags] = useState(false)
  const [savingTitle, setSavingTitle] = useState(false)
  const [pinSaving, setPinSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [shareOpen, setShareOpen] = useState(false)
  const [editTitleOpen, setEditTitleOpen] = useState(false)
  const pinned = chat?.pinned === true
  const extraNameChanged =
    extraName !== chat?.settings?.title && extraName !== ""

  useEffect(() => {
    setExtraName(chat?.settings?.title || "")
  }, [chat?.settings?.title])

  useEffect(() => {
    setTags(Array.isArray(chat?.tags) ? chat.tags : [])
  }, [chat?.tags])

  // Opening a share link like `/chat/<uuid>?share=<uuid>` deep-links straight
  // into the share dialog for that chat.
  useEffect(() => {
    if (typeof window === "undefined" || !chat?.uuid) {
      return
    }
    const params = new URLSearchParams(window.location.search)
    if (params.get("share") === chat.uuid) {
      setOpen(true)
      setShareOpen(true)
    }
    // Only react to a new chat identity; re-running on every render would
    // re-open the dialog after the user dismisses it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chat?.uuid])

  const persistTags = async (nextTags: string[]) => {
    setTags(nextTags)
    if (!chat?.uuid) {
      return
    }
    setSavingTags(true)
    try {
      await fetch(`/api/v1/chats/${chat.uuid}/settings`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ tags: nextTags }),
      })
      await invalidateChatCaches(chat.uuid)
    } finally {
      setSavingTags(false)
    }
  }

  const addTag = () => {
    const tag = normalizeTag(newTag)
    if (!tag || tags.includes(tag)) {
      setNewTag("")
      return
    }
    setNewTag("")
    void persistTags([...tags, tag])
  }

  const removeTag = (tag: string) => {
    void persistTags(tags.filter((entry) => entry !== tag))
  }

  // Persists the user-visible chat title into the same server-side ChatSettings
  // slot the internal automations integration writes to, so a manual rename and
  // an automation-generated label stay consistent.
  const persistTitle = async (nextTitle: string) => {
    if (!chat?.uuid) {
      return
    }
    setSavingTitle(true)
    try {
      const response = await fetch(`/api/v1/chats/${chat.uuid}/settings`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: nextTitle }),
      })
      if (response.ok) {
        const data = await response.json().catch(() => null)
        setExtraName(data?.settings?.title ?? "")
        await invalidateChatCaches(chat.uuid)
      }
    } finally {
      setSavingTitle(false)
    }
  }

  const onSaveExtraTitle = () => {
    void persistTitle(extraName.trim())
  }

  const onClearExtraTitle = () => {
    void persistTitle("")
  }

  const onResetExtraText = () => {
    setExtraName(chat?.settings?.title || "")
  }

  const togglePin = async () => {
    if (!chat?.uuid) {
      return
    }
    const nextPinned = !pinned
    setPinSaving(true)
    // Optimistically reflect the new pin state in every cached chat list so the
    // list reorders immediately (native refetches can lag behind the write).
    void mutate(
      (key) =>
        typeof key === "string" && key.startsWith("/api/v1/chats/list"),
      (current: any) => {
        if (!current || !Array.isArray(current.rows)) {
          return current
        }
        return {
          ...current,
          rows: current.rows.map((row: any) =>
            row?.uuid === chat.uuid ? { ...row, pinned: nextPinned } : row
          ),
        }
      },
      { revalidate: false }
    )
    try {
      // Use the shared authenticated fetcher (credentials + device-online
      // header + error handling) so the write is forwarded consistently on
      // native, where a bare same-origin fetch can be dropped by the proxy.
      await apiFetch(`/api/v1/chats/${chat.uuid}/pin`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pinned: nextPinned }),
      })
    } catch (error) {
      console.error("Failed to update chat pin state", error)
    } finally {
      await invalidateChatCaches(chat.uuid)
      setPinSaving(false)
    }
  }

  const deleteChat = async () => {
    if (!chat?.uuid) {
      return
    }
    setDeleting(true)
    try {
      const response = await fetch(`/api/v1/chats/${chat.uuid}`, { method: "DELETE" })
      if (response.ok) {
        await invalidateChatCaches(chat.uuid)
        setOpen(false)
        onDeleted?.(chat.uuid)
      }
    } finally {
      setDeleting(false)
      setConfirmDelete(false)
    }
  }

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen)
    if (!nextOpen) {
      setConfirmDelete(false)
    }
  }

  const titleSection = compact ? (
    <div className="flex items-center justify-between gap-2 px-2 py-1">
      <div className="min-w-0 flex-1">
        <p className="text-[10px] uppercase tracking-wide text-muted-foreground">Title</p>
        <span className="block truncate text-sm text-foreground">
          {chat?.settings?.title || chat?.partner?.name || "Untitled"}
        </span>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-8 shrink-0"
        aria-label="Edit chat title"
        onClick={() => setEditTitleOpen(true)}
      >
        <Icon name="pencil" size="sm" />
      </Button>
    </div>
  ) : (
    <div className="flex flex-row items-center gap-1 px-2 py-1">
      <Input
        type="text"
        value={extraName}
        onChange={(e) => setExtraName(e.target.value)}
        placeholder="Extra name"
        className="h-8 bg-background"
      />
      {(!extraName || !extraNameChanged) && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          aria-label="Edit chat title"
        >
          <Icon name="pencil" size="sm" />
        </Button>
      )}
      {extraName && extraNameChanged && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          aria-label="Save chat title"
          disabled={savingTitle}
          onClick={onSaveExtraTitle}
        >
          <Icon name="check" size="sm" />
        </Button>
      )}
      {chat?.settings?.title && !extraNameChanged && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          aria-label="Clear saved title"
          disabled={savingTitle}
          onClick={onClearExtraTitle}
        >
          <Icon name="trash" size="sm" />
        </Button>
      )}
      {!chat?.settings?.title && !extraNameChanged && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          aria-label="No custom title set"
        >
          <Icon name="eraser" size="sm" className="text-muted-foreground" />
        </Button>
      )}
      {extraNameChanged && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8 shrink-0"
          aria-label="Reset title changes"
          onClick={onResetExtraText}
        >
          <Icon name="undo" size="sm" />
        </Button>
      )}
    </div>
  )

  // Generic body shared by the dropdown (desktop) and the modal (small screens).
  // It deliberately avoids DropdownMenu-specific primitives so it can render in
  // either container.
  const content = (
    <div className="flex flex-col text-sm">
      <p className="px-2 pb-1 pt-0.5 text-sm font-semibold text-foreground">Chat Settings</p>
      <SettingsSeparator />
      {titleSection}
      <SettingsSeparator />
      <button
        type="button"
        disabled={pinSaving || !chat?.uuid}
        onClick={() => void togglePin()}
        className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors hover:bg-secondary disabled:pointer-events-none disabled:opacity-50"
      >
        {pinned ? <PinOff className="size-4" /> : <Pin className="size-4" />}
        {pinned ? "Unpin chat" : "Pin chat"}
      </button>
      <SettingsSeparator />
      <div className="px-2 py-1">
        <p className="mb-1 text-[10px] uppercase tracking-wide text-muted-foreground">Tags</p>
        <div className="mb-1 flex flex-wrap gap-1">
          {tags.length === 0 ? (
            <span className="text-[10px] text-muted-foreground">No tags</span>
          ) : (
            tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center gap-1 rounded-full border border-border/70 bg-secondary px-1.5 py-[1px] text-[10px]"
              >
                {tag}
                <button
                  type="button"
                  aria-label={`Remove ${tag} tag`}
                  className="text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
                  disabled={savingTags}
                  onClick={() => removeTag(tag)}
                >
                  <X className="size-3" />
                </button>
              </span>
            ))
          )}
        </div>
        <div className="flex items-center gap-1">
          <Input
            type="text"
            value={newTag}
            onChange={(event) => setNewTag(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault()
                addTag()
              }
            }}
            placeholder="Add tag"
            className="h-7 text-xs"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7 shrink-0"
            aria-label="Add tag"
            disabled={savingTags || normalizeTag(newTag) === ""}
            onClick={addTag}
          >
            <Plus className="size-3.5" />
          </Button>
        </div>
      </div>
      <SettingsSeparator />
      <button
        type="button"
        disabled={!chat?.uuid}
        onClick={() => {
          setShareOpen(true)
          handleOpenChange(false)
        }}
        className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors hover:bg-secondary disabled:pointer-events-none disabled:opacity-50"
      >
        <Share2 className="size-4" />
        Share chat
      </button>
      <SettingsSeparator />
      {chat?.partner?.is_bot && (
        <>
          <p className="px-2 py-1.5 text-xs font-normal text-muted-foreground">
            Bot chat actions coming soon
          </p>
          <SettingsSeparator />
        </>
      )}
      {confirmDelete ? (
        <div className="px-2 py-1">
          <p className="mb-2 text-xs text-muted-foreground">
            Delete this chat? This cannot be undone.
          </p>
          <div className="flex justify-end gap-1">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={deleting}
              onClick={() => setConfirmDelete(false)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={deleting}
              onClick={() => void deleteChat()}
            >
              {deleting ? "Deleting…" : "Delete"}
            </Button>
          </div>
        </div>
      ) : (
        <button
          type="button"
          disabled={deleting}
          onClick={() => setConfirmDelete(true)}
          className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm text-destructive transition-colors hover:bg-destructive/10 disabled:pointer-events-none disabled:opacity-50"
        >
          <Trash2 className="size-4" />
          Delete chat
        </button>
      )}
    </div>
  )

  const editTitleDialog = compact ? (
    <Dialog open={editTitleOpen} onOpenChange={setEditTitleOpen}>
      <DialogContent
        data-testid="chat-title-modal"
        className="w-[calc(100%-2rem)] max-w-xs gap-3 p-4"
      >
        <DialogTitle className="text-sm font-semibold">Edit chat title</DialogTitle>
        <Input
          autoFocus
          type="text"
          value={extraName}
          onChange={(event) => setExtraName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault()
              void persistTitle(extraName.trim()).then(() => setEditTitleOpen(false))
            }
          }}
          placeholder="Chat title"
          className="h-9 bg-background"
        />
        <div className="flex justify-end gap-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 px-3 text-xs"
            onClick={() => {
              setExtraName(chat?.settings?.title || "")
              setEditTitleOpen(false)
            }}
          >
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            className="h-8 px-3 text-xs"
            disabled={savingTitle}
            onClick={() => {
              void persistTitle(extraName.trim()).then(() => setEditTitleOpen(false))
            }}
          >
            {savingTitle ? "Saving…" : "Save"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  ) : null

  if (compact) {
    return (
      <>
        <Dialog open={open} onOpenChange={handleOpenChange}>
          {children({ Trigger })}
          <DialogContent
            data-testid="chat-settings-modal"
            className="max-h-[85vh] w-[calc(100%-2rem)] max-w-md gap-0 overflow-y-auto p-4"
          >
            <DialogTitle className="sr-only">Chat settings</DialogTitle>
            {content}
          </DialogContent>
        </Dialog>
        {editTitleDialog}
        <ShareChatModal chat={chat} open={shareOpen} setOpen={setShareOpen} />
      </>
    )
  }

  return (
    <>
      <DropdownMenu open={open} onOpenChange={handleOpenChange}>
        {children({ Trigger })}
        <DropdownMenuContent data-testid="chat-settings-dropdown" className="w-60" align="end">
          {content}
        </DropdownMenuContent>
      </DropdownMenu>
      <ShareChatModal chat={chat} open={shareOpen} setOpen={setShareOpen} />
    </>
  )
}
