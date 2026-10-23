import type { ReactNode } from "react"
import { Cookies } from "typescript-cookie"
import useSWR from "swr"
import { AccountCardButton } from "./AccountCardButton"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from "../dropdown-menu"
import { usePinnedIntegrationPages } from "../../integration/lib/use-pinned-integration-pages"

const fetcher = (...args: [RequestInfo, RequestInit?]) =>
  fetch(...args).then((res) => res.json())

export function ProfileCard({
  navigateTo,
  themeSelector,
  avatarSrc,
}: {
  navigateTo: (path: string) => void
  themeSelector: ReactNode
  avatarSrc?: string
}) {
  const { data: user } = useSWR<{ name?: string; email?: string; is_admin?: boolean }>(`/api/v1/user/self`, fetcher)
  const { data: integrations } = useSWR<{ rows?: Array<{ name?: string }> }>(
    user?.is_admin ? `/api/v1/integrations/list` : null,
    fetcher,
  )
  const displayName = user?.name?.trim() || user?.email || "My Account"
  const { pages: pinnedPages } = usePinnedIntegrationPages()
  const hasAdminIntegration = Boolean(
    integrations?.rows?.some((integration) => integration?.name === "admin"),
  )

  const onLogout = () => {
    fetch("/api/v1/user/logout", { method: "POST" }).then((res) => {
      if (res.ok) {
        navigateTo("/")
        Cookies.remove("oc_client_state")
      }
    })
  }

  return (
    <div className="w-full">
      <DropdownMenu>
        <AccountCardButton displayName={displayName} avatarSrc={avatarSrc} />
        <DropdownMenuContent className="w-56">
          <DropdownMenuLabel>My Account</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => navigateTo("/")}>
            Home
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => navigateTo("/chat")}>
            Chat
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => navigateTo("/chat/new")}>
            Bots
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => navigateTo("/integrations")}>
            Integrations
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {/* Pages the user pinned in the integration sidebar (ci#610): one
              click instead of walking /integrations -> integration -> page. */}
          {pinnedPages.length > 0 ? (
            <>
              <DropdownMenuGroup>
                <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">
                  Pinned pages
                </DropdownMenuLabel>
                {pinnedPages.map((page) => (
                  <DropdownMenuItem
                    key={page.path}
                    onClick={() => navigateTo(page.path)}
                    title={page.path}
                  >
                    {page.title}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
            </>
          ) : null}
          {user?.is_admin && hasAdminIntegration ? (
            <>
              <DropdownMenuGroup>
                <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">Admin</DropdownMenuLabel>
                <DropdownMenuItem onClick={() => navigateTo("/integrations/admin")}>Admin Overview</DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigateTo("/integrations/admin/models")}>Model Management</DropdownMenuItem>
                <DropdownMenuItem onClick={() => navigateTo("/integrations/admin/settings")}>Integration Settings</DropdownMenuItem>
              </DropdownMenuGroup>
              <DropdownMenuSeparator />
            </>
          ) : null}
          <DropdownMenuGroup>
            <DropdownMenuLabel className="px-2 py-1.5 text-xs text-muted-foreground">Api</DropdownMenuLabel>
            <DropdownMenuItem onClick={() => navigateTo("/chat/api/")}>
              API Keys
            </DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={onLogout}>
            Log out
            <DropdownMenuShortcut>⇧⌘Q</DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
