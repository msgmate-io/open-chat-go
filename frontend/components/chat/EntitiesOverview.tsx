import { useEffect, useMemo, useState } from "react";
import useSWR from "swr";
import { Boxes, Grid2x2, LayoutGrid, List, Search, SlidersHorizontal, X } from "lucide-react";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  LoadingSpinner,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { cn, fetcher } from "@/lib/utils";
import { useBreakpoint } from "@/components/utils";
import { CollapseIndicator } from "@open-chat-go/ui";
import { useSidePanelCollapse } from "@open-chat-go/ui";

type ContactRow = {
  contact_token: string;
  name?: string;
  is_automated?: boolean;
  profile_data?: Record<string, unknown>;
};

type ContactsResponse = {
  rows: ContactRow[];
};

type BotRow = {
  uuid: string;
  owner_user_uuid?: string;
  bot_contact_token: string;
  name: string;
  description?: string;
  is_public: boolean;
  integration_name?: string;
  default_shared_config?: {
    model?: string;
    backend?: string;
    [key: string]: unknown;
  };
};

type BotsResponse = {
  rows: BotRow[];
};

type PermissionsResponse = {
  rows: string[];
};

type SelfUser = {
  uuid?: string;
};

type IntegrationRow = {
  name: string;
  has_route_registrar: boolean;
  api_route_count: number;
  model_provider_count: number;
  function_count: number;
};

type IntegrationsResponse = {
  rows: IntegrationRow[];
};

type OverviewEntry = {
  contactToken: string;
  botUUID?: string;
  name: string;
  description: string;
  isAutomated: boolean;
  model?: string;
  backend?: string;
  integrationName?: string;
  sourceOwned: boolean;
  sourcePublic: boolean;
  sourceContact: boolean;
};

type EntityKindFilter = "all" | "bots" | "people";

type EntityViewMode = "card" | "compact" | "list";

const VIEW_MODE_OPTIONS = [
  { value: "card", label: "Card view", Icon: LayoutGrid },
  { value: "compact", label: "Compact view", Icon: Grid2x2 },
  { value: "list", label: "List view", Icon: List },
] as const;

const KIND_QUERY_PARAM = "kind";
const SEARCH_QUERY_PARAM = "q";
const OWNED_QUERY_PARAM = "owned";
const PUBLIC_QUERY_PARAM = "public";
const INTEGRATIONS_QUERY_PARAM = "integrations";

function parseBoolParam(value: string | null): boolean {
  return value === "1" || value === "true";
}

function parseListParam(value: string | null): string[] {
  if (!value) {
    return [];
  }
  return value
    .split(",")
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0);
}

function normalizeDescription(raw: unknown): string {
  if (typeof raw !== "string") {
    return "";
  }
  return raw.trim();
}

