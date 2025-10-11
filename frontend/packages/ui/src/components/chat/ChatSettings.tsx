import { useEffect, useState, type ReactNode } from "react"
import { Button } from "../button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "../dropdown-menu"
import { Input } from "../input"
import { Icon } from "../icon"

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
  const extraNameChanged =
    extraName !== chat?.settings?.title && extraName !== ""

  useEffect(() => {
    setExtraName(chat?.settings?.title || "")
  }, [chat?.settings?.title])

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
