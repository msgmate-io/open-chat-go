import { useMemo, useState, type ReactNode } from "react";
import useSWR from "swr";
import { ArrowLeft, Pin, PinOff, Search } from "lucide-react";
import { cn, Text, TextTypes } from "@open-chat-go/ui";
import { fetcher } from "../../lib/utils";
import {
  filterIntegrationRows,
  integrationDisplayTitle,
  isPagePinned,
  normalizeIntegrationPath,
  pinnedPagesForIntegration,
  resolveIntegrationLandingPath,
  togglePinnedPage,
  type IntegrationNavRow,
  type PinnedPageInput,
} from "../../lib/pinned-integrations";
import { usePinnedIntegrationPages } from "../../lib/use-pinned-integration-pages";

type IntegrationRow = IntegrationNavRow;

type IntegrationsResponse = {
  rows: IntegrationRow[];
};

type IntegrationFrontendRoute = {
  route: string;
  kind: string;
  public: boolean;
  description?: string;
  asset_path?: string;
  /** Integration-provided readable title (ci#610). */
  title?: string;
  section?: string;
  order?: number;
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

/** Small star toggle rendered next to a page row (ci#610). */
function PinToggle({
  path,
  title,
  integrationTitle,
  pinned,
  onToggle,
}: {
  path: string;
  title: string;
  integrationTitle?: string;
  pinned: boolean;
  onToggle: (page: PinnedPageInput) => void;
}) {
  const label = pinned ? `Unpin ${title}` : `Pin ${title}`;
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={pinned}
      onClick={(event) => {
        event.stopPropagation();
        onToggle(
          integrationTitle
            ? { path, title, integrationTitle }
            : { path, title },
        );
      }}
      className={cn(
        "flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground",
        pinned && "text-primary hover:text-primary",
      )}
    >
      {pinned ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
    </button>
  );
}

function NavRow({
  label,
  secondary,
  selected = false,
  icon,
  action,
  onSelect,
}: {
  label: string;
  secondary?: string;
  selected?: boolean;
  icon?: ReactNode;
  action?: ReactNode;
  onSelect: () => void;
}) {
  return (
    <div className="flex items-center gap-0.5">
      <button
        type="button"
        onClick={onSelect}
        className={cn(
          "chat-list-row flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left",
          selected && "chat-list-row--selected",
        )}
      >
        {icon}
        <span className="min-w-0 flex-1">
          <Text type={TextTypes.Body6} bold className="block truncate">
            {label}
          </Text>
          {secondary ? (
            <Text
              type={TextTypes.Body7}
              color="muted"
              className="block truncate font-mono"
            >
              {secondary}
            </Text>
          ) : null}
        </span>
      </button>
      {action}
    </div>
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
  const [query, setQuery] = useState("");
  const {
    pages: pinnedPages,
    toggle: togglePin,
    landingPath,
  } = usePinnedIntegrationPages();

  const { data: listData, isLoading: listLoading } =
    useSWR<IntegrationsResponse>(
      isOverview ? "/api/v1/integrations/list" : null,
      fetcher,
    );
  const { data: overviewData, isLoading: overviewLoading } =
    useSWR<IntegrationOverviewResponse>(
      !isOverview && integrationName
        ? `/api/v1/integrations/${encodeURIComponent(integrationName)}/overview`
        : null,
      fetcher,
    );

  const integrations = useMemo(
    () => filterIntegrationRows(listData?.rows ?? [], query),
    [listData, query],
  );

  const pages = useMemo(() => {
    const routes = overviewData?.frontend_routes ?? [];
    const base = integrationName ? `/integrations/${integrationName}` : "";
    return routes
      .filter(
        (route) =>
          route.kind === "page" &&
          typeof route.route === "string" &&
          !route.route.includes("{"),
      )
      .filter((route) => {
        if (route.public) return false;
        if (!base) return true;
        return route.route === base || route.route.startsWith(`${base}/`);
      })
      .sort((a, b) => a.route.localeCompare(b.route));
  }, [overviewData, integrationName]);

  const docs = useMemo(() => {
    const routes = overviewData?.frontend_routes ?? [];
    const base = integrationName ? `/integrations/${integrationName}` : "";
    return routes
      .filter(
        (route) =>
          route.kind === "doc" &&
          typeof route.route === "string" &&
          !route.route.includes("{"),
      )
      .filter((route) => {
        if (route.public) return false;
        if (!base) return true;
        return route.route === base || route.route.startsWith(`${base}/`);
      })
      .sort((a, b) => {
        const sectionOrder = (a.section || "").localeCompare(b.section || "");
        if (sectionOrder !== 0) return sectionOrder;
        const orderDiff =
          (a.order ?? Number.MAX_SAFE_INTEGER) -
          (b.order ?? Number.MAX_SAFE_INTEGER);
        if (orderDiff !== 0) return orderDiff;
        return a.route.localeCompare(b.route);
      });
  }, [overviewData, integrationName]);

  const currentPath = normalizePath(activePath);

  // Pages of the active integration the user pinned, rendered above the rest so
  // the frequently used ones are always one click away (ci#610).
  const pinnedHere = useMemo(
    () =>
      integrationName
        ? pinnedPagesForIntegration(pinnedPages, integrationName).filter(
            (page) => normalizePath(page.path) !== currentPath,
          )
        : [],
    [pinnedPages, integrationName, currentPath],
  );
  const pinnedHerePaths = useMemo(
    () => new Set(pinnedHere.map((page) => page.path)),
    [pinnedHere],
  );

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
        <label className="flex items-center gap-2 px-2 pb-1">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Filter integrations"
            aria-label="Filter integrations"
            className="w-full bg-transparent py-1 text-sm outline-none placeholder:text-muted-foreground"
          />
        </label>
        {pinnedPages.length > 0 ? (
          <>
            <div className="chat-list-divider">
              <Text type={TextTypes.Body7} color="muted" tag="span" bold>
                Pinned pages
              </Text>
            </div>
            {pinnedPages.map((page) => (
              <NavRow
                key={`pinned-${page.path}`}
                label={page.title}
                secondary={page.path.replace(/^\/integrations\//, "")}
                selected={currentPath === normalizePath(page.path)}
                action={
                  <PinToggle
                    path={page.path}
                    title={page.title}
                    integrationTitle={page.integrationTitle}
                    pinned
                    onToggle={(entry) => togglePin(entry)}
                  />
                }
                onSelect={() => navigateTo(page.path)}
              />
            ))}
          </>
        ) : null}
        <div className="chat-list-divider">
          <Text type={TextTypes.Body7} color="muted" tag="span" bold>
            All integrations
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
            {query.trim()
              ? "No integrations match this filter"
              : "No integrations available"}
          </Text>
        ) : (
          integrations.map((integration) => (
            <NavRow
              key={integration.name}
              label={integrationDisplayTitle(integration)}
              secondary={
                (integration.labels ?? []).join(", ") ||
                integration.name ||
                (integration.admin_only ? "admin" : undefined)
              }
              selected={currentPath === `/integrations/${integration.name}`}
              // Selecting an integration opens the page the user pinned for it
              // (else its overview) instead of always landing on the overview.
              onSelect={() => navigateTo(landingPath(integration.name))}
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
      <NavRow
        label="Overview"
        secondary="Integration start page"
        selected={
          currentPath === normalizePath(`/integrations/${integrationName}`)
        }
        onSelect={() =>
          navigateTo(
            `/integrations/${encodeURIComponent(integrationName as string)}`,
          )
        }
      />
      <div className="chat-list-divider">
        <Text type={TextTypes.Body7} color="muted" tag="span" bold>
          {integrationDisplayTitle(integrationName as string)}
        </Text>
      </div>
      {pinnedHere.length > 0 ? (
        <>
          <div className="chat-list-divider">
            <Text type={TextTypes.Body7} color="muted" tag="span" bold>
              Pinned
            </Text>
          </div>
          {pinnedHere.map((page) => (
            <NavRow
              key={`pinned-${page.path}`}
              label={page.title}
              secondary={
                shortRoutePath(page.path, integrationName) || undefined
              }
              action={
                <PinToggle
                  path={page.path}
                  title={page.title}
                  integrationTitle={page.integrationTitle}
                  pinned
                  onToggle={(entry) => togglePin(entry)}
                />
              }
              onSelect={() => navigateTo(page.path)}
            />
          ))}
        </>
      ) : null}
      {overviewLoading ? (
        <div className="space-y-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-12 animate-pulse rounded-lg bg-muted" />
          ))}
        </div>
      ) : pages.length === 0 && docs.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted" className="px-1 py-2">
          No frontend pages registered
        </Text>
      ) : (
        <>
          {pages
            .filter((page) => {
              const normalized = normalizeIntegrationPath(page.route);
              return !normalized || !pinnedHerePaths.has(normalized);
            })
            .map((page) => {
              const secondary = shortRoutePath(page.route, integrationName);
              // Prefer the integration-provided readable title over a derived one.
              const label =
                page.title?.trim() ||
                page.description?.trim() ||
                routeTitle(page.route, integrationName);
              const normalized = normalizeIntegrationPath(page.route);
              return (
                <NavRow
                  key={page.route}
                  label={label}
                  secondary={secondary || undefined}
                  selected={currentPath === normalizePath(page.route)}
                  action={
                    normalized ? (
                      <PinToggle
                        path={normalized}
                        title={label}
                        integrationTitle={integrationDisplayTitle(
                          integrationName as string,
                        )}
                        pinned={isPagePinned(pinnedPages, normalized)}
                        onToggle={(entry) => togglePin(entry)}
                      />
                    ) : undefined
                  }
                  onSelect={() => navigateTo(page.route)}
                />
              );
            })}
          {docs.length > 0 ? (
            <>
              <div className="chat-list-divider">
                <Text type={TextTypes.Body7} color="muted" tag="span" bold>
                  Documentation
                </Text>
              </div>
              {docs.map((doc) => (
                <NavRow
                  key={doc.route}
                  label={
                    doc.title?.trim() ||
                    doc.description?.trim() ||
                    routeTitle(doc.route, integrationName)
                  }
                  secondary={doc.section?.trim() || undefined}
                  selected={currentPath === normalizePath(doc.route)}
                  onSelect={() => navigateTo(doc.route)}
                />
              ))}
            </>
          ) : null}
        </>
      )}
      {pinnedPages.length === 0 && pages.length > 0 ? (
        <Text type={TextTypes.Body7} color="muted" className="px-3 pt-1">
          Tip: pin a page to open it first and list it in your profile menu.
        </Text>
      ) : null}
    </div>
  );
}

export { togglePinnedPage, resolveIntegrationLandingPath };