function toTitleCase(value: string): string {
  if (!value) {
    return "";
  }
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

function entryRank(entry: OverviewEntry): number {
  if (entry.sourceOwned) {
    return 0;
  }
  if (entry.sourceContact) {
    return 1;
  }
  if (entry.sourcePublic) {
    return 2;
  }
  return 3;
}

function ViewModeToggle({
  value,
  onChange,
}: {
  value: EntityViewMode;
  onChange: (value: EntityViewMode) => void;
}) {
  return (
    <div
      role="group"
      aria-label="View mode"
      className="surface-sunken flex shrink-0 gap-1 p-1"
    >
      {VIEW_MODE_OPTIONS.map(({ value: mode, label, Icon }) => {
        const isActive = value === mode;
        return (
          <button
            key={mode}
            type="button"
            aria-pressed={isActive}
            aria-label={label}
            title={label}
            onClick={() => onChange(mode)}
            className={cn(
              "flex items-center justify-center rounded-md p-1.5 transition-[color,box-shadow,background-color]",
              isActive
                ? "bg-background text-foreground shadow-sm ring-1 ring-black/[0.04] dark:ring-white/[0.06]"
                : "text-muted-foreground hover:bg-background/60 hover:text-foreground",
            )}
          >
            <Icon className="size-4" aria-hidden />
          </button>
        );
      })}
    </div>
  );
}

function FilterChip({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-full border border-border/70 bg-secondary px-2 py-[2px] text-[10px] text-foreground">
      <span className="max-w-[10rem] truncate">{label}</span>
      <button
        type="button"
        aria-label={`Remove ${label} filter`}
        className="text-muted-foreground transition-colors hover:text-foreground"
        onClick={onRemove}
      >
        <X className="size-3" />
      </button>
    </span>
  );
}

function EntityCard({
  entity,
  navigateTo,
  compact = false,
}: {
  entity: OverviewEntry;
  navigateTo: (to: string) => void;
  compact?: boolean;
}) {
  const initials = (entity.name || "?").slice(0, 2).toUpperCase();

  return (
    <Card
      className={cn(
        "group cursor-pointer border-border/70 bg-card/90 shadow-sm transition-all hover:border-border hover:bg-card hover:shadow-md",
        !compact && "hover:-translate-y-0.5",
      )}
      onClick={() => navigateTo(`/chat/new/${entity.contactToken}`)}
    >
      <CardHeader className={cn("pb-3", compact ? "space-y-2 p-3" : "space-y-3")}>
        <div className="flex items-start justify-between gap-3">
          <div className={cn("flex min-w-0 items-center", compact ? "gap-2" : "gap-3")}>
            <div
              className={cn(
                "flex shrink-0 items-center justify-center rounded-full border font-semibold",
                compact ? "size-8 text-xs" : "size-10 text-sm",
                entity.isAutomated
                  ? "border-primary/30 bg-primary/10 text-primary"
                  : "border-border bg-muted/60 text-foreground",
              )}
            >
              {initials}
            </div>
            <div className="min-w-0">
              <CardTitle className={cn("truncate", compact ? "text-sm" : "text-base")}>
                {entity.name}
              </CardTitle>
              {!compact ? (
                <CardDescription className="truncate">
                  {entity.isAutomated ? "Automated bot" : "Contact"}
                </CardDescription>
              ) : null}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            {entity.isAutomated && entity.sourceOwned && entity.botUUID ? (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-7 px-2 text-xs"
                onClick={(event) => {
                  event.stopPropagation();
                  navigateTo(`/chats/bots/${encodeURIComponent(entity.botUUID || "")}/edit`);
                }}
              >
                Edit
              </Button>
            ) : null}
            <Badge variant="outline" className="shrink-0">
              {entity.isAutomated ? "Bot" : "User"}
            </Badge>
          </div>
        </div>
        {!compact ? (
          <div className="flex flex-wrap gap-1.5">
            {entity.sourceOwned ? <Badge variant="secondary">Owned</Badge> : null}
            {entity.sourceContact ? <Badge variant="outline">In contacts</Badge> : null}
            {entity.sourcePublic ? <Badge variant="outline">Public</Badge> : null}
            {entity.integrationName ? (
              <Badge variant="outline">{toTitleCase(entity.integrationName)}</Badge>
            ) : null}
          </div>
        ) : null}
      </CardHeader>
      <CardContent className={cn("pt-0", compact ? "space-y-2" : "space-y-3")}>
        <Text
          type={TextTypes.Body7}
          color="muted"
          className={cn(compact ? "line-clamp-1 min-h-0" : "line-clamp-2 min-h-10")}
        >
          {entity.description || "Start a new conversation."}
        </Text>
        {!compact ? (
          <div className="flex flex-wrap items-center gap-3">
            {entity.model ? (
              <Text type={TextTypes.Body7} color="muted" className="rounded bg-muted px-2 py-0.5">
                Model: {entity.model}
              </Text>
            ) : null}
            {entity.backend ? (
              <Text type={TextTypes.Body7} color="muted" className="rounded bg-muted px-2 py-0.5">
                Backend: {entity.backend}
              </Text>
            ) : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function EntityListRow({
  entity,
  navigateTo,
}: {
  entity: OverviewEntry;
  navigateTo: (to: string) => void;
}) {
  const initials = (entity.name || "?").slice(0, 2).toUpperCase();

  return (
    <Card
      className="group cursor-pointer border-border/70 bg-card/90 shadow-sm transition-all hover:border-border hover:bg-card"
      onClick={() => navigateTo(`/chat/new/${entity.contactToken}`)}
    >
      <div className="flex items-center gap-3 px-3 py-2">
        <div
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-full border text-xs font-semibold",
            entity.isAutomated
              ? "border-primary/30 bg-primary/10 text-primary"
              : "border-border bg-muted/60 text-foreground",
          )}
        >
          {initials}
        </div>
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <Text type={TextTypes.Body6} tag="span" bold className="max-w-[40%] shrink-0 truncate">
            {entity.name}
          </Text>
          <Text type={TextTypes.Body7} color="muted" className="min-w-0 flex-1 truncate">
            {entity.description || "Start a new conversation."}
          </Text>
        </div>
        <div className="hidden shrink-0 items-center gap-2 sm:flex">
          {entity.integrationName ? (
            <Badge variant="outline" className="shrink-0">
              {toTitleCase(entity.integrationName)}
            </Badge>
          ) : null}
          {entity.model ? (
            <Text type={TextTypes.Body7} color="muted" className="truncate">
              {entity.model}
            </Text>
          ) : null}
          {entity.backend ? (
            <Text type={TextTypes.Body7} color="muted" className="truncate">
              {entity.backend}
            </Text>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {entity.isAutomated && entity.sourceOwned && entity.botUUID ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2 text-xs"
              onClick={(event) => {
                event.stopPropagation();
                navigateTo(`/chats/bots/${encodeURIComponent(entity.botUUID || "")}/edit`);
              }}
            >
              Edit
            </Button>
          ) : null}
          <Badge variant="outline" className="shrink-0">
            {entity.isAutomated ? "Bot" : "User"}
          </Badge>
        </div>
      </div>
    </Card>
  );
}

const VIEW_MODE_GRID_CLASSES: Record<EntityViewMode, string> = {
  card: "grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3",
  compact: "grid grid-cols-1 gap-2 sm:grid-cols-2",
  list: "flex flex-col gap-1.5",
};

function EntityEntries({
  entries,
  viewMode,
  navigateTo,
}: {
  entries: OverviewEntry[];
  viewMode: EntityViewMode;
  navigateTo: (to: string) => void;
}) {
  return (
    <div className={VIEW_MODE_GRID_CLASSES[viewMode]}>
      {entries.map((entry) =>
        viewMode === "list" ? (
          <EntityListRow key={entry.contactToken} entity={entry} navigateTo={navigateTo} />
        ) : (
          <EntityCard
            key={entry.contactToken}
            entity={entry}
            navigateTo={navigateTo}
            compact={viewMode === "compact"}
          />
        ),
      )}
    </div>
  );
}

export function EntitiesOverview({
  navigateTo,
  routeMode,
}: {
  navigateTo: (to: string) => void;
  routeMode: "all" | "bots";
}) {
  const { isSm } = useBreakpoint("sm");
  const leftPannelCollapsed = useSidePanelCollapse((state) => state.isCollapsed);
  const onToggleCollapse = useSidePanelCollapse((state) => state.toggle);
  const onSidebarButtonClick = isSm ? onToggleCollapse : () => navigateTo("/chat");

  const defaultKind: EntityKindFilter = routeMode === "bots" ? "bots" : "all";

  const [kindFilter, setKindFilter] = useState<EntityKindFilter>(() => {
    if (typeof window === "undefined") {
      return defaultKind;
    }
    const value = new URLSearchParams(window.location.search).get(KIND_QUERY_PARAM);
    return value === "all" || value === "bots" || value === "people" ? value : defaultKind;
  });
  const [query, setQuery] = useState(() => {
    if (typeof window === "undefined") {
      return "";
    }
    return new URLSearchParams(window.location.search).get(SEARCH_QUERY_PARAM) ?? "";
  });
  const [searchOpen, setSearchOpen] = useState(() => query.trim().length > 0);
  const [ownedOnly, setOwnedOnly] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return parseBoolParam(new URLSearchParams(window.location.search).get(OWNED_QUERY_PARAM));
  });
  const [publicOnly, setPublicOnly] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return parseBoolParam(new URLSearchParams(window.location.search).get(PUBLIC_QUERY_PARAM));
  });
  const [integrationFilters, setIntegrationFilters] = useState<string[]>(() => {
    if (typeof window === "undefined") {
      return [];
    }
    return parseListParam(new URLSearchParams(window.location.search).get(INTEGRATIONS_QUERY_PARAM));
  });
  const [scopeMenuOpen, setScopeMenuOpen] = useState(false);
  const [integrationsMenuOpen, setIntegrationsMenuOpen] = useState(false);
  const [showIntegrations, setShowIntegrations] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    const params = new URLSearchParams(window.location.search);
    return params.get("show_integrations") === "1";
  });
  const [viewMode, setViewMode] = useState<EntityViewMode>(() => {
    if (typeof window === "undefined") {
      return "list";
    }
    const value = new URLSearchParams(window.location.search).get("view");
    return value === "compact" || value === "card" || value === "list" ? value : "list";
  });

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const url = new URL(window.location.href);
    if (showIntegrations) {
      url.searchParams.set("show_integrations", "1");
    } else {
      url.searchParams.delete("show_integrations");
    }
    if (viewMode === "list") {
      url.searchParams.delete("view");
    } else {
      url.searchParams.set("view", viewMode);
    }
    if (kindFilter === defaultKind) {
      url.searchParams.delete(KIND_QUERY_PARAM);
    } else {
      url.searchParams.set(KIND_QUERY_PARAM, kindFilter);
    }
    if (query.trim()) {
      url.searchParams.set(SEARCH_QUERY_PARAM, query.trim());
    } else {
      url.searchParams.delete(SEARCH_QUERY_PARAM);
    }
    if (ownedOnly) {
      url.searchParams.set(OWNED_QUERY_PARAM, "1");
    } else {
      url.searchParams.delete(OWNED_QUERY_PARAM);
    }
    if (publicOnly) {
      url.searchParams.set(PUBLIC_QUERY_PARAM, "1");
    } else {
      url.searchParams.delete(PUBLIC_QUERY_PARAM);
    }
    if (integrationFilters.length > 0) {
      url.searchParams.set(INTEGRATIONS_QUERY_PARAM, integrationFilters.join(","));
    } else {
      url.searchParams.delete(INTEGRATIONS_QUERY_PARAM);
    }
    const next = `${url.pathname}${url.search}${url.hash}`;
    window.history.replaceState({}, "", next);
  }, [showIntegrations, viewMode, kindFilter, defaultKind, query, ownedOnly, publicOnly, integrationFilters]);

  const {
    data: contacts,
    isLoading: contactsLoading,
    error: contactsError,
  } = useSWR<ContactsResponse>("/api/v1/contacts/list?limit=200&page=1", fetcher);

  const {
    data: bots,
    isLoading: botsLoading,
    error: botsError,
  } = useSWR<BotsResponse>("/api/v1/bots/list?include_public=true&limit=200&page=1", fetcher);

  const { data: permissions } = useSWR<PermissionsResponse>("/api/v1/user/permissions", fetcher);
  const { data: selfUser } = useSWR<SelfUser>("/api/v1/user/self", fetcher);
  const { data: actionTasksCount } = useSWR<{ count: number }>(
    "/api/v1/chats/action-tasks?count_only=1",
    fetcher,
    {
      refreshInterval: () => (typeof document !== "undefined" && document.hidden ? 0 : 5000),
      revalidateOnFocus: true,
    },
  );
  const actionCount = typeof actionTasksCount?.count === "number" ? actionTasksCount.count : 0;
  const {
    data: integrations,
    isLoading: integrationsLoading,
    error: integrationsError,
  } = useSWR<IntegrationsResponse>(showIntegrations ? "/api/v1/integrations/list" : null, fetcher);
  const canCreateBots = Boolean(permissions?.rows?.includes("create_bots"));

  const entities = useMemo<OverviewEntry[]>(() => {
    const byContactToken = new Map<string, OverviewEntry>();

    for (const contact of contacts?.rows ?? []) {
      if (!contact.contact_token) {
        continue;
      }

      const profileData = contact.profile_data ?? {};
      const existing = byContactToken.get(contact.contact_token);
      const fallbackName = toTitleCase(contact.name || "Unnamed");
      const fallbackDescription =
        normalizeDescription(profileData.description) ||
        (contact.is_automated ? "Automated assistant from your contacts." : "Person in your contacts.");

      const entry: OverviewEntry = existing ?? {
        contactToken: contact.contact_token,
        name: fallbackName,
        description: fallbackDescription,
        isAutomated: Boolean(contact.is_automated),
        model: typeof profileData.model === "string" ? profileData.model : undefined,
        backend: typeof profileData.backend === "string" ? profileData.backend : undefined,
        sourceOwned: false,
        sourcePublic: false,
        sourceContact: false,
      };

      entry.sourceContact = true;
      entry.isAutomated = entry.isAutomated || Boolean(contact.is_automated);
      if (!entry.name || entry.name === "Unnamed") {
        entry.name = fallbackName;
      }
      if (!entry.description) {
        entry.description = fallbackDescription;
      }

      byContactToken.set(contact.contact_token, entry);
    }

    for (const bot of bots?.rows ?? []) {
      if (!bot.bot_contact_token) {
        continue;
      }

      const existing = byContactToken.get(bot.bot_contact_token);
      const entry: OverviewEntry = existing ?? {
        contactToken: bot.bot_contact_token,
        botUUID: bot.uuid,
        name: toTitleCase(bot.name || "Unnamed Bot"),
        description: normalizeDescription(bot.description) || "AI bot ready to chat.",
        isAutomated: true,
        model: undefined,
        backend: undefined,
        integrationName: undefined,
        sourceOwned: false,
        sourcePublic: false,
        sourceContact: false,
      };

      entry.name = toTitleCase(bot.name || entry.name);
      entry.botUUID = bot.uuid || entry.botUUID;
      entry.description = normalizeDescription(bot.description) || entry.description;
      entry.isAutomated = true;
      entry.integrationName = bot.integration_name || entry.integrationName;
      entry.model =
        typeof bot.default_shared_config?.model === "string"
          ? bot.default_shared_config.model
          : entry.model;
      entry.backend =
        typeof bot.default_shared_config?.backend === "string"
          ? bot.default_shared_config.backend
          : entry.backend;
      entry.sourceOwned = entry.sourceOwned || Boolean(bot.owner_user_uuid && selfUser?.uuid && bot.owner_user_uuid === selfUser.uuid);
      entry.sourcePublic = entry.sourcePublic || bot.is_public;

      byContactToken.set(bot.bot_contact_token, entry);
    }

    return Array.from(byContactToken.values()).sort((a, b) => {
      const rankDiff = entryRank(a) - entryRank(b);
      if (rankDiff !== 0) {
        return rankDiff;
      }
      if (a.isAutomated !== b.isAutomated) {
        return a.isAutomated ? -1 : 1;
      }
      return a.name.localeCompare(b.name);
    });
  }, [bots?.rows, contacts?.rows, selfUser?.uuid]);

  const integrationOptions = useMemo(() => {
    const names = new Set<string>();
    for (const entry of entities) {
      if (entry.integrationName) {
        names.add(entry.integrationName);
      }
    }
    return Array.from(names).sort((a, b) => a.localeCompare(b));
  }, [entities]);

  const filteredEntities = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    const integrationSet = new Set(integrationFilters);

    return entities.filter((entry) => {
      if (kindFilter === "bots" && !entry.isAutomated) {
        return false;
      }
      if (kindFilter === "people" && entry.isAutomated) {
        return false;
      }
      if (ownedOnly && !entry.sourceOwned) {
        return false;
      }
      if (publicOnly && !entry.sourcePublic) {
        return false;
      }
      if (integrationSet.size > 0 && !(entry.integrationName && integrationSet.has(entry.integrationName))) {
        return false;
      }

      if (!normalizedQuery) {
        return true;
      }

      const haystack = [
        entry.name,
        entry.description,
        entry.model,
        entry.backend,
        entry.integrationName,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();

      return haystack.includes(normalizedQuery);
    });
  }, [entities, kindFilter, ownedOnly, publicOnly, integrationFilters, query]);

  const grouped = useMemo(() => {
    const botsList = filteredEntities.filter((entry) => entry.isAutomated);
    const peopleList = filteredEntities.filter((entry) => !entry.isAutomated);
    return { botsList, peopleList };
  }, [filteredEntities]);

  const isLoading = contactsLoading || botsLoading;
  const hasError = Boolean(contactsError || botsError);

  const hasSearchFilter = query.trim().length > 0;
  const hasAttributeFilter = ownedOnly || publicOnly;
  const hasIntegrationFilter = integrationFilters.length > 0;
  const hasExtraFilters = hasSearchFilter || hasAttributeFilter || hasIntegrationFilter;
  const isDefaultKind = kindFilter === defaultKind;
  const hasAnyFilter = hasExtraFilters || !isDefaultKind;

  const handleKindChange = (value: EntityKindFilter) => {
    setKindFilter(value);
  };

  const handleIntegrationToggle = (name: string, checked: boolean) => {
    setIntegrationFilters((current) => {
      if (checked) {
        return current.includes(name) ? current : [...current, name];
      }
      return current.filter((entry) => entry !== name);
    });
  };

  const handleResetFilters = () => {
    setKindFilter(defaultKind);
    setQuery("");
    setOwnedOnly(false);
    setPublicOnly(false);
    setIntegrationFilters([]);
  };

  const scopeMenu = (
    <DropdownMenu open={scopeMenuOpen} onOpenChange={setScopeMenuOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          variant={!isDefaultKind || hasAttributeFilter ? "default" : "ghost"}
          size="icon"
          className="relative size-7"
          aria-label="Filter entities"
        >
          <SlidersHorizontal className="size-4" />
          {!isDefaultKind || hasAttributeFilter ? (
            <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
          ) : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48" onCloseAutoFocus={(event) => event.preventDefault()}>
        <DropdownMenuLabel>Filter entities</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={kindFilter}
          onValueChange={(value) => handleKindChange(value as EntityKindFilter)}
        >
          <DropdownMenuRadioItem value="all">All</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="bots">Bots</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="people">People</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={ownedOnly}
          onCheckedChange={(checked) => setOwnedOnly(checked === true)}
          onSelect={(event) => event.preventDefault()}
        >
          Owned
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          checked={publicOnly}
          onCheckedChange={(checked) => setPublicOnly(checked === true)}
          onSelect={(event) => event.preventDefault()}
        >
          Public
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );

  const integrationsMenu = (
    <DropdownMenu open={integrationsMenuOpen} onOpenChange={setIntegrationsMenuOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          variant={hasIntegrationFilter ? "default" : "ghost"}
          size="icon"
          className="relative size-7"
          aria-label="Filter by integration"
        >
          <Boxes className="size-4" />
          {hasIntegrationFilter ? (
            <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
          ) : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56" onCloseAutoFocus={(event) => event.preventDefault()}>
        <DropdownMenuLabel>Filter by integration</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {integrationOptions.length === 0 ? (
          <div className="px-3 py-2">
            <Text type={TextTypes.Body7} color="muted">
              No integration bots
            </Text>
          </div>
        ) : (
          integrationOptions.map((name) => (
            <DropdownMenuCheckboxItem
              key={name}
              checked={integrationFilters.includes(name)}
              onCheckedChange={(checked) => handleIntegrationToggle(name, checked === true)}
              onSelect={(event) => event.preventDefault()}
            >
              {toTitleCase(name)}
            </DropdownMenuCheckboxItem>
          ))
        )}
        {hasIntegrationFilter ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={(event) => {
                event.preventDefault();
                setIntegrationFilters([]);
              }}
            >
              Clear integration filter
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );

  const integrationsToggleCard = (
    <Card
      className="cursor-pointer border-border/70 bg-card/80 transition-all hover:border-border hover:shadow-sm"
      onClick={() => setShowIntegrations((value) => !value)}
    >
      <CardHeader className="py-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="text-lg">Integrations</CardTitle>
            <CardDescription>
              View integration modules compiled into this backend build.
            </CardDescription>
          </div>
          <Badge variant={showIntegrations ? "secondary" : "outline"}>
            {showIntegrations ? "Hide" : "Show"}
          </Badge>
        </div>
      </CardHeader>
    </Card>
  );

  return (
    <div className="mx-auto h-full w-full max-w-6xl space-y-4 overflow-y-auto px-4 py-6 md:px-6">
      {!isSm && leftPannelCollapsed ? (
        <div className="flex items-center justify-start">
          <CollapseIndicator
            leftPannelCollapsed={leftPannelCollapsed}
            onToggleCollapse={onSidebarButtonClick}
          />
        </div>
      ) : null}

      <Card className="overflow-hidden border-border/70 bg-gradient-to-br from-background via-background to-muted/40">
        <CardHeader className="gap-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0 flex-1">
              <CardTitle className="break-words text-xl leading-tight sm:text-2xl">Bots & Users Overview</CardTitle>
              <CardDescription className="max-w-3xl text-sm md:text-base">
                Start a chat with owned bots, public bots, or people from your contacts.
              </CardDescription>
            </div>
            <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
              <Button
                type="button"
                variant="outline"
                className="w-full sm:w-auto"
                onClick={() => navigateTo("/chats/actions")}
              >
                Actions required
                {actionCount > 0 ? (
                  <Badge variant="default" className="ml-2 bg-amber-500 text-white shadow-none dark:text-amber-950">
                    {actionCount}
                  </Badge>
                ) : null}
              </Button>
              {canCreateBots ? (
                <Button type="button" className="w-full sm:w-auto" onClick={() => navigateTo("/chats/bots/create")}>
                  Create bot
                </Button>
              ) : null}
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Badge variant="secondary">Owned</Badge>
            <Badge variant="outline">Public</Badge>
            <Badge variant="outline">Contacts</Badge>
          </div>
        </CardHeader>
      </Card>

      {isSm ? integrationsToggleCard : null}

      <div className="space-y-2 border-b border-border/60 pb-3">
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <Text type={TextTypes.Body7} color="muted" tag="span" bold className="uppercase tracking-wide text-[10px]">
              Filters
            </Text>
            <div className="flex min-w-0 flex-wrap items-center gap-1">
              {hasSearchFilter ? (
                <FilterChip label={`Search: ${query.trim()}`} onRemove={() => setQuery("")} />
              ) : null}
              {ownedOnly ? <FilterChip label="Owned" onRemove={() => setOwnedOnly(false)} /> : null}
              {publicOnly ? <FilterChip label="Public" onRemove={() => setPublicOnly(false)} /> : null}
              {integrationFilters.map((name) => (
                <FilterChip
                  key={name}
                  label={toTitleCase(name)}
                  onRemove={() => handleIntegrationToggle(name, false)}
                />
              ))}
              {hasAnyFilter ? (
                <button
                  type="button"
                  className="text-[10px] text-muted-foreground underline-offset-2 hover:underline"
                  onClick={handleResetFilters}
                >
                  Clear all
                </button>
              ) : null}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <Button
              variant={searchOpen ? "default" : "ghost"}
              size="icon"
              className="relative size-7"
              aria-label="Search bots and people"
              onClick={() => setSearchOpen((value) => !value)}
            >
              <Search className="size-4" />
              {hasSearchFilter ? (
                <span className="bg-primary absolute top-1 right-1 size-1.5 rounded-full" aria-hidden="true" />
              ) : null}
            </Button>
            <ViewModeToggle value={viewMode} onChange={setViewMode} />
            {scopeMenu}
            {integrationsMenu}
          </div>
        </div>
        {searchOpen ? (
          <Input
            autoFocus
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search bots, people, models, integrations"
            className="h-8 text-sm md:max-w-md"
          />
        ) : null}
      </div>

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <LoadingSpinner />
        </div>
      ) : (
        <div className="space-y-6 pb-6">
          {hasError ? (
            <Card className="border-destructive/30 bg-destructive/5">
              <CardHeader>
                <CardTitle className="text-base">Some data could not be loaded</CardTitle>
                <CardDescription>
                  The overview is showing all data that was available. Reload to retry missing sources.
                </CardDescription>
              </CardHeader>
            </Card>
          ) : null}

          {grouped.botsList.length > 0 ? (
            <section className="space-y-3">
              <div className="flex items-center justify-between gap-2">
                <Text type={TextTypes.Heading6} tag="h2" bold>
                  Bots
                </Text>
              </div>
              <EntityEntries
                entries={grouped.botsList}
                viewMode={viewMode}
                navigateTo={navigateTo}
              />
            </section>
          ) : null}

          {grouped.peopleList.length > 0 ? (
            <section className="space-y-3">
              <div className="flex items-center justify-between gap-2">
                <Text type={TextTypes.Heading6} tag="h2" bold>
                  People
                </Text>
              </div>
              <EntityEntries
                entries={grouped.peopleList}
                viewMode={viewMode}
                navigateTo={navigateTo}
              />
            </section>
          ) : null}

          {!isSm ? integrationsToggleCard : null}

          {showIntegrations ? (
            <section className="space-y-3">
              <Text type={TextTypes.Heading6} tag="h2" bold>
                Compiled Integrations
              </Text>
              {integrationsLoading ? (
                <div className="flex h-24 items-center justify-center">
                  <LoadingSpinner />
                </div>
              ) : integrationsError ? (
                <Card className="border-destructive/30 bg-destructive/5">
                  <CardHeader>
                    <CardTitle className="text-base">Failed to load integrations</CardTitle>
                    <CardDescription>
                      Could not fetch compiled integrations from the backend.
                    </CardDescription>
                  </CardHeader>
                </Card>
              ) : integrations?.rows?.length ? (
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {integrations.rows.map((integration) => (
                    <Card
                      key={integration.name}
                      className="cursor-pointer border-border/70 bg-card/90 transition-all hover:border-border hover:shadow-sm"
                      onClick={() => navigateTo(`/integrations/${encodeURIComponent(integration.name)}/`)}
                    >
                      <CardHeader className="pb-2">
                        <CardTitle className="text-base">{integration.name}</CardTitle>
                        <CardDescription>Compiled integration module</CardDescription>
                      </CardHeader>
                      <CardContent className="flex flex-wrap gap-2 pt-0">
                        <Badge variant={integration.has_route_registrar ? "secondary" : "outline"}>
                          {integration.has_route_registrar ? "Routes" : "No routes"}
                        </Badge>
                        <Badge variant="outline">API: {integration.api_route_count}</Badge>
                        <Badge variant="outline">Models: {integration.model_provider_count}</Badge>
                        <Badge variant="outline">Functions: {integration.function_count}</Badge>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              ) : (
                <Card className="border-dashed border-border/70 bg-muted/30">
                  <CardHeader>
                    <CardTitle className="text-base">No integrations available</CardTitle>
                    <CardDescription>
                      This backend build has no compiled integration modules.
                    </CardDescription>
                  </CardHeader>
                </Card>
              )}
            </section>
          ) : null}

          {filteredEntities.length === 0 ? (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-lg">No matches found</CardTitle>
                <CardDescription>Try clearing the search or switching filters.</CardDescription>
              </CardHeader>
              <CardContent>
                <Button type="button" variant="outline" onClick={handleResetFilters}>
                  Reset filters
                </Button>
              </CardContent>
            </Card>
          ) : null}
        </div>
      )}
    </div>
  );
}
