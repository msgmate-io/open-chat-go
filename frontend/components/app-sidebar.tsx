import * as React from "react"
import { useEffect, useMemo, useState } from "react"
import { Boxes, User, Wrench } from "lucide-react"
import useSWR from "swr"

import { NavMain } from "@/components/nav-main"
import { AdminSidebarNav } from "@/components/admin/AdminSidebarNav"
import { ConnectedThemeSelector } from "@open-chat-go/ui"
import { useCurrentUser } from "@open-chat-go/ui"
import { fetcher } from "@/lib/utils"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
  ProfileCard,
  GuestAccountCard,
} from "@open-chat-go/ui"
import logoUrl from "@/assets/logo.png?inline"

const profileNavItem = {
  title: "Profile",
  url: "#",
  icon: User,
  isActive: false,
  items: [
    {
      title: "View Profile",
      url: "/profile",
    },
    {
      title: "Bots",
      url: "/profile/bots",
    },
    {
      title: "2FA Settings",
      url: "/profile/2fa",
    },
    {
      title: "API Tokens",
      url: "/profile/access-tokens",
    },
    {
      title: "Integrations",
      url: "/integrations",
    },
  ],
}

const modelsNavItem = {
  title: "Models",
  url: "#",
  icon: Boxes,
  isActive: false,
  items: [
    {
      title: "Overview",
      url: "/models",
    },
  ],
}

const toolsNavItem = {
  title: "Tools",
  url: "#",
  icon: Wrench,
  isActive: false,
  items: [
    {
      title: "Overview",
      url: "/tools",
    },
		{
			title: "MCP Integrations",
			url: "/tools/mcp",
		},
  ],
}

type AppSidebarProps = React.ComponentProps<typeof Sidebar> & {
  navigateTo?: (path: string) => void;
  accessMode?: "private" | "public";
};

type IntegrationsResponse = {
  rows: Array<{ name: string }>;
};

function getPageKind(pathname: string): "admin" | "other" {
  if (pathname === "/integrations/admin" || pathname.startsWith("/integrations/admin/")) return "admin";
  return "other";
}

export function AppSidebar({
  navigateTo = () => {},
  accessMode = "private",
  ...props
}: AppSidebarProps) {
  const [pageKind, setPageKind] = useState<"admin" | "other">(() =>
    typeof window !== "undefined" ? getPageKind(window.location.pathname) : "other"
  );
  const { data: user } = useCurrentUser();
  const { data: integrations } = useSWR<IntegrationsResponse>(
    user?.is_admin ? "/api/v1/integrations/list" : null,
    fetcher,
  );
  const isAdminIntegrationActive = Boolean(
    integrations?.rows?.some((integration) => integration.name === "admin"),
  );
  const themeSelector = <ConnectedThemeSelector />;

  useEffect(() => {
    const syncPath = () => setPageKind(getPageKind(window.location.pathname));
    syncPath();
    window.addEventListener("popstate", syncPath);
    window.addEventListener("hashchange", syncPath);
    return () => {
      window.removeEventListener("popstate", syncPath);
      window.removeEventListener("hashchange", syncPath);
    };
  }, []);

  const navItems = useMemo(() => {
    const pathname = typeof window !== "undefined" ? window.location.pathname : "";
    const profileItems = [...profileNavItem.items];
    if (user?.is_admin && isAdminIntegrationActive) {
      profileItems.push({
        title: "Admin Overview",
        url: "/integrations/admin",
      });
      profileItems.push({
        title: "Admin Models",
        url: "/integrations/admin/models",
      });
      profileItems.push({
        title: "Integration Settings",
        url: "/integrations/admin/settings",
      });
    }
    const items = [
      {
        ...profileNavItem,
        items: profileItems,
        isActive: pathname.startsWith("/profile") || pathname.startsWith("/integrations"),
      },
      {
        ...modelsNavItem,
        isActive: pathname.startsWith("/models"),
      },
      {
        ...toolsNavItem,
        isActive: pathname.startsWith("/tools"),
      },
    ];
    return items;
  }, [isAdminIntegrationActive, user?.is_admin]);

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader />
      <SidebarContent>
        {pageKind === "admin" && user?.is_admin && (
          <AdminSidebarNav navigateTo={navigateTo} />
        )}
        {accessMode === "private" && <NavMain items={navItems} navigateTo={navigateTo} />}
      </SidebarContent>
      <SidebarFooter>
        {accessMode === "private" ? (
          <ProfileCard navigateTo={navigateTo} themeSelector={themeSelector} avatarSrc={logoUrl} />
        ) : (
          <GuestAccountCard navigateTo={navigateTo} themeSelector={themeSelector} avatarSrc={logoUrl} />
        )}
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
