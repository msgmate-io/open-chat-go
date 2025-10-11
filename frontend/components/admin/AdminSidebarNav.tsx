import useSWR from "swr";
import { Database, ListTodo, Settings2, ShieldCheck } from "lucide-react";
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@open-chat-go/ui";

const fetcher = (...args: [RequestInfo, RequestInit?]) =>
  fetch(...args).then((res) => res.json());

function getActiveTableName(pathname: string): string | null {
  const match = pathname.match(/^\/integrations\/admin\/([^/]+)/);
  if (!match) {
    return null;
  }
  const candidate = match[1];
  if (candidate === "models" || candidate === "queues" || candidate === "settings") {
    return null;
  }
  return candidate;
}

export function AdminSidebarNav({ navigateTo }: { navigateTo: (path: string) => void }) {
  const pathname = typeof window !== "undefined" ? window.location.pathname : "/integrations/admin";
  const activeTable = getActiveTableName(pathname);
  const { data: tables } = useSWR<Array<{ name: string }>>(
    "/api/v1/admin/tables",
    fetcher,
  );

  return (
    <SidebarGroup>
      <SidebarGroupLabel>Administration</SidebarGroupLabel>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton
            isActive={pathname === "/integrations/admin" || pathname === "/integrations/admin/"}
            tooltip="Admin dashboard"
            onClick={() => navigateTo("/integrations/admin")}
          >
            <ShieldCheck className="size-4 shrink-0" />
            <span>Dashboard</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            isActive={pathname === "/integrations/admin/queues" || pathname.startsWith("/integrations/admin/queues/")}
            tooltip="Asynq queue overview"
            onClick={() => navigateTo("/integrations/admin/queues")}
          >
            <ListTodo className="size-4 shrink-0" />
            <span>Queues</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            isActive={pathname === "/integrations/admin/models" || pathname === "/integrations/admin/models/"}
            tooltip="Admin model management"
            onClick={() => navigateTo("/integrations/admin/models")}
          >
            <ShieldCheck className="size-4 shrink-0" />
            <span>Model Management</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton
            isActive={pathname === "/integrations/admin/settings" || pathname.startsWith("/integrations/admin/settings/")}
            tooltip="Integration settings"
            onClick={() => navigateTo("/integrations/admin/settings")}
          >
            <Settings2 className="size-4 shrink-0" />
            <span>Integration Settings</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
        {tables?.map((table) => {
          return (
            <SidebarMenuItem key={table.name}>
              <SidebarMenuButton
                isActive={activeTable === table.name}
                onClick={() => navigateTo(`/integrations/admin/${table.name}`)}
              >
                <Database className="size-4 shrink-0" />
                <span>{table.name}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          );
        })}
      </SidebarMenu>
    </SidebarGroup>
  );
}
