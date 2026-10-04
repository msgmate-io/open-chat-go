import { useEffect, useState, type ReactNode } from "react"
import { mutate } from "swr"
import { Plus, X } from "lucide-react"
import { Button } from "../button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "../dropdown-menu"
import { Input } from "../input"
import { Icon } from "../icon"

const normalizeTag = (tag: string) => tag.trim().toLowerCase()

const invalidateChatCaches = () =>
  mutate(
    (key) =>
      typeof key === "string" &&
      (key.startsWith("/api/v1/chats/list") || key === "/api/v1/chats/tags"),
    undefined,
    { revalidate: true }
  )

export function ChatSettings({
  chat,
  open,
  setOpen,
  children,
}: {
  chat: any
  open: boolean
  setOpen: (open: boolean) => void
  children: ReactNode
}) {
  const [markedForDeletion, setMarkedForDeletion] = useState(false)
  const [extraName, setExtraName] = useState(chat?.settings?.title || "")
  const [tags, setTags] = useState<string[]>(Array.isArray(chat?.tags) ? chat.tags : [])
  const [newTag, setNewTag] = useState("")
  const [savingTags, setSavingTags] = useState(false)
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
      await invalidateChatCaches()
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

  useEffect(() => {
    if (markedForDeletion) {
      //dispatch(deleteChat({ chatId: chat?.uuid }))
    }
  }, [markedForDeletion])

  const onSaveExtraTitle = () => {
    /*
        api.chatsSettingsCreate(chat?.uuid, { title: extraName }).then((res) => {
            //dispatch(updateChatSettings({ chatId: chat?.uuid, settings: res }))
        }).catch((error) => {
            toast.error(`Failed to save extra title: ${JSON.stringify(error)}`)
        })*/
  }

  const onResetExtraText = () => {
    setExtraName(chat?.settings?.title || "")
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
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
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
