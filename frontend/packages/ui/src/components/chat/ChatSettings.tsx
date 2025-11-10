import { useEffect, useState, type ReactNode } from "react"
import { mutate } from "swr"
import { Plus, Trash2, X } from "lucide-react"
import { Button } from "../button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "../dropdown-menu"
import { Input } from "../input"
import { Icon } from "../icon"

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

export function ChatSettings({
  chat,
  open,
  setOpen,
  onDeleted,
  children,
}: {
  chat: any
  open: boolean
  setOpen: (open: boolean) => void
  onDeleted?: (chatUUID: string) => void
  children: ReactNode
}) {
  const [extraName, setExtraName] = useState(chat?.settings?.title || "")
  const [tags, setTags] = useState<string[]>(Array.isArray(chat?.tags) ? chat.tags : [])
  const [newTag, setNewTag] = useState("")
  const [savingTags, setSavingTags] = useState(false)
  const [savingTitle, setSavingTitle] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const extraNameChanged =
    extraName !== chat?.settings?.title && extraName !== ""

  useEffect(() => {
    setExtraName(chat?.settings?.title || "")
  }, [chat?.settings?.title])

  useEffect(() => {
    setTags(Array.isArray(chat?.tags) ? chat.tags : [])
  }, [chat?.tags])

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

  return (
    <DropdownMenu
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen) {
          setConfirmDelete(false)
        }
      }}
    >
      {children}
      <DropdownMenuContent className="w-56" align="end">
        <DropdownMenuLabel>Chat Settings</DropdownMenuLabel>
        <DropdownMenuSeparator />
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
        <DropdownMenuSeparator />
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
        <DropdownMenuSeparator />
        {chat?.partner?.is_bot && (
          <>
            <DropdownMenuLabel className="font-normal text-muted-foreground">
              Bot chat actions coming soon
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
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
          <DropdownMenuItem
            variant="destructive"
            disabled={deleting}
            onSelect={(event) => {
              event.preventDefault()
              setConfirmDelete(true)
            }}
          >
            <Trash2 className="size-4" />
            Delete chat
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
