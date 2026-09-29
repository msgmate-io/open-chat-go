import { useState } from "react";
import useSWR from "swr";
import Markdown from "react-markdown";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  LoadingSpinner,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { fetcher } from "../../lib/utils";

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

type OverviewTab = "readme" | "api" | "models" | "functions" | "pages" | "env";

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

function ModelFieldTree({ field, depth = 0 }: { field: IntegrationModelFieldOverview; depth?: number }) {
  return (
    <div className="space-y-1">
      <div
        className="rounded border border-border/60 bg-muted/20 px-3 py-2"
        style={{ marginLeft: depth > 0 ? `${depth * 12}px` : undefined }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <Text type={TextTypes.Body6} className="font-mono">
            {field.json_name || field.name}
          </Text>
          {field.json_name && field.json_name !== field.name ? (
            <Badge variant="outline" className="font-mono">
              {field.name}
            </Badge>
          ) : null}
          <Badge variant="outline">{field.kind}</Badge>
          <Badge variant="outline" className="font-mono">
            {field.type}
          </Badge>
          <Badge variant={field.required ? "secondary" : "outline"}>
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
      <div className="flex h-full items-center justify-center">
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
      <div className="mx-auto flex h-full w-full max-w-5xl flex-col gap-4 px-4 py-6 md:px-6">
        <Card className="border-destructive/30 bg-destructive/5">
          <CardHeader>
            <CardTitle className="text-lg">Integration not found</CardTitle>
            <CardDescription>
              Could not load integration details for <span className="font-mono">{integrationName}</span>.
              <br />
              {errorMessage}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {showBackButton ? (
              <Button type="button" variant="outline" onClick={() => navigateTo("/integrations")}>
                Back to integrations
              </Button>
            ) : null}
          </CardContent>
        </Card>
      </div>
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

  const tabs: Array<{ key: OverviewTab; label: string; count: number }> = [
    { key: "readme", label: "README.md", count: readmeMarkdown ? 1 : 0 },
    { key: "api", label: "API Routes", count: apiRouteKeys.length },
    { key: "models", label: "Models", count: models.length },
    { key: "functions", label: "Functions", count: functions.length },
    { key: "env", label: "Runtime Env", count: runtimeEnvVars.length },
    {
      key: "pages",
      label: "Pages",
      count: frontendRoutes.filter((route) => route.kind === "page").length,
    },
  ];
  const frontendPages = frontendRoutes.filter((route) => route.kind === "page");

  return (
    <div className="mx-auto flex h-full w-full max-w-5xl flex-col gap-4 overflow-y-auto px-4 py-6 md:px-6">
      <Card className="border-border/70 bg-gradient-to-br from-background via-background to-muted/30">
        <CardHeader className="space-y-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle className="text-2xl">Integration: {data.name}</CardTitle>
              <CardDescription>
                API, models, functions, and pages overview for this compiled integration module.
              </CardDescription>
            </div>
            {showBackButton ? (
              <Button type="button" variant="outline" onClick={() => navigateTo("/integrations")}>
                Back
              </Button>
            ) : null}
          </div>
          <div className="flex flex-wrap gap-2">
            {tabs.map((tab) => (
              <Button
                key={tab.key}
                type="button"
                size="sm"
                variant={activeTab === tab.key ? "default" : "outline"}
                onClick={() => setActiveTab(tab.key)}
              >
                {tab.label}: {tab.count}
              </Button>
            ))}
          </div>
        </CardHeader>
      </Card>

      {activeTab === "api" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            API Overview
          </Text>
          {apiRouteKeys.length ? (
            <div className="space-y-2">
              {apiRouteKeys.map((routeKey) => {
                const routeOverview = apiRouteOverviewByRoute.get(routeKey) ?? buildRouteOverview(routeKey, openapi);
                const route = routeOverview.route;
                const parameters = routeOverview.parameters ?? fallbackPathParameters(route);
                const auth = routeOverview.required_auth ?? [];
                return (
                  <Collapsible key={route}>
                    <Card>
                      <CardHeader className="pb-3">
                        <CollapsibleTrigger asChild>
                          <Button type="button" variant="outline" className="w-full justify-between font-mono text-left">
                            <span>{route}</span>
                            <Badge variant="outline">{parameters.length} params</Badge>
                          </Button>
                        </CollapsibleTrigger>
                      </CardHeader>
                      <CollapsibleContent>
                        <CardContent className="space-y-3 pt-0">
                          <div className="space-y-1">
                            <Text type={TextTypes.Body6} bold>
                              Description
                            </Text>
                            <Text type={TextTypes.Body7} color="muted">
                              {routeOverview.summary ||
                                routeOverview.description ||
                                "No API description found for this endpoint."}
                            </Text>
                          </div>
                          <div className="space-y-1">
                            <Text type={TextTypes.Body6} bold>
                              Required Auth
                            </Text>
                            <div className="flex flex-wrap gap-2">
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
                          <div className="space-y-2">
                            <Text type={TextTypes.Body6} bold>
                              Parameters
                            </Text>
                            {parameters.length ? (
                              <div className="space-y-2">
                                {parameters.map((param, index) => (
                                  <div
                                    key={`${param.in || "unknown"}-${param.name || "param"}-${index}`}
                                    className="rounded border border-border/60 bg-muted/20 px-3 py-2"
                                  >
                                    <div className="flex flex-wrap items-center gap-2">
                                      <Text type={TextTypes.Body6} className="font-mono">
                                        {param.name || "unnamed"}
                                      </Text>
                                      <Badge variant="outline">{param.in || "unknown"}</Badge>
                                      <Badge variant={param.required ? "secondary" : "outline"}>
                                        {param.required ? "required" : "optional"}
                                      </Badge>
                                      <Badge variant="outline">{param.type || "unknown"}</Badge>
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
                        </CardContent>
                      </CollapsibleContent>
                    </Card>
                  </Collapsible>
                );
              })}
            </div>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No API routes registered</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}

      {activeTab === "readme" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            README.md
          </Text>
          {readmeMarkdown ? (
            <Card>
              <CardContent className="pt-6">
                <div className="chat-markdown">
                  <Markdown>{readmeMarkdown}</Markdown>
                </div>
              </CardContent>
            </Card>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No README available</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}

      {activeTab === "models" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            Model Overview
          </Text>
          {models.length ? (
            <div className="space-y-3">
              {models.map((model) => (
                <Collapsible key={`${model.type_name}-${model.kind}`}>
                  <Card>
                    <CardHeader className="pb-3">
                      <CollapsibleTrigger asChild>
                        <Button type="button" variant="outline" className="w-full justify-between text-left">
                          <span>{model.type_name}</span>
                          <div className="flex items-center gap-2">
                            <Badge variant="outline">{model.kind}</Badge>
                            <Badge variant="outline">{model.fields?.length || 0} fields</Badge>
                          </div>
                        </Button>
                      </CollapsibleTrigger>
                    </CardHeader>
                    <CollapsibleContent>
                      <CardContent className="space-y-2 pt-0">
                        {model.fields?.length ? (
                          model.fields.map((field) => (
                            <ModelFieldTree key={`${model.type_name}-${field.name}-${field.type}`} field={field} />
                          ))
                        ) : (
                          <Badge variant="outline">No schema fields discovered</Badge>
                        )}
                      </CardContent>
                    </CollapsibleContent>
                  </Card>
                </Collapsible>
              ))}
            </div>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No models registered</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}

      {activeTab === "functions" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            Functions
          </Text>
          {functions.length ? (
            <Card>
              <CardContent className="flex flex-wrap gap-2 pt-6">
                {functions.map((fn) => (
                  <Badge key={fn} variant="outline" className="font-mono">
                    {fn}
                  </Badge>
                ))}
              </CardContent>
            </Card>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No functions registered</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}

      {activeTab === "env" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            Runtime Environment Variables
          </Text>
          {runtimeEnvVars.length ? (
            <div className="space-y-2">
              {runtimeEnvVars.map((envVar) => (
                <Card key={envVar.key}>
                  <CardContent className="space-y-2 pt-6">
                    <div className="flex flex-wrap items-center gap-2">
                      <Text type={TextTypes.Body6} className="font-mono">
                        {envVar.key}
                      </Text>
                      <Badge variant={envVar.sensitive ? "secondary" : "outline"}>
                        {envVar.sensitive ? "sensitive" : "public"}
                      </Badge>
                    </div>
                    {envVar.description ? (
                      <Text type={TextTypes.Body7} color="muted">
                        {envVar.description}
                      </Text>
                    ) : null}
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No runtime env vars declared</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}

      {activeTab === "pages" ? (
        <section className="space-y-3">
          <Text type={TextTypes.Heading6} tag="h2" bold>
            Integration Pages
          </Text>
          {frontendPages.length ? (
            <div className="space-y-2">
              {frontendPages.map((page) => (
                <Card key={`${page.kind}-${page.route}`}>
                  <CardContent className="flex flex-wrap items-center justify-between gap-3 pt-6">
                    <div className="space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant="outline">{page.kind}</Badge>
                        <Badge variant={page.public ? "secondary" : "outline"}>
                          {page.public ? "public" : "auth"}
                        </Badge>
                        <Text type={TextTypes.Body6} className="font-mono">
                          {page.route}
                        </Text>
                      </div>
                      {page.description ? (
                        <Text type={TextTypes.Body7} color="muted">
                          {page.description}
                        </Text>
                      ) : null}
                      {page.asset_path ? (
                        <Text type={TextTypes.Body7} color="muted" className="font-mono">
                          Asset: {page.asset_path}
                        </Text>
                      ) : null}
                    </div>
                    <Button type="button" variant="outline" onClick={() => navigateTo(page.route)}>
                      Open page
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <Card className="border-dashed border-border/70 bg-muted/30">
              <CardHeader>
                <CardTitle className="text-base">No frontend pages registered</CardTitle>
              </CardHeader>
            </Card>
          )}
        </section>
      ) : null}
    </div>
  );
}
