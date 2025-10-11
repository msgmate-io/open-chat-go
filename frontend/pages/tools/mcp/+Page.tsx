import { useMemo, useState } from "react";
import useSWR from "swr";
import PlatformLayout from "@/components/PlatformLayout";
import { fetcher } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Text,
  TextTypes,
} from "@open-chat-go/ui";

type MCPIntegrationRow = {
  name: string;
  config: Record<string, unknown>;
  enabled: boolean;
  has_auth_data: boolean;
  created_at_unix: number;
  updated_at_unix: number;
};

type MCPIntegrationsResponse = {
  rows: MCPIntegrationRow[];
};

const defaultConfig = {
  transport: "http_streamable",
  url: "https://mcp.figma.com/mcp",
  request_timeout_seconds: 25,
};

export default function MCPIntegrationsPage() {
  const { data, error, isLoading, mutate } = useSWR<MCPIntegrationsResponse>("/api/v1/tools/mcp/integrations", fetcher);
  const [name, setName] = useState("figma");
  const [configText, setConfigText] = useState(JSON.stringify(defaultConfig, null, 2));
  const [authText, setAuthText] = useState(JSON.stringify({ bearer_token: "" }, null, 2));
  const [enabled, setEnabled] = useState(true);
  const [editingName, setEditingName] = useState<string | null>(null);
  const [status, setStatus] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const rows = useMemo(() => data?.rows ?? [], [data?.rows]);

  const resetForm = () => {
    setName("figma");
    setConfigText(JSON.stringify(defaultConfig, null, 2));
    setAuthText(JSON.stringify({ bearer_token: "" }, null, 2));
    setEnabled(true);
    setEditingName(null);
  };

  const parseJSON = (input: string, fieldName: string) => {
    const parsed = JSON.parse(input);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      throw new Error(`${fieldName} must be a JSON object`);
    }
    return parsed as Record<string, unknown>;
  };

  const save = async () => {
    setStatus("");
    setIsSubmitting(true);
    try {
      const payload = {
        name,
        config: parseJSON(configText, "config"),
        auth_data: parseJSON(authText, "auth_data"),
        enabled,
      };
      const targetName = encodeURIComponent((editingName || name).trim().toLowerCase());
      const response = await fetch(editingName ? `/api/v1/tools/mcp/integrations/${targetName}` : "/api/v1/tools/mcp/integrations", {
        method: editingName ? "PUT" : "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || `Request failed with status ${response.status}`);
      }
      await mutate();
      setStatus(editingName ? "Integration updated." : "Integration created.");
      resetForm();
    } catch (err) {
      setStatus(err instanceof Error ? err.message : "Failed to save integration.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const remove = async (integrationName: string) => {
    setStatus("");
    const response = await fetch(`/api/v1/tools/mcp/integrations/${encodeURIComponent(integrationName)}`, {
      method: "DELETE",
      credentials: "include",
    });
    if (!response.ok) {
      const message = await response.text();
      setStatus(message || "Failed to delete integration.");
      return;
    }
    await mutate();
    setStatus(`Deleted ${integrationName}.`);
  };

  const discover = async (integrationName: string) => {
    setStatus("");
    const response = await fetch(`/api/v1/tools/mcp/integrations/${encodeURIComponent(integrationName)}/discover`, {
      method: "POST",
      credentials: "include",
    });
    if (!response.ok) {
      const message = await response.text();
      setStatus(message || "Discovery failed.");
      return;
    }
    const payload = await response.json();
    setStatus(`Discovered ${payload.count ?? 0} tools for ${integrationName}.`);
  };

  return (
    <PlatformLayout descriptor="MCP Integrations" basePath="/tools" baseTitle="Tools" parent={{ title: "Tools", href: "/tools" }}>
      <div className="space-y-4">
        <div>
          <Text type={TextTypes.Heading5} tag="h1" bold>MCP Integrations</Text>
          <Text type={TextTypes.Body5} color="muted">Register external MCP servers and attach them via `integrations` in bot configs.</Text>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>{editingName ? `Edit ${editingName}` : "Add integration"}</CardTitle>
            <CardDescription>Use HTTPS MCP endpoints. Figma default: `https://mcp.figma.com/mcp`.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="integration name (slug)" />
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
              Enabled
            </label>
            <textarea className="h-44 w-full rounded-md border border-border bg-background p-3 font-mono text-xs" value={configText} onChange={(event) => setConfigText(event.target.value)} />
            <textarea className="h-40 w-full rounded-md border border-border bg-background p-3 font-mono text-xs" value={authText} onChange={(event) => setAuthText(event.target.value)} />
            <div className="flex items-center gap-2">
              <Button onClick={save} disabled={isSubmitting}>{isSubmitting ? "Saving..." : editingName ? "Update" : "Create"}</Button>
              <Button variant="outline" onClick={resetForm}>Reset</Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Registered integrations</CardTitle>
            <CardDescription>{rows.length} total</CardDescription>
          </CardHeader>
          <CardContent>
            {isLoading ? <div className="h-24 animate-pulse rounded bg-muted" /> : null}
            {error ? <Text type={TextTypes.Body6} color="destructive">Failed to load integrations.</Text> : null}
            {!isLoading && !error ? (
              <div className="space-y-3">
                {rows.length === 0 ? <Text type={TextTypes.Body6} color="muted">No MCP integrations registered yet.</Text> : null}
                {rows.map((row) => (
                  <div key={row.name} className="rounded-lg border border-border/70 bg-card/80 p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <Text type={TextTypes.Body5} bold>{row.name}</Text>
                        <Badge variant={row.enabled ? "secondary" : "outline"}>{row.enabled ? "Enabled" : "Disabled"}</Badge>
                        <Badge variant={row.has_auth_data ? "secondary" : "outline"}>{row.has_auth_data ? "Auth set" : "No auth"}</Badge>
                      </div>
                      <div className="flex items-center gap-2">
                        <Button size="sm" variant="outline" onClick={() => {
                          setEditingName(row.name);
                          setName(row.name);
                          setEnabled(row.enabled);
                          setConfigText(JSON.stringify(row.config ?? {}, null, 2));
                          setAuthText(JSON.stringify({ bearer_token: "" }, null, 2));
                        }}>Edit</Button>
                        <Button size="sm" variant="outline" onClick={() => discover(row.name)}>Discover</Button>
                        <Button size="sm" variant="outline" onClick={() => remove(row.name)}>Delete</Button>
                      </div>
                    </div>
                    <pre className="mt-2 max-h-56 overflow-auto rounded bg-muted/30 p-2 text-xs">{JSON.stringify(row.config ?? {}, null, 2)}</pre>
                  </div>
                ))}
              </div>
            ) : null}
          </CardContent>
        </Card>

        {status ? <Text type={TextTypes.Body6} color="muted">{status}</Text> : null}
      </div>
    </PlatformLayout>
  );
}
