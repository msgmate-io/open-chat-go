import { useState, type ReactNode } from "react";
import useSWR from "swr";
import Markdown from "react-markdown";
import {
  ArrowLeft,
  BookOpen,
  Bot,
  Boxes,
  ChevronDown,
  Code2,
  ExternalLink,
  FileCode2,
  FileText,
  KeyRound,
  LayoutGrid,
  Network,
} from "lucide-react";
import {
  Badge,
  Button,
  Card,
  CardContent,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  LoadingSpinner,
  Text,
  TextTypes,
  cn,
} from "@open-chat-go/ui";
import { fetcher } from "../../lib/utils";
import { trimTrailingSlashes } from "../../lib/pinned-integrations";

type IntegrationModelFieldOverview = {
  name: string;
  json_name?: string;
  type: string;
  kind: string;
  required: boolean;
  fields?: IntegrationModelFieldOverview[];
};

type IntegrationModelOverview = {
  type_name: string;
  kind: string;
  fields?: IntegrationModelFieldOverview[];
};

type IntegrationAPIParameterOverview = {
  name: string;
  in: string;
  type: string;
  required: boolean;
  description?: string;
};

type IntegrationAPIRouteOverview = {
  route: string;
  summary?: string;
  description?: string;
  required_auth?: string[];
  parameters?: IntegrationAPIParameterOverview[];
};

type IntegrationFrontendRouteOverview = {
  route: string;
  kind: string;
  public: boolean;
  description?: string;
  asset_path?: string;
  title?: string;
  section?: string;
  order?: number;
};

type IntegrationDefaultBotOverview = {
  primary_owner: string;
  additional_owners?: string[];
  username: string;
  email?: string;
  name: string;
  description?: string;
  is_public?: boolean;
  is_active?: boolean;
  uses_random_password: boolean;
  overwrite_if_exists: boolean;
  default_shared_config?: Record<string, unknown>;
};

type IntegrationOverviewResponse = {
  name: string;
  readme_markdown?: string;
  api_routes: string[];
  api_routes_overview: IntegrationAPIRouteOverview[];
  frontend_routes: IntegrationFrontendRouteOverview[];
  models: IntegrationModelOverview[];
  functions: string[];
  runtime_env_vars?: Array<{
    key: string;
    sensitive: boolean;
    description?: string;
  }>;
  default_bots?: IntegrationDefaultBotOverview[];
  labels?: string[];
  source_repository?: string;
  source_ref?: string;
};

type OpenAPISchema = {
  type?: string;
};

type OpenAPIParameter = {
  name?: string;
  in?: string;
  required?: boolean;
  description?: string;
  schema?: OpenAPISchema;
};

type OpenAPIRequestBody = {
  required?: boolean;
  description?: string;
  content?: Record<string, { schema?: OpenAPISchema }>;
};

type OpenAPIOperation = {
  summary?: string;
  description?: string;
  security?: Array<Record<string, string[]>>;
  parameters?: OpenAPIParameter[];
  requestBody?: OpenAPIRequestBody;
};

type OpenAPIDocument = {
  security?: Array<Record<string, string[]>>;
  paths?: Record<string, Record<string, OpenAPIOperation>>;
};

type OverviewTab = "readme" | "api" | "models" | "functions" | "bots" | "pages" | "docs" | "env";

const METHOD_CLASSES: Record<string, string> = {
  GET: "border-sky-300/70 bg-sky-50 text-sky-700 dark:bg-sky-950/30 dark:text-sky-300",
  POST: "border-emerald-300/70 bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300",
  PUT: "border-amber-300/70 bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300",
  PATCH: "border-violet-300/70 bg-violet-50 text-violet-700 dark:bg-violet-950/30 dark:text-violet-300",
  DELETE: "border-rose-300/70 bg-rose-50 text-rose-700 dark:bg-rose-950/30 dark:text-rose-300",
};

