import type { ReactNode } from "react"
import { AccountCardButton } from "./AccountCardButton"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "../dropdown-menu"
import { Icon } from "../icon"

export function GuestAccountCard({
  navigateTo,
  themeSelector,
  avatarSrc,
}: {
  navigateTo: (path: string) => void
  themeSelector: ReactNode
  avatarSrc?: string
}) {
  return (
    <div className="shadow-xl bg-background">
      <DropdownMenu>
        <AccountCardButton
          displayName="Guest"
          hint={<Icon name="menu" size="sm" className="text-muted-foreground" />}
          avatarSrc={avatarSrc}
        />
        <DropdownMenuContent className="w-56">
          <DropdownMenuLabel>Open-Chat Go</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigateTo("/")}>
            Home
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => navigateTo("/chat/new")}>
            Bots
          </DropdownMenuItem>
          <DropdownMenuGroup>
            <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">Api</DropdownMenuLabel>
            <DropdownMenuItem onClick={() => navigateTo("/chat/api/")}>
              API Keys
            </DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigateTo("/login")}>
            Log in
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
