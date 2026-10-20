import React, { useCallback, useMemo, useState } from "react";
import useSWR from "swr";
import { cn, fetcher } from "../../lib/utils";
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import {
  Check,
  Copy,
  ExternalLink,
  Info,
  LayoutGrid,
  List,
  AlignJustify,
  Search,
  Server,
  Settings2,
  Wrench,
  X,
} from "lucide-react";

export type ChatDetailsTabId = "overview" | "init" | "tools" | "mcp";

/**
 * Tabs of the shared chat details surface (ci#749). The tool init payload is a
 * sub-section of the details now instead of a standalone dialog, and tools get
 * their own browse view with list/tile layouts and filters.
 */
export const CHAT_DETAILS_TABS: ReadonlyArray<{
  id: ChatDetailsTabId;
  label: string;
  description: string;
}> = [
  { id: "overview", label: "Overview", description: "Model and chat configuration" },
  { id: "init", label: "Init", description: "Per-tool init payload" },
  { id: "tools", label: "Tools", description: "Browse and inspect chat tools" },
  { id: "mcp", label: "MCP servers", description: "Connected MCP integrations" },
];

export type ChatToolRow = {
  name: string;
  function_name?: string;
  description?: string;
  type?: string;
  source_path?: string;
  source_line?: number;
  source_url?: string;
  requires_init?: boolean;
  requires_confirmation?: boolean;
  stop_on_first_confirmable_tool_call?: boolean;
  confirmation_block_message?: string;
  required?: string[];
  call_schema?: Record<string, unknown>;
  init_schema?: Record<string, unknown>;
};

type ToolsResponse = {
  rows?: ChatToolRow[];
  total_rows?: number;
  filters?: { types?: string[] };
};

export type ChatMCPServerRow = {
  name: string;
  enabled?: boolean;
  has_auth_data?: boolean;
};

type MCPServersResponse = { rows?: ChatMCPServerRow[] };

type ToolScope = "chat" | "all";
type ToolViewMode = "list" | "compact" | "grid";

/** Full tool slug, eg `integration:signal_send_message`. */
export function toolSlug(toolName: string): string {
  return toolName;
}

/** Last path segment of a namespaced tool slug. */
export function normalizeToolName(toolName: string): string {
  if (!toolName.includes(":")) return toolName;
  const parts = toolName.split(":");
  return parts[parts.length - 1] || toolName;
}

