import React, { useEffect, useMemo, useState } from "react";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Text, TextTypes } from "@open-chat-go/ui";

type TaskItem = {
  id: string;
  type: string;
  queue: string;
  state: string;
  last_error?: string;
  completed_at?: string;
};

type TaskListResponse = {
  tasks: TaskItem[];
};

type TaskDetail = {
  id: string;
  type: string;
  queue: string;
  state: string;
  payload: string;
  result?: string;
  last_error?: string;
  completed_at?: string;
};

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

const fetchJSON = async <T,>(url: string): Promise<T> => {
  const response = await fetch(url, { credentials: "include" });
  if (!response.ok) throw new Error(`Failed request to ${url}: ${response.status}`);
  return response.json() as Promise<T>;
};

const pretty = (value?: string) => {
  if (!value) return "";
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
};

export function TaskResultsOverview() {
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [selected, setSelected] = useState<TaskDetail | null>(null);
  const [tableInfo, setTableInfo] = useState<TableInfo | null>(null);
  const [modelDoc, setModelDoc] = useState<TaggedDoc | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      setError(null);
      try {
        const [completed, retry, archived, table, taggedDoc] = await Promise.all([
          fetchJSON<TaskListResponse>("/api/v1/admin/asynq/queues/default/tasks?state=completed&page_size=100"),
          fetchJSON<TaskListResponse>("/api/v1/admin/asynq/queues/default/tasks?state=retry&page_size=100"),
          fetchJSON<TaskListResponse>("/api/v1/admin/asynq/queues/default/tasks?state=archived&page_size=100"),
          fetchJSON<TableInfo>("/api/v1/admin/table/task_results?full=1"),
          fetchJSON<TaggedDoc>("/api/v1/admin/docs/tag/open-chat-task-results-model"),
        ]);
        if (!cancelled) {
          const merged = [...(retry.tasks ?? []), ...(archived.tasks ?? []), ...(completed.tasks ?? [])];
          setTasks(merged);
          setSelectedID(merged[0]?.id ?? null);
          setTableInfo(table);
          setModelDoc(taggedDoc);
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load task results");
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!selectedID) {
      setSelected(null);
      return;
    }
    let cancelled = false;
    const load = async () => {
      try {
        const detail = await fetchJSON<TaskDetail>(`/api/v1/admin/asynq/queues/default/tasks/${selectedID}`);
        if (!cancelled) setSelected(detail);
      } catch {
        if (!cancelled) setSelected(null);
      }
    };
    load();
    return () => {
      cancelled = true;
    };
  }, [selectedID]);

  const summary = useMemo(() => {
    const completed = tasks.filter((t) => t.state === "completed").length;
    const failed = tasks.filter((t) => t.state === "retry" || t.state === "archived").length;
    return { total: tasks.length, completed, failed };
  }, [tasks]);

  if (loading) return <Text type={TextTypes.Body5}>Loading task results...</Text>;
  if (error) return <Text type={TextTypes.Body5} color="destructive">{error}</Text>;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="text-base">task_results model</CardTitle>
            {tableInfo?.source_url ? (
              <Button asChild variant="outline" size="sm" className="h-7 px-2 text-xs">
                <a href={tableInfo.source_url} target="_blank" rel="noreferrer">Source</a>
              </Button>
            ) : null}
          </div>
          <CardDescription>{tableInfo?.description ?? "Durable async task result model."}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-1 p-3 text-xs">
          {(tableInfo?.fields ?? []).map((field) => (
            <div key={field.name_raw}>
              <code>{field.name_raw}</code> : {field.type || "unknown"}
              {field.is_primary ? " [PK]" : ""}
              {!field.is_nullable ? " [NOT NULL]" : ""}
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="text-base">In-code model docs</CardTitle>
            {modelDoc?.source_url ? (
              <Button asChild variant="outline" size="sm" className="h-7 px-2 text-xs">
                <a href={modelDoc.source_url} target="_blank" rel="noreferrer">Source</a>
              </Button>
            ) : null}
          </div>
        </CardHeader>
        <CardContent className="p-3 text-sm text-muted-foreground">
          {modelDoc?.content ?? "No task results model docs found."}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b bg-muted/30">
          <CardTitle className="text-base">Result payload shape</CardTitle>
        </CardHeader>
        <CardContent className="p-3 text-xs leading-5">
          <div><code>success</code>: boolean</div>
          <div><code>result</code>: string (optional)</div>
          <div><code>error</code>: string (optional)</div>
        </CardContent>
      </Card>

      <div className="rounded-md border border-border bg-card px-3 py-2 text-xs text-muted-foreground">
        Snapshot overview: total {summary.total}, completed {summary.completed}, failed {summary.failed}
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="max-h-[520px] overflow-auto rounded-md border border-border bg-card">
          {tasks.length === 0 ? (
            <div className="px-3 py-3 text-sm text-muted-foreground">No task results found.</div>
          ) : (
            tasks.map((task) => (
              <button
                key={task.id}
                type="button"
                onClick={() => setSelectedID(task.id)}
                className={`block w-full border-t border-border px-3 py-2 text-left text-xs hover:bg-muted/30 ${selectedID === task.id ? "bg-muted/40" : ""}`}
              >
                <div className="font-medium">{task.type}</div>
                <div className="text-muted-foreground">{task.id}</div>
                <div className="text-muted-foreground">state: {task.state}</div>
              </button>
            ))
          )}
        </div>

        <div className="rounded-md border border-border bg-card px-3 py-3">
          {!selected ? (
            <div className="text-sm text-muted-foreground">Select a task to inspect result payload.</div>
          ) : (
            <div className="space-y-3 text-xs">
              <div>
                <div className="font-medium">{selected.type}</div>
                <div className="text-muted-foreground">{selected.id}</div>
              </div>
              <div>
                <div className="mb-1 font-medium">Payload</div>
                <pre className="overflow-auto rounded border border-border bg-muted/20 p-2">{pretty(selected.payload)}</pre>
              </div>
              <div>
                <div className="mb-1 font-medium">Result</div>
                <pre className="overflow-auto rounded border border-border bg-muted/20 p-2 whitespace-pre-wrap">{pretty(selected.result) || "(empty)"}</pre>
              </div>
              {selected.last_error ? (
                <div>
                  <div className="mb-1 font-medium text-destructive">Last Error</div>
                  <pre className="overflow-auto rounded border border-border bg-muted/20 p-2 whitespace-pre-wrap">{selected.last_error}</pre>
                </div>
              ) : null}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
