export type PageMetadata = {
  pageName: string;
  sidebarTitle: string;
};

function isUUIDLike(value: string): boolean {
  return /^[a-zA-Z0-9-]{8,}$/.test(value);
}

export function getPageMetadata(pathname: string): PageMetadata {
  const path = (pathname || "/").trim();

  if (path === "/integrations/admin" || path === "/integrations/admin/") {
    return { pageName: "Admin: Overview", sidebarTitle: "Admin" };
  }
  if (path === "/integrations/admin/queues" || path === "/integrations/admin/queues/") {
    return { pageName: "Admin: Queues", sidebarTitle: "Admin" };
  }
  if (path === "/integrations/admin/models" || path === "/integrations/admin/models/") {
    return { pageName: "Admin: Models", sidebarTitle: "Admin" };
  }
  if (path === "/integrations/admin/settings" || path === "/integrations/admin/settings/") {
    return { pageName: "Admin: Integration Settings", sidebarTitle: "Admin" };
  }
  if (path.startsWith("/integrations/admin/")) {
    return { pageName: "Admin: Table", sidebarTitle: "Admin" };
  }
  if (path === "/integrations" || path === "/integrations/") {
    return { pageName: "Integrations", sidebarTitle: "Integrations" };
  }
  if (path.startsWith("/integrations/")) {
    return { pageName: "Integration", sidebarTitle: "Integrations" };
  }
  if (path.startsWith("/docs")) {
    return { pageName: "Docs", sidebarTitle: "Docs" };
  }
  if (path === "/models") {
    return { pageName: "Models", sidebarTitle: "Models" };
  }
  if (path === "/tools") {
    return { pageName: "Tools", sidebarTitle: "Tools" };
  }
  if (path === "/tools/mcp") {
    return { pageName: "Tools: MCP Integrations", sidebarTitle: "Tools" };
  }
  if (path === "/profile" || path.startsWith("/profile/")) {
    return { pageName: "Profile", sidebarTitle: "Profile" };
  }
  if (path === "/chat/new" || path.startsWith("/chat/new/")) {
    return { pageName: "Browse Agents", sidebarTitle: "Browse Agents" };
  }
  if (path === "/chat") {
    return { pageName: "Chats", sidebarTitle: "Chats" };
  }
  if (path.startsWith("/chat/")) {
    const chatID = path.slice("/chat/".length).split("/")[0] || "";
    if (isUUIDLike(chatID)) {
      return { pageName: "Conversation", sidebarTitle: "Conversation" };
    }
  }
  return { pageName: "Open Chat", sidebarTitle: "Chats" };
}

export function applyDocumentTitle(pathname: string): void {
  const { pageName } = getPageMetadata(pathname);
  document.title = `Open-Chat | ${pageName}`;
}
