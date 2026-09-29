import { ChatBase } from "./chat/ChatBase";
import { navigate } from "vike/client/router";
import { usePageContext } from "vike-react/usePageContext";
import type { ReactNode } from "react";

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

  return (
    <ChatBase
      chatUUID={chatUUID}
      navigateTo={(to: string) => navigate(to)}
      sidebar={sidebar}
      sidebarTitle={sidebarTitle}
      sidebarTopSection={topSection}
      integrationName={resolvedIntegrationName}
    >
      <div
        className={`mx-auto flex h-full min-h-0 w-full ${maxWidthClassName} flex-col gap-4 overflow-y-auto px-3 py-4 sm:px-4 md:px-6 ${className}`.trim()}
      >
        {children}
      </div>
    </ChatBase>
  );
}
