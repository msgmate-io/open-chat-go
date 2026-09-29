import type { ReactNode } from "react"
import { Card } from "../card"
import { DropdownMenuTrigger } from "../dropdown-menu"
import { Icon } from "../icon"

export function AccountCardButton({
  displayName,
  hint,
  avatarSrc,
}: {
  displayName: string
  hint?: ReactNode
  avatarSrc?: string
}) {
  const menuHint = hint ?? (
    <Icon name="chevron-down" size="sm" className="text-muted-foreground" />
  )

  return (
    <DropdownMenuTrigger asChild>
      <Card className="flex min-w-0 w-full cursor-pointer rounded-lg border border-border bg-card p-0 hover:bg-accent">
        <div className="flex h-12 w-12 shrink-0 overflow-hidden rounded-l-lg border-r border-border">
          {avatarSrc ? (
            <img src={avatarSrc} className="block h-full w-full object-contain" alt="" />
          ) : (
            <div className="flex h-full w-full items-center justify-center bg-muted text-lg font-semibold">
              {displayName.charAt(0).toUpperCase()}
            </div>
          )}
        </div>
        <div className="flex min-w-0 flex-1 items-center content-center justify-start pr-2">
          <div className="relative min-w-0 flex-1 p-2 text-sm">
            <span className="block truncate">{displayName}</span>
            <span
              aria-hidden="true"
              className="pointer-events-none absolute inset-y-0 right-0 w-8 bg-gradient-to-r from-transparent to-card"
            />
          </div>
          <div className="shrink-0">{menuHint}</div>
        </div>
      </Card>
    </DropdownMenuTrigger>
  )
}