function normalizeRepoUrl(repo?: string): string | null {
  if (!repo) {
    return null;
  }
  // ci#737: linear trailing-slash trim instead of a backtracking regex quantifier.
  const trimmed = trimTrailingSlashes(repo.trim().replace(/\.git$/i, ""));
  return trimmed || null;
}

function buildCodeSearchUrl(repo: string | null, query?: string): string | null {
  if (!repo) {
    return null;
  }
  const search = (query ?? "").trim();
  if (!search) {
    return repo;
  }
  return `${repo}/search?q=${encodeURIComponent(search)}&type=code`;
}

function buildFileUrl(repo: string | null, ref: string, path: string): string | null {
  if (!repo) {
    return null;
  }
  return `${repo}/blob/${encodeURIComponent(ref || "main")}/${path.replace(/^\/+/, "")}`;
}

function parseRoute(route: string): { method: string; path: string } | null {
  const trimmed = route.trim();
  if (!trimmed) {
    return null;
  }
  const firstSpace = trimmed.indexOf(" ");
  if (firstSpace <= 0) {
    return null;
  }
  const method = trimmed.slice(0, firstSpace).toUpperCase();
  const path = trimmed.slice(firstSpace + 1).trim();
  if (!path) {
    return null;
  }
  return { method, path };
}

function fallbackPathParameters(route: string): IntegrationAPIParameterOverview[] {
  const parsed = parseRoute(route);
  if (!parsed) {
    return [];
  }
  const matches = parsed.path.match(/\{([^}]+)\}/g) || [];
  return matches.map((raw) => {
    const name = raw.slice(1, -1).trim();
    return {
      name,
      in: "path",
      type: "string",
      required: true,
      description: `Path parameter '${name}' extracted from route template.`,
    };
  });
}

function getOpenAPIOperation(doc: OpenAPIDocument | undefined, route: string): OpenAPIOperation | null {
  if (!doc?.paths) {
    return null;
  }
  const parsed = parseRoute(route);
  if (!parsed) {
    return null;
  }
  const method = parsed.method.toLowerCase();
  const basePath = parsed.path.startsWith("/") ? parsed.path : `/${parsed.path}`;
  const candidates = basePath.startsWith("/api/") ? [basePath] : [`/api/v1${basePath}`, basePath];
  for (const candidate of candidates) {
    const pathItem = doc.paths[candidate];
    if (pathItem && pathItem[method]) {
      return pathItem[method];
    }
  }
  return null;
}

function mapSecurityToAuthLabels(
  operation: OpenAPIOperation | null,
  doc: OpenAPIDocument | undefined,
): string[] {
  const security = operation?.security ?? doc?.security ?? [];
  const labels = new Set<string>();
  for (const entry of security) {
    for (const schemeName of Object.keys(entry || {})) {
      if (schemeName) {
        labels.add(schemeName);
      }
    }
  }
  return Array.from(labels).sort();
}

function mapOperationParameters(
  route: string,
  operation: OpenAPIOperation | null,
): IntegrationAPIParameterOverview[] {
  const params = (operation?.parameters || []).map((param) => ({
    name: param.name || "",
    in: param.in || "unknown",
    type: param.schema?.type || "unknown",
    required: Boolean(param.required),
    description: param.description,
  }));
  const requestBody = operation?.requestBody;
  if (requestBody) {
    const firstContent = requestBody.content && Object.values(requestBody.content)[0];
    params.push({
      name: "body",
      in: "body",
      type: firstContent?.schema?.type || "object",
      required: Boolean(requestBody.required),
      description: requestBody.description || "Request body",
    });
  }
  if (params.length > 0) {
    return params;
  }
  return fallbackPathParameters(route);
}

