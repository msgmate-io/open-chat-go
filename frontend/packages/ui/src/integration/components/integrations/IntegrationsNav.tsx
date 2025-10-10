import { useMemo, type ReactNode } from "react";
import useSWR from "swr";
import { ArrowLeft } from "lucide-react";
import { cn, Text, TextTypes } from "@open-chat-go/ui";

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then((res) => res.json());

type IntegrationRow = {
  name: string;
  admin_only?: boolean;
  user_accessible?: boolean;
};

type IntegrationsResponse = {
  rows: IntegrationRow[];
};

type IntegrationFrontendRoute = {
  route: string;
  kind: string;
  public: boolean;
  description?: string;
  asset_path?: string;
};

type IntegrationOverviewResponse = {
  frontend_routes?: IntegrationFrontendRoute[];
};

function normalizePath(path: string | undefined): string {
  if (!path) {
    return "";
  }
  return path.split("?")[0].split("#")[0].replace(/\/+$/, "");
}

function shortRoutePath(route: string, integrationName: string | null): string {
  const prefix = integrationName ? `/integrations/${integrationName}/` : "";
  if (prefix && route.startsWith(prefix)) {
    return route.slice(prefix.length).replace(/\/+$/, "");
  }
  return route.replace(/^\/integrations\//, "").replace(/\/+$/, "");
}

function routeTitle(route: string, integrationName: string | null): string {
  const short = shortRoutePath(route, integrationName);
  const segments = short.split("/").filter(Boolean);
  const last = segments[segments.length - 1] || integrationName || "Page";
  return last
    .split(/[-_]/)
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}

function NavRow({
  label,
  secondary,
  selected = false,
  icon,
  onSelect,
}: {
  label: string;
  secondary?: string;
  selected?: boolean;
  icon?: ReactNode;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "chat-list-row flex w-full items-center gap-2 px-3 py-2 text-left",
        selected && "chat-list-row--selected",
      )}
    >
      {icon}
      <span className="min-w-0 flex-1">
        <Text type={TextTypes.Body6} bold className="block truncate">
          {label}
        </Text>
        {secondary ? (
          <Text type={TextTypes.Body7} color="muted" className="block truncate font-mono">
            {secondary}
          </Text>
        ) : null}
      </span>
    </button>
  );
}

export function IntegrationsNav({
  integrationName,
  activePath,
  navigateTo,
}: {
  integrationName: string | null;
  activePath?: string;
  navigateTo: (to: string) => void;
}) {
  const isOverview = integrationName === null;
  const { data: listData, isLoading: listLoading } = useSWR<IntegrationsResponse>(
    isOverview ? "/api/v1/integrations/list" : null,
    fetcher,
  );
  const { data: overviewData, isLoading: overviewLoading } = useSWR<IntegrationOverviewResponse>(
    !isOverview && integrationName
      ? `/api/v1/integrations/${encodeURIComponent(integrationName)}/overview`
      : null,
    fetcher,
  );

  const integrations = useMemo(
    () => [...(listData?.rows ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [listData],
  );

  const pages = useMemo(() => {
    const routes = overviewData?.frontend_routes ?? [];
    return routes
      .filter((route) => route.kind === "page" && typeof route.route === "string" && !route.route.includes("{"))
      .sort((a, b) => a.route.localeCompare(b.route));
  }, [overviewData]);

  const currentPath = normalizePath(activePath);

  if (isOverview) {
    return (
      <div className="flex flex-col gap-1.5">
        <NavRow
          label="Chats"
          icon={<ArrowLeft className="size-4 shrink-0 text-muted-foreground" />}
          onSelect={() => navigateTo("/chat")}
        />
        <div className="chat-list-divider">
          <Text type={TextTypes.Body7} color="muted" tag="span" bold>
            Integrations
          </Text>
        </div>
        {listLoading ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-12 animate-pulse rounded-lg bg-muted" />
            ))}
          </div>
        ) : integrations.length === 0 ? (
          <Text type={TextTypes.Body7} color="muted" className="px-1 py-2">
            No integrations available
          </Text>
        ) : (
          integrations.map((integration) => (
            <NavRow
              key={integration.name}
              label={integration.name}
              secondary={integration.admin_only ? "admin" : undefined}
              selected={currentPath === `/integrations/${integration.name}`}
              onSelect={() => navigateTo(`/integrations/${encodeURIComponent(integration.name)}/`)}
            />
          ))
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      <NavRow
        label="Integrations"
        icon={<ArrowLeft className="size-4 shrink-0 text-muted-foreground" />}
        onSelect={() => navigateTo("/integrations")}
      />
      <div className="chat-list-divider">
        <Text type={TextTypes.Body7} color="muted" tag="span" bold>
          {integrationName}
        </Text>
      </div>
      {overviewLoading ? (
        <div className="space-y-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-12 animate-pulse rounded-lg bg-muted" />
          ))}
        </div>
      ) : pages.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted" className="px-1 py-2">
          No frontend pages registered
        </Text>
      ) : (
        pages.map((page) => {
          const secondary = shortRoutePath(page.route, integrationName);
          return (
            <NavRow
              key={page.route}
              label={page.description?.trim() || routeTitle(page.route, integrationName)}
              secondary={secondary || undefined}
              selected={currentPath === normalizePath(page.route)}
              onSelect={() => navigateTo(page.route)}
            />
          );
        })
      )}
    </div>
  );
}