/** Human readable tool title, falling back to the prettified slug tail. */
export function toolTitle(tool: ChatToolRow | string): string {
  if (typeof tool !== "string") {
    const fn = (tool.function_name || "").trim();
    if (fn) {
      return fn
        .split("_")
        .filter(Boolean)
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(" ");
    }
    return toolTitle(tool.name);
  }
  return tool
    .split(":")
    .pop()!
    .split(/[_-]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function useCopyToClipboard() {
  const [copied, setCopied] = useState(false);
  const copy = useCallback((value: string) => {
    const clipboard = typeof navigator !== "undefined" ? navigator.clipboard : undefined;
    if (clipboard?.writeText) {
      void clipboard.writeText(value).catch(() => undefined);
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1600);
  }, []);
  return { copied, copy };
}

function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const { copied, copy } = useCopyToClipboard();
  return (
    <Button size="sm" variant="outline" onClick={() => copy(value)}>
      {copied ? <Check className="mr-1 h-3.5 w-3.5" /> : <Copy className="mr-1 h-3.5 w-3.5" />}
      {copied ? "Copied" : label}
    </Button>
  );
}

function CodeBlock({ value, className }: { value: string; className?: string }) {
  return (
    <pre
      className={cn(
        "max-h-72 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5",
        className,
      )}
    >
      {value}
    </pre>
  );
}

function PanelCard({
  title,
  hint,
  action,
  className,
  children,
}: {
  title: string;
  hint?: string;
  action?: React.ReactNode;
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <div className={cn("rounded-xl border border-border/70 bg-card/90 p-3", className)}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <Text type={TextTypes.Body6} bold>
            {title}
          </Text>
          {hint ? (
            <Text type={TextTypes.Body7} color="muted" className="mt-0.5">
              {hint}
            </Text>
          ) : null}
        </div>
        {action}
      </div>
      {children ? <div className="mt-2">{children}</div> : null}
    </div>
  );
}

function KeyValueList({ entries }: { entries: Array<[string, React.ReactNode]> }) {
  return (
    <dl className="space-y-1 text-sm">
      {entries.map(([key, value]) => (
        <div key={key} className="flex items-start justify-between gap-3">
          <dt className="text-muted-foreground">{key}</dt>
          <dd className="min-w-0 break-all text-right text-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function SchemaBlock({ title, schema }: { title: string; schema?: Record<string, unknown> }) {
  return (
    <PanelCard title={title}>
      <CodeBlock
        className="max-h-64"
        value={JSON.stringify(schema ?? { type: "object", properties: {} }, null, 2)}
      />
    </PanelCard>
  );
}

function buildModelPayload(tool: ChatToolRow) {
  return {
    type: "function",
    function: {
      name: tool.name,
      description: tool.description || "",
      parameters: {
        type: "object",
        properties: tool.call_schema?.properties ?? {},
        required: tool.required ?? [],
        description: "The parameters for the tool",
      },
    },
  };
}

/**
 * Revised tool details modal (ci#749): a review-first layout with the slug, the
 * source location and the schemas surfaced in one scrollable surface.
 */
export function ToolDetailsDialog({
  tool,
  fallbackName,
  open,
  onOpenChange,
}: {
  tool?: ChatToolRow | null;
  fallbackName?: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [showModelPayload, setShowModelPayload] = useState(false);
  const name = tool?.name || fallbackName || "";
  const payload = useMemo(() => (tool ? buildModelPayload(tool) : null), [tool]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] w-[calc(100vw-1.5rem)] max-w-4xl overflow-y-auto overscroll-contain sm:w-full">
        <DialogHeader>
          <DialogTitle className="break-words">
            {tool ? toolTitle(tool) : toolTitle(name) || "Tool"}
          </DialogTitle>
          <DialogDescription className="break-all">
            {tool?.description || "No tool metadata found in the catalog API for this tool."}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <PanelCard
            title="Tool slug"
            hint="Namespaced identifier used in the model tool list."
            action={name ? <CopyButton value={name} label="Copy slug" /> : undefined}
          >
            <code className="block break-all rounded-lg border border-border/60 bg-muted/30 p-2 text-xs">
              {name || "unknown"}
            </code>
          </PanelCard>

          <div className="flex flex-wrap gap-2">
            <Badge variant="outline">Type: {tool?.type || "unknown"}</Badge>
            <Badge variant={tool?.requires_init ? "secondary" : "outline"}>
              {tool?.requires_init ? "Init required" : "No init required"}
            </Badge>
            <Badge variant={tool?.requires_confirmation ? "secondary" : "outline"}>
              {tool?.requires_confirmation ? "Requires confirmation" : "No confirmation"}
            </Badge>
            {tool?.requires_confirmation ? (
              <Badge variant={tool.stop_on_first_confirmable_tool_call ? "secondary" : "outline"}>
                {tool.stop_on_first_confirmable_tool_call
                  ? "Stop on first confirmable call"
                  : "Continue after confirm request"}
              </Badge>
            ) : null}
            {(tool?.required ?? []).length > 0 ? (
              <Badge variant="outline">Required input: {(tool?.required ?? []).join(", ")}</Badge>
            ) : (
              <Badge variant="outline">No required input params</Badge>
            )}
          </div>

          <PanelCard
            title="Source location"
            action={
              <Button
                size="sm"
                variant="outline"
                disabled={!tool?.source_url}
                onClick={() => {
                  if (!tool?.source_url) return;
                  window.open(tool.source_url, "_blank", "noopener,noreferrer");
                }}
              >
                <ExternalLink className="mr-1 h-3.5 w-3.5" />
                Open source
              </Button>
            }
          >
            <code className="block break-all text-xs text-muted-foreground">
              {tool?.source_path ? `${tool.source_path}:${tool.source_line || 1}` : "Not available"}
            </code>
          </PanelCard>

          <PanelCard
            title="Model API payload"
            hint="The exact tool object included in the `tools` array for chat completions."
            action={
              <Button size="sm" variant="outline" onClick={() => setShowModelPayload((prev) => !prev)}>
                {showModelPayload ? "Hide payload" : "View payload"}
              </Button>
            }
          >
            {showModelPayload ? (
              payload ? (
                <div className="space-y-2">
                  <CodeBlock className="max-h-80" value={JSON.stringify(payload, null, 2)} />
                  <CopyButton value={JSON.stringify(payload, null, 2)} label="Copy payload" />
                </div>
              ) : (
                <Text type={TextTypes.Body7} color="muted">
                  No catalog entry, so the payload cannot be reconstructed.
                </Text>
              )
            ) : (
              <Text type={TextTypes.Body7} color="muted">
                Hidden by default to keep the details compact.
              </Text>
            )}
          </PanelCard>

          {tool?.requires_confirmation && tool?.confirmation_block_message ? (
            <PanelCard
              title="Confirmation block message"
              hint="Returned to the model while a confirmable tool call awaits approval."
            >
              <CodeBlock className="max-h-48" value={tool.confirmation_block_message} />
            </PanelCard>
          ) : null}

          <div className={cn("grid gap-3", tool?.requires_init ? "md:grid-cols-2" : "")}>
            <SchemaBlock title="Input parameters (call schema)" schema={tool?.call_schema} />
            {tool?.requires_init ? (
              <SchemaBlock title="Init parameters (init schema)" schema={tool?.init_schema} />
            ) : null}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function ToolBadges({ tool }: { tool: ChatToolRow }) {
  return (
    <>
      <Badge variant="outline">{tool.type || "tool"}</Badge>
      {tool.requires_init ? <Badge variant="secondary">Init</Badge> : null}
      {tool.requires_confirmation ? <Badge variant="secondary">Confirm</Badge> : null}
    </>
  );
}

export type ToolFlagFilter = "" | "init" | "confirm" | "no_init";

const TOOL_FLAG_FILTERS: ReadonlyArray<{ value: ToolFlagFilter; label: string }> = [
  { value: "", label: "All" },
  { value: "init", label: "Needs init" },
  { value: "confirm", label: "Needs confirm" },
  { value: "no_init", label: "No init" },
];

const TOOL_VIEW_GRID_CLASSES: Record<ToolViewMode, string> = {
  list: "flex flex-col gap-1.5",
  compact: "grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3",
  grid: "grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3",
};

/**
 * A single tool in the chat tools browser. `row` is the dense list layout,
 * `card` the tile layout used by the grid view (ci#749).
 */
function ToolCard({
  tool,
  onOpen,
  density = "card",
}: {
  tool: ChatToolRow;
  onOpen: () => void;
  density?: "row" | "card";
}) {
  const description = tool.description || "No description available.";
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`View details for ${toolTitle(tool)}`}
      className={cn(
        "flex w-full min-w-0 flex-col items-start gap-1 rounded-xl border border-border/70 bg-card/90 p-3 text-left transition-colors hover:border-border hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        density === "row" && "gap-0.5 p-2",
      )}
    >
      <span className="flex w-full min-w-0 items-center justify-between gap-2">
        <span className="truncate text-sm font-medium text-foreground">{toolTitle(tool)}</span>
        <ExternalLink className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
      </span>
      <code className="w-full truncate text-[11px] text-muted-foreground">{toolSlug(tool.name)}</code>
      <span
        className={cn(
          "text-xs text-muted-foreground",
          density === "row" ? "line-clamp-1" : "line-clamp-3",
        )}
      >
        {description}
      </span>
      <span className="mt-1 flex flex-wrap gap-1">
        <ToolBadges tool={tool} />
      </span>
    </button>
  );
}

function ToolsToolbar({
  query,
  onQueryChange,
  typeFilter,
  onTypeFilterChange,
  typeOptions,
  flagFilter,
  onFlagFilterChange,
  viewMode,
  onViewModeChange,
  scope,
  onScopeChange,
  showScopeToggle,
  onReset,
  hasActiveFilters,
}: {
  query: string;
  onQueryChange: (value: string) => void;
  typeFilter: string;
  onTypeFilterChange: (value: string) => void;
  typeOptions: string[];
  flagFilter: ToolFlagFilter;
  onFlagFilterChange: (value: ToolFlagFilter) => void;
  viewMode: ToolViewMode;
  onViewModeChange: (value: ToolViewMode) => void;
  scope: ToolScope;
  onScopeChange: (value: ToolScope) => void;
  showScopeToggle: boolean;
  onReset: () => void;
  hasActiveFilters: boolean;
}) {
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[160px] flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder="Search tools..."
            aria-label="Search tools"
            className="h-9 pl-7"
          />
        </div>
        {showScopeToggle ? (
          <div role="group" aria-label="Tool scope" className="surface-sunken flex shrink-0 gap-1 p-1">
            {(["chat", "all"] as ToolScope[]).map((option) => (
              <button
                key={option}
                type="button"
                aria-pressed={scope === option}
                onClick={() => onScopeChange(option)}
                className={cn(
                  "rounded-md px-2 py-1 text-xs transition-colors",
                  scope === option
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {option === "chat" ? "This chat" : "All tools"}
              </button>
            ))}
          </div>
        ) : null}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm" className="justify-between">
              <span className="truncate">Type {typeFilter ? `: ${typeFilter}` : "(all)"}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>Filter by tool type</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => onTypeFilterChange("")}>All types</DropdownMenuItem>
            {typeOptions.map((type) => (
              <DropdownMenuItem key={type} onClick={() => onTypeFilterChange(type)}>
                {type}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <div role="group" aria-label="Tool layout" className="surface-sunken flex shrink-0 gap-1 p-1">
          {(
            [
              { mode: "list" as ToolViewMode, label: "List layout", Icon: List },
              { mode: "compact" as ToolViewMode, label: "Compact layout", Icon: AlignJustify },
              { mode: "grid" as ToolViewMode, label: "Tile layout", Icon: LayoutGrid },
            ] as const
          ).map(({ mode, label, Icon }) => (
            <button
              key={mode}
              type="button"
              aria-label={label}
              title={label}
              aria-pressed={viewMode === mode}
              onClick={() => onViewModeChange(mode)}
              className={cn(
                "rounded-md p-1.5 transition-colors",
                viewMode === mode
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-4" aria-hidden />
            </button>
          ))}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        {TOOL_FLAG_FILTERS.map((option) => (
          <button
            key={option.value || "all"}
            type="button"
            aria-pressed={flagFilter === option.value}
            onClick={() => onFlagFilterChange(option.value)}
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-[11px] font-medium transition-colors",
              flagFilter === option.value
                ? "border-primary/40 bg-primary/10 text-foreground"
                : "border-border/60 text-muted-foreground hover:border-border hover:text-foreground",
            )}
          >
            {option.label}
          </button>
        ))}
        {hasActiveFilters ? (
          <Button variant="ghost" size="sm" className="h-6 px-2 text-[11px]" onClick={onReset}>
            <X className="mr-1 h-3 w-3" aria-hidden /> Reset filters
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function ToolsTab({ chat, isSharedView }: { chat: any; isSharedView: boolean }) {
  const config = chat?.config ?? {};
  const configuredTools: string[] = Array.isArray(config?.tools) ? config.tools : [];
  const { data, isLoading } = useSWR<ToolsResponse>("/api/v1/tools?page=1&page_size=400", fetcher);
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [flagFilter, setFlagFilter] = useState<ToolFlagFilter>("");
  const [viewMode, setViewMode] = useState<ToolViewMode>("list");
  const [scope, setScope] = useState<ToolScope>(configuredTools.length > 0 ? "chat" : "all");
  const [detailTool, setDetailTool] = useState<ChatToolRow | null>(null);

  const toolsBySlug = useMemo(() => {
    // The chat config stores tool slugs that may be namespaced (eg
    // `integration:signal_send_message`) while the catalog can expose either
    // form, so index by the full slug *and* the normalized short name (ci#749).
    const map = new Map<string, ChatToolRow>();
    (data?.rows ?? []).forEach((tool) => {
      map.set(tool.name, tool);
      map.set(normalizeToolName(tool.name), tool);
      if (tool.function_name) map.set(tool.function_name, tool);
    });
    return map;
  }, [data?.rows]);

  const rows = useMemo(() => {
    const source: ChatToolRow[] =
      scope === "chat"
        ? configuredTools.map(
            (slug) => toolsBySlug.get(slug) ?? toolsBySlug.get(normalizeToolName(slug)) ?? { name: slug },
          )
        : (data?.rows ?? []);
    return source;
  }, [configuredTools, data?.rows, scope, toolsBySlug]);

  const typeOptions = useMemo(() => {
    const fromApi = data?.filters?.types ?? [];
    if (fromApi.length > 0) return fromApi;
    const types = new Set<string>();
    rows.forEach((tool) => {
      if (tool.type) types.add(tool.type);
    });
    return Array.from(types).sort((a, b) => a.localeCompare(b));
  }, [data?.filters?.types, rows]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return rows.filter((tool) => {
      if (typeFilter && (tool.type || "tool") !== typeFilter) return false;
      if (flagFilter === "init" && !tool.requires_init) return false;
      if (flagFilter === "confirm" && !tool.requires_confirmation) return false;
      if (flagFilter === "no_init" && tool.requires_init) return false;
      if (!needle) return true;
      return [tool.name, tool.function_name, toolTitle(tool), tool.description]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle));
    });
  }, [flagFilter, query, rows, typeFilter]);

  const hasActiveFilters = Boolean(query.trim() || typeFilter || flagFilter);
  const resetFilters = () => {
    setQuery("");
    setTypeFilter("");
    setFlagFilter("");
  };

  return (
    <div className="space-y-3">
      <ToolsToolbar
        query={query}
        onQueryChange={setQuery}
        typeFilter={typeFilter}
        onTypeFilterChange={setTypeFilter}
        typeOptions={typeOptions}
        flagFilter={flagFilter}
        onFlagFilterChange={setFlagFilter}
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        scope={scope}
        onScopeChange={setScope}
        showScopeToggle={configuredTools.length > 0}
        onReset={resetFilters}
        hasActiveFilters={hasActiveFilters}
      />

      <div className="flex items-center justify-between gap-2">
        <Text type={TextTypes.Body7} color="muted">
          {filtered.length} of {rows.length} {scope === "chat" ? "chat tools" : "platform tools"}
          {isSharedView ? " (read-only view)" : ""}
        </Text>
      </div>

      {isLoading && rows.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted">
          Loading tools...
        </Text>
      ) : filtered.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted">
          No tools match the current filters.
        </Text>
      ) : (
        <div className={TOOL_VIEW_GRID_CLASSES[viewMode]}>
          {filtered.map((tool) => (
            <ToolCard
              key={tool.name}
              tool={tool}
              density={viewMode === "grid" ? "card" : "row"}
              onOpen={() => setDetailTool(tool)}
            />
          ))}
        </div>
      )}

      <ToolDetailsDialog
        tool={detailTool}
        open={Boolean(detailTool)}
        onOpenChange={(open) => {
          if (!open) setDetailTool(null);
        }}
      />
    </div>
  );
}

function InitTab({ chat, isSharedView }: { chat: any; isSharedView: boolean }) {
  const toolInit = chat?.config?.tool_init;
  const pretty = JSON.stringify(toolInit ?? {}, null, 2);
  const entries = useMemo(() => {
    if (!toolInit || typeof toolInit !== "object") return [] as Array<[string, unknown]>;
    return Object.entries(toolInit) as Array<[string, unknown]>;
  }, [toolInit]);
  const hasInit = entries.length > 0;

  if (isSharedView) {
    return (
      <Text type={TextTypes.Body7} color="muted">
        Tool init data is not available in shared views.
      </Text>
    );
  }

  return (
    <div className="space-y-3">
      <PanelCard
        title="Tool init"
        hint="The `tool_init` object this chat passes to integrations when tools require init."
        action={hasInit ? <CopyButton value={pretty} label="Copy JSON" /> : undefined}
      >
        <Text type={TextTypes.Body7} color="muted" className="mb-2 block">
          {hasInit
            ? `${entries.length} tool(s) carry init data. Init values are passed to the integration on every tool call.`
            : "This chat runs without per-tool init data."}
        </Text>
        {hasInit ? (
          <CodeBlock className="max-h-64" value={pretty} />
        ) : (
          <Text type={TextTypes.Body7} color="muted">
            No tool init configured for this chat.
          </Text>
        )}
      </PanelCard>

      {hasInit ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {entries.map(([name, value]) => (
            <PanelCard key={name} title={normalizeToolName(name)}>
              <code className="block break-all text-[11px] text-muted-foreground">{name}</code>
              <CodeBlock
                className="mt-2 max-h-40"
                value={typeof value === "object" ? JSON.stringify(value, null, 2) : String(value)}
              />
            </PanelCard>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function McpServersTab() {
  const { data, isLoading } = useSWR<MCPServersResponse>("/api/v1/tools/mcp/integrations", fetcher);
  const rows = data?.rows ?? [];
  const enabled = rows.filter((row) => row.enabled);

  return (
    <div className="space-y-3">
      <PanelCard title="Connected MCP servers" hint="Servers attached to this deployment and their auth state.">
        <Text type={TextTypes.Body7} color="muted">
          {enabled.length} of {rows.length} server(s) enabled
        </Text>
      </PanelCard>

      {isLoading && rows.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted">
          Loading MCP servers...
        </Text>
      ) : rows.length === 0 ? (
        <Text type={TextTypes.Body7} color="muted">
          No MCP servers are connected to this deployment.
        </Text>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {rows.map((row) => (
            <PanelCard
              key={row.name}
              title={row.name}
              action={
                <Badge variant={row.enabled ? "secondary" : "outline"}>
                  {row.enabled ? "Enabled" : "Disabled"}
                </Badge>
              }
            >
              <div className="flex flex-wrap items-center gap-2">
                <Server className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
                <Badge variant={row.has_auth_data ? "secondary" : "outline"}>
                  {row.has_auth_data ? "Authenticated" : "No auth data"}
                </Badge>
              </div>
            </PanelCard>
          ))}
        </div>
      )}
    </div>
  );
}

function OverviewTab({ chat, user }: { chat: any; user?: any }) {
  const config = chat?.config ?? {};
  const partner = chat?.partner;
  const chatTitle = (chat?.settings?.title || "").trim();

  const configuredTools: string[] = Array.isArray(config?.tools) ? config.tools : [];
  const initEntries =
    config?.tool_init && typeof config.tool_init === "object"
      ? Object.keys(config.tool_init as Record<string, unknown>)
      : [];

  return (
    <div className="grid grid-cols-1 gap-2.5 md:grid-cols-2 xl:grid-cols-3">
      <PanelCard
        title="Configuration"
        hint="Counts of the tools and init data attached to this chat."
        className="md:col-span-2 xl:col-span-3"
      >
        <div className="flex flex-wrap gap-2">
          <Badge variant="outline">
            <Wrench className="mr-1 h-3 w-3" aria-hidden />
            {configuredTools.length} tool(s)
          </Badge>
          <Badge variant={initEntries.length > 0 ? "secondary" : "outline"}>
            {initEntries.length} tool init entr(ies)
          </Badge>
          <Badge variant="outline">Backend: {config.backend ?? "-"}</Badge>
          {chat?.settings?.title ? <Badge variant="outline">Custom title</Badge> : null}
        </div>
      </PanelCard>

      <PanelCard title="Model">
        <KeyValueList
          entries={[
            ["Model", config.model ?? "-"],
            ["Backend", config.backend ?? "-"],
            ["Max tokens", config.max_tokens ?? "-"],
            ["Temperature", config.temperature ?? "-"],
            ["Context", config.context ?? "-"],
          ]}
        />
      </PanelCard>

      <PanelCard title="Chat">
        <KeyValueList
          entries={[
            ["Title", chatTitle || `${partner?.name || "Bot"} interaction`],
            ["Type", chat?.chat_type ?? "-"],
            ["Partner", partner?.name ?? "-"],
            ["Status", chat?.status?.state ?? "-"],
            ["Owner", user?.username ?? chat?.user?.username ?? "-"],
          ]}
        />
      </PanelCard>

      <PanelCard title="System prompt" className="md:col-span-2 xl:col-span-3">
        {config?.system_prompt ? (
          <p className="max-h-40 overflow-y-auto whitespace-pre-wrap text-sm text-muted-foreground">
            {config.system_prompt}
          </p>
        ) : (
          <Text type={TextTypes.Body7} color="muted">
            No system prompt configured.
          </Text>
        )}
      </PanelCard>
    </div>
  );
}

const CHAT_DETAILS_TAB_ICONS: Record<ChatDetailsTabId, typeof Info> = {
  overview: Info,
  init: Settings2,
  tools: Wrench,
  mcp: Server,
};

/**
 * Tabbed chat details body (ci#749). Rendered inside the collapsible chat
 * details card; the tab selection is owned by the caller so header menu items
 * can jump straight to a section.
 */
export function ChatDetailsPanel({
  chat,
  user,
  isSharedView = false,
  activeTab,
  onTabChange,
}: {
  chat: any;
  user?: any;
  isSharedView?: boolean;
  activeTab: ChatDetailsTabId;
  onTabChange: (tab: ChatDetailsTabId) => void;
}) {
  return (
    <div className="space-y-3">
      <div
        role="tablist"
        aria-label="Chat details sections"
        className="surface-sunken scrollbar-hidden -mx-1 flex gap-1 overflow-x-auto p-1"
      >
        {CHAT_DETAILS_TABS.map((tab) => {
          const isActive = tab.id === activeTab;
          const Icon = CHAT_DETAILS_TAB_ICONS[tab.id];
          return (
            <button
              key={tab.id}
              type="button"
              role="tab"
              id={`chat-details-tab-${tab.id}`}
              title={tab.description}
              aria-selected={isActive}
              aria-controls={`chat-details-panel-${tab.id}`}
              onClick={() => onTabChange(tab.id)}
              className={cn(
                "flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-[color,box-shadow,background-color]",
                isActive
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:bg-background/60 hover:text-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" aria-hidden />
              {tab.label}
            </button>
          );
        })}
      </div>

      <div
        role="tabpanel"
        id={`chat-details-panel-${activeTab}`}
        aria-labelledby={`chat-details-tab-${activeTab}`}
        tabIndex={0}
        className="max-h-[min(60vh,32rem)] overflow-y-auto overscroll-contain pr-0.5"
      >
        {activeTab === "overview" ? <OverviewTab chat={chat} user={user} /> : null}
        {activeTab === "init" ? <InitTab chat={chat} isSharedView={isSharedView} /> : null}
        {activeTab === "tools" ? <ToolsTab chat={chat} isSharedView={isSharedView} /> : null}
        {activeTab === "mcp" ? <McpServersTab /> : null}
      </div>
    </div>
  );
}

export default ChatDetailsPanel;
