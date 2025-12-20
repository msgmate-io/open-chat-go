import { useMemo, useState } from "react";
import useSWR from "swr";
import { navigate } from "vike/client/router";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  LoadingSpinner,
  Text,
  TextTypes,
  cn,
} from "@open-chat-go/ui";
import { IntegrationPageShell } from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";

type IntegrationRow = {
  name: string;
  has_route_registrar: boolean;
  api_route_count: number;
  frontend_route_count: number;
  frontend_page_count: number;
  frontend_doc_count: number;
  model_provider_count: number;
  function_count: number;
  admin_only?: boolean;
  user_accessible?: boolean;
  labels?: string[];
};

type IntegrationsResponse = {
  rows: IntegrationRow[];
};

type FilterMode = "all" | "pages" | "docs" | "routes";

const MODE_LABELS: Record<FilterMode, string> = {
  all: "All",
  pages: "Pages",
  docs: "Docs",
  routes: "Routes",
};

const modeCount = (row: IntegrationRow, mode: FilterMode): number => {
  switch (mode) {
    case "pages":
      return row.frontend_page_count ?? 0;
    case "docs":
      return row.frontend_doc_count ?? 0;
    case "routes":
      return row.frontend_route_count ?? 0;
    default:
      return 0;
  }
};

export default function IntegrationsIndexPage() {
  const { data, isLoading, error } =
    useSWR<IntegrationsResponse>("/api/v1/integrations/list", fetcher);
  const [mode, setMode] = useState<FilterMode>("all");
  const [labelFilter, setLabelFilter] = useState<string>("all");

  const rows = useMemo(
    () => [...(data?.rows ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [data],
  );

  const labels = useMemo(() => {
    const set = new Set<string>();
    for (const row of rows) {
      for (const label of row.labels ?? []) {
        if (label.trim()) set.add(label);
      }
    }
    return Array.from(set).sort((a, b) => {
      const order = ["core", "full"];
      const ai = order.indexOf(a);
      const bi = order.indexOf(b);
      if (ai !== -1 || bi !== -1) {
        return (ai === -1 ? order.length : ai) - (bi === -1 ? order.length : bi);
      }
      return a.localeCompare(b);
    });
  }, [rows]);

  const modeTotals = useMemo(() => {
    const totals: Record<FilterMode, number> = { all: rows.length, pages: 0, docs: 0, routes: 0 };
    for (const row of rows) {
      totals.pages += modeCount(row, "pages");
      totals.docs += modeCount(row, "docs");
      totals.routes += modeCount(row, "routes");
    }
    return totals;
  }, [rows]);

  const visibleRows = useMemo(
    () =>
      rows.filter((row) => {
        if (mode !== "all" && modeCount(row, mode) === 0) {
          return false;
        }
        if (labelFilter !== "all" && !(row.labels ?? []).includes(labelFilter)) {
          return false;
        }
        return true;
      }),
    [rows, mode, labelFilter],
  );

  return (
    <IntegrationPageShell>
      <div className="space-y-1">
        <Text type={TextTypes.Heading6} tag="h1" bold>
          Integrations
        </Text>
        <Text type={TextTypes.Body7} color="muted">
          Browse the integrations compiled into this backend.
        </Text>
      </div>

      {!isLoading && !error && rows.length > 0 ? (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-1.5">
            {(Object.keys(MODE_LABELS) as FilterMode[]).map((key) => (
              <Button
                key={key}
                type="button"
                size="sm"
                variant={mode === key ? "default" : "outline"}
                onClick={() => setMode(key)}
              >
                {MODE_LABELS[key]}
                <span className="ml-1.5 text-xs opacity-70">{modeTotals[key]}</span>
              </Button>
            ))}
          </div>
          {labels.length > 0 ? (
            <div className="flex flex-wrap items-center gap-1.5">
              <button
                type="button"
                onClick={() => setLabelFilter("all")}
                className={cn(
                  "rounded-full border px-2.5 py-0.5 text-xs transition-colors",
                  labelFilter === "all"
                    ? "border-transparent bg-primary text-primary-foreground"
                    : "border-border/70 bg-card/90 text-muted-foreground hover:text-foreground",
                )}
              >
                All labels
              </button>
              {labels.map((label) => (
                <button
                  key={label}
                  type="button"
                  onClick={() => setLabelFilter(label)}
                  className={cn(
                    "rounded-full border px-2.5 py-0.5 text-xs capitalize transition-colors",
                    labelFilter === label
                      ? "border-transparent bg-primary text-primary-foreground"
                      : "border-border/70 bg-card/90 text-muted-foreground hover:text-foreground",
                  )}
                >
                  {label}
                </button>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {isLoading ? (
        <div className="flex h-24 items-center justify-center">
          <LoadingSpinner />
        </div>
      ) : error ? (
        <Card className="border-destructive/30 bg-destructive/5">
          <CardHeader>
            <CardTitle className="text-base">Failed to load integrations</CardTitle>
            <CardDescription>
              Could not fetch compiled integrations from the backend.
            </CardDescription>
          </CardHeader>
        </Card>
      ) : visibleRows.length > 0 ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {visibleRows.map((integration) => (
            <Card
              key={integration.name}
              className="cursor-pointer border-border/70 bg-card/90 transition-all hover:border-border hover:shadow-sm"
              onClick={() => navigate(`/integrations/${encodeURIComponent(integration.name)}/`)}
            >
              <CardHeader className="pb-2">
                <div className="flex items-start justify-between gap-2">
                  <CardTitle className="text-base">{integration.name}</CardTitle>
                  <div className="flex flex-wrap justify-end gap-1">
                    {(integration.labels ?? []).map((label) => (
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
                <CardDescription>Compiled integration module</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-wrap gap-2 pt-0">
                <Badge variant="outline">Pages: {integration.frontend_page_count ?? 0}</Badge>
                <Badge variant="outline">Docs: {integration.frontend_doc_count ?? 0}</Badge>
                <Badge variant="outline">Routes: {integration.frontend_route_count ?? 0}</Badge>
                <Badge variant="outline">API: {integration.api_route_count}</Badge>
                <Badge variant="outline">Models: {integration.model_provider_count}</Badge>
                <Badge variant="outline">Functions: {integration.function_count}</Badge>
                {integration.admin_only ? <Badge variant="outline">admin</Badge> : null}
              </CardContent>
            </Card>
          ))}
        </div>
      ) : (
        <Card className="border-dashed border-border/70 bg-muted/30">
          <CardHeader>
            <CardTitle className="text-base">No integrations available</CardTitle>
            <CardDescription>
              {rows.length > 0
                ? "No integrations match the selected filters."
                : "This backend build has no compiled integration modules."}
            </CardDescription>
          </CardHeader>
        </Card>
      )}
    </IntegrationPageShell>
  );
}
