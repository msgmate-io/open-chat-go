import { ChatBase } from "./chat/ChatBase";
import { navigate } from "vike/client/router";
import { usePageContext } from "vike-react/usePageContext";
import type { ReactNode } from "react";
import { normalizePathname } from "../lib/pinned-integrations";

type IntegrationPageShellProps = {
  children: ReactNode;
  chatUUID?: string | null;
  maxWidthClassName?: string;
  className?: string;
  sidebar?: ReactNode;
  sidebarTitle?: string;
  topSection?: ReactNode;
  integrationName?: string | null;
};

function deriveIntegrationName(pathname: string): string | null {
  const match = /^\/integrations\/([^/?#]+)/.exec(pathname);
  if (!match) {
    return null;
  }
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return match[1];
  }
}

// The integrations index (`/integrations`) and a per-integration overview
// (`/integrations/<name>`) are "master" routes: on native/mobile they should
// render the integration list (the sidebar) directly instead of first painting
// the desktop overview grid and forcing the user through an in-place toggle.
// Deeper routes (a specific settings page) stay in content mode.
function isIntegrationOverviewPath(pathname: string): boolean {
  // ci#737: linear-time normalization (was `.replace(/\/+$/, "")`).
  const path = normalizePathname(pathname);
  return /^\/integrations(?:\/[^/]+)?$/.test(path);
}

export function IntegrationPageShell({
  children,
  chatUUID = null,
  maxWidthClassName = "max-w-5xl",
  className = "",
  sidebar,
  sidebarTitle,
  topSection,
  integrationName,
}: IntegrationPageShellProps) {
  const pageContext = usePageContext();
  const activePath = pageContext?.urlPathname || (typeof window !== "undefined" ? window.location.pathname : "");
  const resolvedIntegrationName = integrationName !== undefined ? integrationName : deriveIntegrationName(activePath);
  // On native/mobile, open the master list view for the integration index and
  // per-integration overview routes instead of the overview content.
  const mobileViewMode = isIntegrationOverviewPath(activePath) ? "list" : "content";

  return (
    <ChatBase
      chatUUID={chatUUID}
      navigateTo={(to: string) => navigate(to)}
      sidebar={sidebar}
      sidebarTitle={sidebarTitle}
      sidebarTopSection={topSection}
      integrationName={resolvedIntegrationName}
      mobileViewMode={mobileViewMode}
    >
      <div
        className={`mx-auto flex h-full min-h-0 w-full ${maxWidthClassName} flex-col gap-3 overflow-y-auto px-3 py-3 sm:px-4 md:px-5 ${className}`.trim()}
      >
        {children}
      </div>
    </ChatBase>
  );
}