function buildRouteOverview(
  route: string,
  doc: OpenAPIDocument | undefined,
): IntegrationAPIRouteOverview {
  const operation = getOpenAPIOperation(doc, route);
  return {
    route,
    summary: operation?.summary,
    description: operation?.description,
    required_auth: mapSecurityToAuthLabels(operation, doc),
    parameters: mapOperationParameters(route, operation),
  };
}

function SourceLink({ href, title = "View source" }: { href: string | null; title?: string }) {
  if (!href) {
    return null;
  }
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      title={title}
      aria-label={title}
      onClick={(event) => event.stopPropagation()}
      className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
    >
      <ExternalLink className="size-3.5" />
    </a>
  );
}

function SectionHeader({
  icon: Icon,
  title,
  count,
  action,
}: {
  icon: typeof Code2;
  title: string;
  count?: number;
  action?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-2">
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <Text type={TextTypes.Body5} bold>
        {title}
      </Text>
      {typeof count === "number" ? (
        <Badge variant="outline" className="font-normal">
          {count}
        </Badge>
      ) : null}
      {action ? <div className="ml-auto">{action}</div> : null}
    </div>
  );
}

function EmptyRow({ label }: { label: string }) {
  return (
    <Card className="border-dashed border-border/70 bg-muted/20">
      <CardContent className="p-4">
        <Text type={TextTypes.Body6} color="muted">
          {label}
        </Text>
      </CardContent>
    </Card>
  );
}

function MethodBadge({ method }: { method: string }) {
  return (
    <Badge
      variant="outline"
      className={cn("shrink-0 font-mono text-[10px] uppercase", METHOD_CLASSES[method] ?? "")}
    >
      {method}
    </Badge>
  );
}

function ModelFieldTree({ field, depth = 0 }: { field: IntegrationModelFieldOverview; depth?: number }) {
  return (
    <div className="space-y-1">
      <div
        className="rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5"
        style={{ marginLeft: depth > 0 ? `${depth * 10}px` : undefined }}
      >
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <Text type={TextTypes.Body7} className="font-mono font-medium">
            {field.json_name || field.name}
          </Text>
          {field.json_name && field.json_name !== field.name ? (
            <Badge variant="outline" className="font-mono text-[10px]">
              {field.name}
            </Badge>
          ) : null}
          <Badge variant="outline" className="text-[10px]">
            {field.kind}
          </Badge>
          <Badge variant="outline" className="font-mono text-[10px]">
            {field.type}
          </Badge>
          <Badge variant={field.required ? "secondary" : "outline"} className="text-[10px]">
            {field.required ? "required" : "optional"}
          </Badge>
        </div>
      </div>
      {field.fields?.length
        ? field.fields.map((child) => (
            <ModelFieldTree key={`${field.name}-${child.name}-${child.type}`} field={child} depth={depth + 1} />
          ))
        : null}
    </div>
  );
}

