import React, { useEffect, useMemo, useState } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../card";
import { Badge } from "../badge";
import { Button } from "../button";
import { Text, TextTypes } from "../text";

type TableField = {
  name_raw: string;
  type: string;
  is_primary: boolean;
  is_nullable: boolean;
};

type TableInfo = {
  name: string;
  description?: string;
  source_url?: string;
  fields: TableField[];
};

type TaggedDoc = {
  tag: string;
  content: string;
  source_url: string;
};

const MODEL_TABLES = ["chats", "messages", "shared_chat_configs"];

const SHARED_CHAT_CONFIG_EXAMPLE = {
  bot_config: {
    provider: "openai",
    model: "gpt-4.1-mini",
    temperature: 0.2,
  },
  tools: [
    { name: "web_search", enabled: true },
    { name: "file_lookup", enabled: true },
  ],
  interaction: {
    mode: "assistant",
    allow_tool_calls: true,
    response_format: "markdown",
  },
};

const fetchJSON = async <T,>(url: string): Promise<T> => {
  const response = await fetch(url, { credentials: "include" });
  if (!response.ok) {
    throw new Error(`Failed request to ${url}: ${response.status}`);
  }
  return response.json() as Promise<T>;
};

function FieldPills({ field }: { field: TableField }) {
  return (
    <div className="flex items-center gap-1.5 text-[11px] leading-4 text-muted-foreground">
      {field.is_primary ? <Badge variant="secondary">PK</Badge> : null}
      {!field.is_nullable ? <Badge variant="outline">NOT NULL</Badge> : null}
    </div>
  );
}

export function ChatsInteractionsOverview() {
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [halDoc, setHalDoc] = useState<TaggedDoc | null>(null);

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      setLoading(true);
      setError(null);
      try {
        const [infos, halDocInfo] = await Promise.all([
          Promise.all(MODEL_TABLES.map((tableName) => fetchJSON<TableInfo>(`/api/v1/admin/table/${tableName}?full=1`))),
          fetchJSON<TaggedDoc>("/api/v1/admin/docs/tag/open-chat-hal-agent-logic"),
        ]);
        if (!cancelled) {
          setTables(infos);
          setHalDoc(halDocInfo);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load chat models");
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  const orderedTables = useMemo(() => {
    const order = new Map(MODEL_TABLES.map((name, index) => [name, index]));
    return [...tables].sort((a, b) => (order.get(a.name) ?? 999) - (order.get(b.name) ?? 999));
  }, [tables]);

  if (loading) {
    return <Text type={TextTypes.Body5}>Loading chats and interactions models...</Text>;
  }

  if (error) {
    return (
      <Text type={TextTypes.Body5} color="destructive">
        {error}
      </Text>
    );
  }

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {orderedTables.map((table) => (
          <Card key={table.name} className="overflow-hidden">
            <CardHeader className="space-y-2 border-b bg-muted/30">
              <div className="flex items-start justify-between gap-2">
                <CardTitle className="text-base">{table.name}</CardTitle>
                {table.source_url ? (
                  <Button asChild variant="outline" size="sm" className="h-7 px-2 text-xs">
                    <a href={table.source_url} target="_blank" rel="noreferrer">
                      Source
                    </a>
                  </Button>
                ) : null}
              </div>
              {table.description ? <CardDescription>{table.description}</CardDescription> : null}
            </CardHeader>
            <CardContent className="space-y-2 p-3">
              {table.fields.map((field) => (
                <div key={`${table.name}.${field.name_raw}`} className="rounded-md border bg-muted/20 p-2">
                  <div className="flex items-center justify-between gap-2">
                    <code className="text-xs font-medium">{field.name_raw}</code>
                    <code className="text-[11px] text-muted-foreground">{field.type || "unknown"}</code>
                  </div>
                  <FieldPills field={field} />
                </div>
              ))}
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Example shared_chat_configs.config_data JSON</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <pre className="overflow-auto px-3 py-3 text-xs leading-5">
            <code>{JSON.stringify(SHARED_CHAT_CONFIG_EXAMPLE, null, 2)}</code>
          </pre>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-start justify-between gap-2">
            <CardTitle className="text-base">Open-Chat HAL Agent Logic</CardTitle>
            {halDoc?.source_url ? (
              <Button asChild variant="outline" size="sm" className="h-7 px-2 text-xs">
                <a href={halDoc.source_url} target="_blank" rel="noreferrer">
                  Source
                </a>
              </Button>
            ) : null}
          </div>
        </CardHeader>
        <CardContent className="px-3 py-3">
          <Text type={TextTypes.Body6} color="muted">
            {halDoc?.content ?? "No HAL agent doc found."}
          </Text>
        </CardContent>
      </Card>
    </div>
  );
}
