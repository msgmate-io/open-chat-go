import { useMemo } from "react";
import useSWR from "swr";
import { navigate } from "vike/client/router";
import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  LoadingSpinner,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { IntegrationPageShell } from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";

type IntegrationRow = {
  name: string;
  has_route_registrar: boolean;
  api_route_count: number;
  model_provider_count: number;
  function_count: number;
  admin_only?: boolean;
};

type IntegrationsResponse = {
  rows: IntegrationRow[];
};

export default function IntegrationsIndexPage() {
  const { data, isLoading, error } =
    useSWR<IntegrationsResponse>("/api/v1/integrations/list", fetcher);

  const rows = useMemo(
    () => [...(data?.rows ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [data],
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
      ) : rows.length > 0 ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {rows.map((integration) => (
            <Card
              key={integration.name}
              className="cursor-pointer border-border/70 bg-card/90 transition-all hover:border-border hover:shadow-sm"
              onClick={() => navigate(`/integrations/${encodeURIComponent(integration.name)}/`)}
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
              This backend build has no compiled integration modules.
            </CardDescription>
          </CardHeader>
        </Card>
      )}
    </IntegrationPageShell>
  );
}