export function IntegrationOverviewView({
  integrationName,
  navigateTo,
  showBackButton = true,
}: {
  integrationName: string;
  navigateTo: (to: string) => void;
  showBackButton?: boolean;
}) {
  const [activeTab, setActiveTab] = useState<OverviewTab>("readme");
  const encodedName = encodeURIComponent(integrationName);
  const { data, isLoading, error } = useSWR<IntegrationOverviewResponse>(
    encodedName ? `/api/v1/integrations/${encodedName}/overview` : null,
    fetcher,
  );
  const { data: openapi } = useSWR<OpenAPIDocument>("/api/openapi.json", fetcher);

  if (isLoading) {
    return (
      <div className="flex flex-1 items-center justify-center py-16">
        <LoadingSpinner />
      </div>
    );
  }

  if (error || !data) {
    const errorMessage =
      error instanceof Error && error.message.trim().length > 0
        ? error.message.trim()
        : "Could not load integration overview from API.";
    return (
      <Card className="border-destructive/30 bg-destructive/5">
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-2">
            <Text type={TextTypes.Body5} bold>
              Integration not found
            </Text>
            <Badge variant="outline" className="font-mono">
              {integrationName}
            </Badge>
          </div>
          <Text type={TextTypes.Body7} color="muted">
            {errorMessage}
          </Text>
          {showBackButton ? (
            <Button type="button" variant="outline" size="sm" onClick={() => navigateTo("/integrations")}>
              <ArrowLeft className="size-4" />
              Back to integrations
            </Button>
          ) : null}
        </CardContent>
      </Card>
    );
  }

  const apiRoutes = Array.isArray(data.api_routes) ? data.api_routes : [];
  const apiRouteOverviews = Array.isArray(data.api_routes_overview) ? data.api_routes_overview : [];
  const apiRouteOverviewByRoute = new Map(
    apiRouteOverviews
      .filter((overview) => typeof overview?.route === "string" && overview.route.trim().length > 0)
      .map((overview) => [overview.route, overview] as const),
  );
  const apiRouteKeys = apiRoutes.length
    ? apiRoutes
    : apiRouteOverviews
        .map((overview) => overview.route)
        .filter((route): route is string => typeof route === "string" && route.trim().length > 0);
  const models = Array.isArray(data.models) ? data.models : [];
  const functions = Array.isArray(data.functions) ? data.functions : [];
  const frontendRoutes = Array.isArray(data.frontend_routes) ? data.frontend_routes : [];
  const readmeMarkdown = typeof data.readme_markdown === "string" ? data.readme_markdown.trim() : "";
  const runtimeEnvVars = Array.isArray(data.runtime_env_vars) ? data.runtime_env_vars : [];
  const bots = Array.isArray(data.default_bots) ? data.default_bots : [];
  const labels = Array.isArray(data.labels) ? data.labels : [];

  const repo = normalizeRepoUrl(data.source_repository);
  const ref = data.source_ref?.trim() || "main";
  const readmeSourceUrl = buildFileUrl(repo, ref, "README.md");

  const tabs: Array<{ key: OverviewTab; label: string; count: number }> = [
    { key: "readme", label: "README", count: readmeMarkdown ? 1 : 0 },
    { key: "api", label: "API Routes", count: apiRouteKeys.length },
    { key: "models", label: "Models", count: models.length },
    { key: "functions", label: "Functions", count: functions.length },
    { key: "bots", label: "Bots", count: bots.length },
    { key: "env", label: "Runtime Env", count: runtimeEnvVars.length },
    {
      key: "pages",
      label: "Pages",
      count: frontendRoutes.filter((route) => route.kind === "page").length,
    },
    {
      key: "docs",
      label: "Docs",
      count: frontendRoutes.filter((route) => route.kind === "doc").length,
    },
  ];
  const frontendPages = frontendRoutes.filter((route) => route.kind === "page");
  const frontendDocs = frontendRoutes
    .filter((route) => route.kind === "doc")
    .sort((a, b) => {
      const sectionOrder = (a.section || "").localeCompare(b.section || "");
      if (sectionOrder !== 0) return sectionOrder;
      const orderDiff = (a.order ?? Number.MAX_SAFE_INTEGER) - (b.order ?? Number.MAX_SAFE_INTEGER);
      if (orderDiff !== 0) return orderDiff;
      return a.route.localeCompare(b.route);
    });

  const initial = (data.name || integrationName || "?").charAt(0).toUpperCase();

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Card className="border border-border/70 bg-card/95 p-2.5 shadow-sm md:p-3">
        <div className="flex items-center gap-2">
          {showBackButton ? (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => navigateTo("/integrations")}
              aria-label="Back to integrations"
              title="Back to integrations"
              className="size-8 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
            </Button>
          ) : null}
          <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground md:size-9 md:text-sm">
            {initial}
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold text-foreground">{data.name || integrationName}</p>
            <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-1.5">
              <Text type={TextTypes.Body7} color="muted" className="truncate">
                Compiled integration module
              </Text>
              {labels.map((label) => (
                <Badge
                  key={label}
                  variant={label.toLowerCase() === "core" ? "secondary" : "outline"}
                  className="capitalize"
                >
                  {label}
                </Badge>
              ))}
            </div>
          </div>
          {repo ? (
            <Button asChild variant="outline" size="sm" className="hidden shrink-0 sm:inline-flex">
              <a href={repo} target="_blank" rel="noreferrer">
                <Code2 className="size-3.5" />
                Source
              </a>
            </Button>
          ) : null}
        </div>
      </Card>

      <div className="-mx-1 flex items-center gap-1 overflow-x-auto px-1 pb-0.5">
        {tabs.map((tab) => {
          const isActive = activeTab === tab.key;
          return (
            <button
              key={tab.key}
              type="button"
              onClick={() => setActiveTab(tab.key)}
              className={cn(
                "inline-flex h-7 shrink-0 items-center gap-1 rounded-full border px-2.5 text-xs font-medium transition-colors",
                isActive
                  ? "border-transparent bg-primary text-primary-foreground"
                  : "border-border/70 bg-card/80 text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
            >
              {tab.label}
              {tab.count > 0 ? (
                <span className={cn("text-[10px]", isActive ? "opacity-80" : "opacity-70")}>{tab.count}</span>
              ) : null}
            </button>
          );
        })}
      </div>

      {activeTab === "readme" ? (
        <section className="space-y-2">
          <SectionHeader
            icon={FileText}
            title="README"
            action={<SourceLink href={readmeSourceUrl} title="View README.md on GitHub" />}
          />
          {readmeMarkdown ? (
            <Card className="border-border/70 bg-card/90">
              <CardContent className="min-w-0 overflow-hidden p-4">
                <div className="chat-markdown break-words">
                  <Markdown>{readmeMarkdown}</Markdown>
                </div>
              </CardContent>
            </Card>
          ) : (
            <EmptyRow label="No README available for this integration." />
          )}
        </section>
      ) : null}

      {activeTab === "api" ? (
        <section className="space-y-2">
          <SectionHeader icon={Network} title="API Routes" count={apiRouteKeys.length} />
          {apiRouteKeys.length ? (
            <div className="space-y-1.5">
              {apiRouteKeys.map((routeKey) => {
                const routeOverview = apiRouteOverviewByRoute.get(routeKey) ?? buildRouteOverview(routeKey, openapi);
                const route = routeOverview.route;
                const parameters = routeOverview.parameters ?? fallbackPathParameters(route);
                const auth = routeOverview.required_auth ?? [];
                const parsed = parseRoute(route);
                const sourceHref = buildCodeSearchUrl(repo, parsed?.path ?? route);
                return (
                  <Collapsible key={route}>
                    <Card className="overflow-hidden border-border/70 bg-card/90">
                      <div className="flex items-center gap-1 pr-2">
                        <CollapsibleTrigger asChild>
                          <button
                            type="button"
                            className="group flex min-w-0 flex-1 items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-muted/40"
                          >
                            <MethodBadge method={parsed?.method ?? "REQ"} />
                            <span className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">
                              {parsed?.path ?? route}
                            </span>
                            <Badge variant="outline" className="hidden shrink-0 sm:inline-flex">
                              {parameters.length} params
                            </Badge>
                            <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
                          </button>
                        </CollapsibleTrigger>
                        <SourceLink href={sourceHref} title="Find this route in the integration source" />
                      </div>
                      <CollapsibleContent>
                        <div className="space-y-3 border-t border-border/60 px-3 py-3">
                          <div className="space-y-1">
                            <Text type={TextTypes.Body7} bold>
                              Description
                            </Text>
                            <Text type={TextTypes.Body7} color="muted">
                              {routeOverview.summary ||
                                routeOverview.description ||
                                "No API description found for this endpoint."}
                            </Text>
                          </div>
                          <div className="space-y-1">
                            <Text type={TextTypes.Body7} bold>
                              Required Auth
                            </Text>
                            <div className="flex flex-wrap gap-1.5">
                              {auth.length ? (
                                auth.map((label) => (
                                  <Badge key={label} variant="secondary">
                                    {label}
                                  </Badge>
                                ))
                              ) : (
                                <Badge variant="outline">No auth required</Badge>
                              )}
                            </div>
                          </div>
                          <div className="space-y-1.5">
                            <Text type={TextTypes.Body7} bold>
                              Parameters
                            </Text>
                            {parameters.length ? (
                              <div className="space-y-1.5">
                                {parameters.map((param, index) => (
                                  <div
                                    key={`${param.in || "unknown"}-${param.name || "param"}-${index}`}
                                    className="rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5"
                                  >
                                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                                      <Text type={TextTypes.Body7} className="font-mono font-medium">
                                        {param.name || "unnamed"}
                                      </Text>
                                      <Badge variant="outline" className="text-[10px]">
                                        {param.in || "unknown"}
                                      </Badge>
                                      <Badge variant={param.required ? "secondary" : "outline"} className="text-[10px]">
                                        {param.required ? "required" : "optional"}
                                      </Badge>
                                      <Badge variant="outline" className="text-[10px]">
                                        {param.type || "unknown"}
                                      </Badge>
                                    </div>
                                    {param.description ? (
                                      <Text type={TextTypes.Body7} color="muted" className="mt-1">
                                        {param.description}
                                      </Text>
                                    ) : null}
                                  </div>
                                ))}
                              </div>
                            ) : (
                              <Badge variant="outline">No parameters</Badge>
                            )}
                          </div>
                        </div>
                      </CollapsibleContent>
                    </Card>
                  </Collapsible>
                );
              })}
            </div>
          ) : (
            <EmptyRow label="No API routes registered for this integration." />
          )}
        </section>
      ) : null}

      {activeTab === "models" ? (
        <section className="space-y-2">
          <SectionHeader icon={Boxes} title="Models" count={models.length} />
          {models.length ? (
            <div className="space-y-1.5">
              {models.map((model) => {
                const sourceHref = buildCodeSearchUrl(repo, `type ${model.type_name}`);
                return (
                  <Collapsible key={`${model.type_name}-${model.kind}`}>
                    <Card className="overflow-hidden border-border/70 bg-card/90">
                      <div className="flex items-center gap-1 pr-2">
                        <CollapsibleTrigger asChild>
                          <button
                            type="button"
                            className="group flex min-w-0 flex-1 items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-muted/40"
                          >
                            <span className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">
                              {model.type_name}
                            </span>
                            <Badge variant="outline" className="shrink-0">
                              {model.kind}
                            </Badge>
                            <Badge variant="outline" className="hidden shrink-0 sm:inline-flex">
                              {model.fields?.length || 0} fields
                            </Badge>
                            <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
                          </button>
                        </CollapsibleTrigger>
                        <SourceLink href={sourceHref} title="Find this model in the integration source" />
                      </div>
                      <CollapsibleContent>
                        <div className="space-y-1.5 border-t border-border/60 px-3 py-3">
                          {model.fields?.length ? (
                            model.fields.map((field) => (
                              <ModelFieldTree key={`${model.type_name}-${field.name}-${field.type}`} field={field} />
                            ))
                          ) : (
                            <Badge variant="outline">No schema fields discovered</Badge>
                          )}
                        </div>
                      </CollapsibleContent>
                    </Card>
                  </Collapsible>
                );
              })}
            </div>
          ) : (
            <EmptyRow label="No models registered for this integration." />
          )}
        </section>
      ) : null}

      {activeTab === "functions" ? (
        <section className="space-y-2">
          <SectionHeader icon={Code2} title="Functions" count={functions.length} />
          {functions.length ? (
            <Card className="border-border/70 bg-card/90">
              <CardContent className="space-y-1 p-3">
                {functions.map((fn) => (
                  <div
                    key={fn}
                    className="flex min-w-0 items-center gap-2 rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5"
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{fn}</span>
                    <SourceLink
                      href={buildCodeSearchUrl(repo, `func ${fn}`)}
                      title="Find this function in the integration source"
                    />
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : (
            <EmptyRow label="No functions registered for this integration." />
          )}
        </section>
      ) : null}

      {activeTab === "bots" ? (
        <section className="space-y-2">
          <SectionHeader icon={Bot} title="Bots" count={bots.length} />
          {bots.length ? (
            <div className="grid grid-cols-1 gap-2 xl:grid-cols-2">
              {bots.map((bot, index) => {
                const sharedConfig = bot.default_shared_config ?? {};
                const hasSharedConfig = Object.keys(sharedConfig).length > 0;
                return (
                  <Card key={`${bot.username}-${index}`} className="overflow-hidden border-border/70 bg-card/90">
                    <CardContent className="space-y-2 p-3">
                      <div className="flex min-w-0 items-center gap-2">
                        <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-secondary text-secondary-foreground">
                          <Bot className="size-3.5" />
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm font-semibold text-foreground">
                            {bot.name || bot.username || "Bot"}
                          </p>
                          <Text type={TextTypes.Body7} color="muted" className="truncate font-mono">
                            {bot.username || bot.email || "-"}
                          </Text>
                        </div>
                        <div className="flex shrink-0 flex-wrap justify-end gap-1">
                          {bot.is_public ? <Badge variant="secondary">public</Badge> : null}
                          {bot.is_active ? (
                            <Badge variant="secondary">active</Badge>
                          ) : bot.is_active === false ? (
                            <Badge variant="outline">inactive</Badge>
                          ) : null}
                        </div>
                      </div>
                      {bot.description ? (
                        <Text type={TextTypes.Body7} color="muted">
                          {bot.description}
                        </Text>
                      ) : null}
                      <div className="space-y-0.5">
                        <Text type={TextTypes.Body7} color="muted" className="truncate">
                          Owner: <span className="text-foreground">{bot.primary_owner || "-"}</span>
                          {(bot.additional_owners ?? []).length
                            ? ` + ${(bot.additional_owners ?? []).join(", ")}`
                            : ""}
                        </Text>
                        {bot.email ? (
                          <Text type={TextTypes.Body7} color="muted" className="truncate">
                            Email: <span className="text-foreground">{bot.email}</span>
                          </Text>
                        ) : null}
                        <div className="flex flex-wrap gap-1 pt-0.5">
                          {bot.uses_random_password ? <Badge variant="outline">random password</Badge> : null}
                          {bot.overwrite_if_exists ? <Badge variant="outline">overwrite if exists</Badge> : null}
                        </div>
                      </div>
                      {hasSharedConfig ? (
                        <Collapsible>
                          <CollapsibleTrigger asChild>
                            <button
                              type="button"
                              className="group flex w-full items-center gap-1.5 rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5 text-left transition-colors hover:bg-muted/40"
                            >
                              <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" />
                              <Text type={TextTypes.Body7} bold className="flex-1">
                                Default shared config
                              </Text>
                              <ChevronDown className="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
                            </button>
                          </CollapsibleTrigger>
                          <CollapsibleContent>
                            <pre className="mt-1.5 max-h-56 overflow-auto rounded-md border border-border/60 bg-muted/30 p-2.5 text-[11px] leading-5">
                              {JSON.stringify(sharedConfig, null, 2)}
                            </pre>
                          </CollapsibleContent>
                        </Collapsible>
                      ) : null}
                    </CardContent>
                  </Card>
                );
              })}
            </div>
          ) : (
            <EmptyRow label="This integration does not register any default bots." />
          )}
        </section>
      ) : null}

      {activeTab === "env" ? (
        <section className="space-y-2">
          <SectionHeader icon={KeyRound} title="Runtime Environment" count={runtimeEnvVars.length} />
          {runtimeEnvVars.length ? (
            <Card className="border-border/70 bg-card/90">
              <CardContent className="space-y-1 p-3">
                {runtimeEnvVars.map((envVar) => (
                  <div key={envVar.key} className="rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5">
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                      <Text type={TextTypes.Body7} className="font-mono font-medium">
                        {envVar.key}
                      </Text>
                      <Badge variant={envVar.sensitive ? "secondary" : "outline"} className="text-[10px]">
                        {envVar.sensitive ? "sensitive" : "public"}
                      </Badge>
                    </div>
                    {envVar.description ? (
                      <Text type={TextTypes.Body7} color="muted" className="mt-0.5">
                        {envVar.description}
                      </Text>
                    ) : null}
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : (
            <EmptyRow label="No runtime environment variables declared." />
          )}
        </section>
      ) : null}

      {activeTab === "pages" ? (
        <section className="space-y-2">
          <SectionHeader icon={LayoutGrid} title="Integration Pages" count={frontendPages.length} />
          {frontendPages.length ? (
            <Card className="border-border/70 bg-card/90">
              <CardContent className="space-y-1 p-3">
                {frontendPages.map((page) => (
                  <div
                    key={`${page.kind}-${page.route}`}
                    className="flex min-w-0 items-center gap-2 rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5"
                  >
                    <Badge variant={page.public ? "secondary" : "outline"} className="shrink-0 text-[10px]">
                      {page.public ? "public" : "auth"}
                    </Badge>
                    <div className="min-w-0 flex-1">
                      <Text type={TextTypes.Body7} className="truncate font-mono">
                        {page.route}
                      </Text>
                      {page.description ? (
                        <Text type={TextTypes.Body7} color="muted" className="truncate">
                          {page.description}
                        </Text>
                      ) : null}
                    </div>
                    <SourceLink
                      href={buildCodeSearchUrl(repo, page.route)}
                      title="Find this page in the integration source"
                    />
                    <Button type="button" variant="outline" size="sm" onClick={() => navigateTo(page.route)}>
                      Open
                    </Button>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : (
            <EmptyRow label="No frontend pages registered." />
          )}
        </section>
      ) : null}

      {activeTab === "docs" ? (
        <section className="space-y-2">
          <SectionHeader icon={BookOpen} title="Documentation" count={frontendDocs.length} />
          {frontendDocs.length ? (
            <Card className="border-border/70 bg-card/90">
              <CardContent className="space-y-1 p-3">
                {frontendDocs.map((doc) => (
                  <div
                    key={`${doc.kind}-${doc.route}`}
                    className="flex min-w-0 items-center gap-2 rounded-md border border-border/60 bg-muted/20 px-2.5 py-1.5"
                  >
                    {doc.section ? (
                      <Badge variant="secondary" className="shrink-0">
                        {doc.section}
                      </Badge>
                    ) : null}
                    <div className="min-w-0 flex-1">
                      <Text type={TextTypes.Body7} bold className="truncate">
                        {doc.title?.trim() || doc.route}
                      </Text>
                      {doc.description ? (
                        <Text type={TextTypes.Body7} color="muted" className="truncate">
                          {doc.description}
                        </Text>
                      ) : null}
                      <Text type={TextTypes.Body7} color="muted" className="truncate font-mono">
                        {doc.route}
                      </Text>
                    </div>
                    <SourceLink
                      href={buildCodeSearchUrl(repo, doc.route)}
                      title="Find this doc page in the integration source"
                    />
                    <Button type="button" variant="outline" size="sm" onClick={() => navigateTo(doc.route)}>
                      Open
                    </Button>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : (
            <EmptyRow label="No documentation pages registered." />
          )}
        </section>
      ) : null}
    </div>
  );
}
