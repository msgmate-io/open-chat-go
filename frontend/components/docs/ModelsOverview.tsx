import React, { useEffect, useMemo, useState } from "react";
import { Button, Text, TextTypes } from "@open-chat-go/ui";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  type Edge,
  type NodeProps,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import staticModelsOverview from "./models-overview.static.json";
import { useCurrentUser } from "@open-chat-go/ui";
import { isMobileAppRuntime } from "@open-chat-go/ui";

type AdminTable = { name: string };

type TableField = {
  name: string;
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

type Relation = {
  fromTable: string;
  fromField: string;
  toTable: string;
  toField?: string;
};

type SchemaRelationResponse = {
  from_table: string;
  from_field: string;
  to_table: string;
  to_field: string;
};

type DiagramData = {
  table: TableInfo;
  activeSourceHandles: string[];
  activeTargetHandles: string[];
};

type StaticModelsOverview = {
  tables: TableInfo[];
  relations: Relation[];
  sql: string;
  generated_at?: string;
};

class HttpError extends Error {
  status: number;
  constructor(url: string, status: number) {
    super(`Failed request to ${url}: ${status}`);
    this.name = "HttpError";
    this.status = status;
  }
}

const HEADER_HEIGHT = 36;
const FIELD_ROW_HEIGHT = 20;
const EDGE_COLORS = ["#1d4ed8", "#b91c1c", "#047857", "#7c3aed", "#c2410c", "#0f766e", "#be123c"];

const hashNumber = (key: string) => {
  let hash = 0;
  for (let i = 0; i < key.length; i++) hash = (hash * 31 + key.charCodeAt(i)) >>> 0;
  return hash;
};

const pickModelColor = (modelName: string) => EDGE_COLORS[hashNumber(modelName) % EDGE_COLORS.length];

function TableNode({ data }: NodeProps<DiagramData>) {
  const table = data.table;
  const [showDescription, setShowDescription] = useState(false);
  const activeSourceHandles = new Set(data.activeSourceHandles ?? []);
  const activeTargetHandles = new Set(data.activeTargetHandles ?? []);
  const handleStyle = {
    opacity: 1,
    width: 7,
    height: 7,
    background: "var(--muted-foreground)",
    border: "1px solid var(--card)",
  } as const;

  const renderFieldHandles = (type: "source" | "target", side: "l" | "r") =>
    table.fields.map((field, index) => {
      const id = `${type === "source" ? "s" : "t"}-f-${field.name_raw}-${side}`;
      const isActive = type === "source" ? activeSourceHandles.has(id) : activeTargetHandles.has(id);
      if (!isActive) return null;
      return (
        <Handle
          key={id}
          type={type}
          id={id}
          position={side === "l" ? Position.Left : Position.Right}
          style={{ ...handleStyle, top: HEADER_HEIGHT + FIELD_ROW_HEIGHT * index + FIELD_ROW_HEIGHT / 2 }}
        />
      );
    });

  return (
    <div className="nodrag nopan pointer-events-auto relative rounded-md border border-border bg-card text-left text-foreground">
      {renderFieldHandles("target", "l")}
      {renderFieldHandles("target", "r")}
      {renderFieldHandles("source", "l")}
      {renderFieldHandles("source", "r")}

      <div className="pointer-events-auto flex items-center justify-between rounded-t-md border-b border-border bg-muted/40 px-3 py-2">
        <div className="text-left font-semibold">{table.name}</div>
        {table.description ? (
          <div className="flex items-center gap-1">
            {table.source_url ? (
              <a
                href={table.source_url}
                target="_blank"
                rel="noreferrer"
                onPointerDown={(event) => {
                  event.stopPropagation();
                }}
                onClick={(event) => {
                  event.stopPropagation();
                }}
                className="nodrag nopan pointer-events-auto rounded border border-border bg-card px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
              >
                Source
              </a>
            ) : null}
            <button
              type="button"
              onPointerDown={(event) => {
                event.stopPropagation();
              }}
              onClick={(event) => {
                event.stopPropagation();
                setShowDescription((prev) => !prev);
              }}
              className="nodrag nopan pointer-events-auto rounded border border-border bg-card px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
            >
              {showDescription ? "Fields" : "Info"}
            </button>
          </div>
        ) : null}
      </div>
      {showDescription && table.description ? (
        <div className="px-3 py-2 text-[11px] leading-5 text-muted-foreground">{table.description}</div>
      ) : (
        <div className="px-3 py-2 text-xs">
          {table.fields.map((field) => (
            <div key={`${table.name}.${field.name_raw}`} className="text-left leading-5">
              <code>{field.name_raw}</code>
              {` : ${field.type || "unknown"}`}
              {field.is_primary ? " [PK]" : ""}
              {!field.is_nullable ? " [NOT NULL]" : ""}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const fetchJSON = async <T,>(url: string): Promise<T> => {
  const response = await fetch(url, { credentials: "include" });
  if (!response.ok) {
    throw new HttpError(url, response.status);
  }
  return response.json() as Promise<T>;
};

const singularize = (value: string) => {
  if (value.endsWith("ies")) return `${value.slice(0, -3)}y`;
  if (value.endsWith("s")) return value.slice(0, -1);
  return value;
};

const toCanonical = (value: string) => value.replace(/_/g, "").toLowerCase();

const inferRelations = (tables: TableInfo[]): Relation[] => {
  const tableNames = new Set(tables.map((table) => table.name));
  const lookup = new Map<string, string>();
  for (const table of tables) {
    lookup.set(toCanonical(table.name), table.name);
    lookup.set(toCanonical(singularize(table.name)), table.name);
  }

  const relations: Relation[] = [];
  for (const table of tables) {
    for (const field of table.fields) {
      if (!field.name_raw.endsWith("_id") || field.is_primary) continue;
      const base = field.name_raw.slice(0, -3);
      const target =
        lookup.get(toCanonical(base)) ||
        lookup.get(toCanonical(`${base}s`)) ||
        lookup.get(toCanonical(`${base}es`));
      if (!target || !tableNames.has(target)) continue;
      relations.push({ fromTable: table.name, fromField: field.name_raw, toTable: target });
    }
  }

  return relations;
};

const buildDiagram = (tables: TableInfo[], relations: Relation[], hiddenTables?: Set<string>) => {
  const columns = 4;
  const cardWidth = 300;
  const colGap = 144;
  const rowGap = 84;

  const tableHeights = tables.map((table) => 72 + table.fields.length * 22);
  const rowHeights: number[] = [];
  for (let i = 0; i < tableHeights.length; i += columns) {
    const rowMax = Math.max(...tableHeights.slice(i, i + columns));
    rowHeights.push(rowMax);
  }

  const rowOffsets: number[] = [];
  let runningOffset = 0;
  for (let row = 0; row < rowHeights.length; row++) {
    rowOffsets[row] = runningOffset;
    runningOffset += rowHeights[row] + rowGap;
  }

  const nodes: Node<DiagramData>[] = tables.map((table, index) => {
    const col = index % columns;
    const row = Math.floor(index / columns);
    const height = tableHeights[index];

    return {
      id: table.name,
      type: "tableNode",
      position: {
        x: col * (cardWidth + colGap),
        y: rowOffsets[row],
      },
      style: {
        width: cardWidth,
        padding: 0,
        textAlign: "left",
        background: "transparent",
        border: "none",
      },
      draggable: false,
      selectable: false,
      hidden: hiddenTables?.has(table.name) ?? false,
      data: { table, activeSourceHandles: [], activeTargetHandles: [] },
    };
  });

  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const fieldSetByTable = new Map<string, Set<string>>(
    tables.map((table) => [table.name, new Set(table.fields.map((field) => field.name_raw))]),
  );
  const tableSet = new Set(tables.map((table) => table.name));
  const edges: Edge[] = relations
    .filter((relation) => tableSet.has(relation.fromTable) && tableSet.has(relation.toTable))
    .map((relation, index) => {
      const toField = relation.toField ?? "id";
      const fromFields = fieldSetByTable.get(relation.fromTable);
      const toFields = fieldSetByTable.get(relation.toTable);
      if (!fromFields || !toFields || !fromFields.has(relation.fromField) || !toFields.has(toField)) {
        return null;
      }

      const routeSeed = `${relation.fromTable}.${relation.fromField}->${relation.toTable}.${relation.toField ?? "id"}`;
      const color = pickModelColor(relation.toTable);
      const colorIndex = EDGE_COLORS.indexOf(color);
      const routeVariant = hashNumber(routeSeed) % 4;
      const offset = 24 + colorIndex * 7 + routeVariant * 5;

      const sourceNode = nodeById.get(relation.fromTable);
      const targetNode = nodeById.get(relation.toTable);
      if (!sourceNode || !targetNode) {
        return null;
      }
      const dx = (targetNode?.position.x ?? 0) - (sourceNode?.position.x ?? 0);
      const dy = (targetNode.position.y ?? 0) - (sourceNode.position.y ?? 0);

      const sourceSide = dx < 0 ? "l" : "r";
      const targetSide = dx < 0 ? "r" : "l";

      return {
        id: `e-${relation.fromTable}-${relation.fromField}-${index}`,
        source: relation.fromTable,
        target: relation.toTable,
        sourceHandle: `s-f-${relation.fromField}-${sourceSide}`,
        targetHandle: `t-f-${toField}-${targetSide}`,
        label: `${relation.fromTable}.${relation.fromField} -> ${relation.toTable}.${toField}`,
        type: "smoothstep",
        markerEnd: { type: MarkerType.ArrowClosed, color },
        style: { strokeWidth: 1.8, stroke: color, pointerEvents: "none" },
        interactionWidth: 0,
        pathOptions: { offset, borderRadius: 10 },
        labelShowBg: true,
        labelBgStyle: { fill: "var(--card)", opacity: 0.95, stroke: "var(--border)", strokeWidth: 1 },
        labelBgPadding: [6, 3],
        labelBgBorderRadius: 4,
        labelStyle: { fontSize: 11, fill: color, fontWeight: 600 },
      };
    })
    .filter((edge): edge is Edge => edge !== null);

  const activeHandles = new Map<string, { source: Set<string>; target: Set<string> }>();
  for (const node of nodes) {
    activeHandles.set(node.id, { source: new Set<string>(), target: new Set<string>() });
  }
  for (const edge of edges) {
    const sourceNodeHandles = activeHandles.get(edge.source);
    const targetNodeHandles = activeHandles.get(edge.target);
    if (sourceNodeHandles && edge.sourceHandle) sourceNodeHandles.source.add(edge.sourceHandle);
    if (targetNodeHandles && edge.targetHandle) targetNodeHandles.target.add(edge.targetHandle);
  }

  const nodesWithActiveHandles: Node<DiagramData>[] = nodes.map((node) => {
    const handles = activeHandles.get(node.id);
    return {
      ...node,
      data: {
        ...node.data,
        activeSourceHandles: handles ? Array.from(handles.source) : [],
        activeTargetHandles: handles ? Array.from(handles.target) : [],
      },
    };
  });

  return { nodes: nodesWithActiveHandles, edges };
};

export function ModelsOverview() {
  const { data: user } = useCurrentUser();
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [sqlSchema, setSQLSchema] = useState<string>("");
  const [schemaRelations, setSchemaRelations] = useState<Relation[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const [edgeModelFilter, setEdgeModelFilter] = useState<Record<string, boolean>>({});
  const [hideUnrelatedToModel, setHideUnrelatedToModel] = useState<string | null>(null);
  const [usingStaticFallback, setUsingStaticFallback] = useState(false);
  const [snapshotSyncState, setSnapshotSyncState] = useState<"unknown" | "in-sync" | "out-of-sync">("unknown");
  const [snapshotBaselineSignature, setSnapshotBaselineSignature] = useState<string>("");
  const [isMobileRuntime, setIsMobileRuntime] = useState(false);

  const isAdmin = user?.is_admin === true;

  useEffect(() => {
    setIsMobileRuntime(isMobileAppRuntime());
  }, []);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      setError(null);
      setUsingStaticFallback(false);
      if (user?.is_admin !== true) {
        const fallback = staticModelsOverview as StaticModelsOverview;
        if (!cancelled) {
          setTables((fallback.tables ?? []).sort((a, b) => a.name.localeCompare(b.name)));
          setSchemaRelations(fallback.relations ?? []);
          setSQLSchema(fallback.sql ?? "");
          setUsingStaticFallback(true);
          setLoading(false);
        }
        return;
      }
      try {
        const tableList = await fetchJSON<AdminTable[]>("/api/v1/admin/tables");
        const infos = await Promise.all(
          tableList.map((table) => fetchJSON<TableInfo>(`/api/v1/admin/table/${table.name}?full=1`)),
        );
        const sqlResponse = await fetchJSON<{ sql: string; relations?: SchemaRelationResponse[] }>("/api/v1/admin/schema/sql");
        const mappedRelations: Relation[] = (sqlResponse.relations ?? []).map((relation) => ({
          fromTable: relation.from_table,
          fromField: relation.from_field,
          toTable: relation.to_table,
          toField: relation.to_field,
        }));
        if (!cancelled) {
          setTables(infos.sort((a, b) => a.name.localeCompare(b.name)));
          setSQLSchema(sqlResponse.sql ?? "");
          setSchemaRelations(mappedRelations);
        }
      } catch (err) {
        if (!cancelled) {
          const isAuthError = err instanceof HttpError && (err.status === 401 || err.status === 403);
          if (isAuthError) {
            const fallback = staticModelsOverview as StaticModelsOverview;
            setTables((fallback.tables ?? []).sort((a, b) => a.name.localeCompare(b.name)));
            setSchemaRelations(fallback.relations ?? []);
            setSQLSchema(fallback.sql ?? "");
            setUsingStaticFallback(true);
          } else {
            setError(err instanceof Error ? err.message : "Failed to load model overview");
          }
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
  }, [user?.is_admin]);

  const relations = useMemo(() => {
    const fieldSetByTable = new Map<string, Set<string>>();
    for (const table of tables) {
      fieldSetByTable.set(
        table.name,
        new Set(table.fields.map((field) => field.name_raw)),
      );
    }

    const source = schemaRelations.length > 0 ? schemaRelations : inferRelations(tables);
    const seen = new Set<string>();
    const filtered = source.filter((relation) => {
      const fromFields = fieldSetByTable.get(relation.fromTable);
      const toFields = fieldSetByTable.get(relation.toTable);
      if (!fromFields || !toFields) return false;
      if (!fromFields.has(relation.fromField)) return false;
      if (relation.toField && !toFields.has(relation.toField)) return false;

      const key = `${relation.fromTable}.${relation.fromField}->${relation.toTable}.${relation.toField ?? "id"}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });

    if (filtered.length > 0) {
      return filtered;
    }

    return inferRelations(tables);
  }, [tables, schemaRelations]);

  const relationModels = useMemo(() => {
    const models = new Set<string>();
    for (const relation of relations) {
      models.add(relation.toTable);
    }
    return Array.from(models).sort((a, b) => a.localeCompare(b));
  }, [relations]);

  useEffect(() => {
    if (relationModels.length === 0) {
      setEdgeModelFilter({});
      setHideUnrelatedToModel(null);
      return;
    }

    setEdgeModelFilter((prev) => {
      const next: Record<string, boolean> = {};
      for (const model of relationModels) {
        next[model] = prev[model] ?? true;
      }
      return next;
    });
  }, [relationModels]);

  useEffect(() => {
    if (hideUnrelatedToModel && !relationModels.includes(hideUnrelatedToModel)) {
      setHideUnrelatedToModel(null);
    }
  }, [hideUnrelatedToModel, relationModels]);

  const filteredRelations = useMemo(() => {
    if (relationModels.length === 0) return relations;
    return relations.filter((relation) => edgeModelFilter[relation.toTable] !== false);
  }, [relations, relationModels, edgeModelFilter]);

  const focusedRelations = useMemo(() => {
    if (!hideUnrelatedToModel) return filteredRelations;
    return filteredRelations.filter((relation) => relation.toTable === hideUnrelatedToModel);
  }, [filteredRelations, hideUnrelatedToModel]);

  const hiddenTables = useMemo(() => {
    if (!hideUnrelatedToModel) return new Set<string>();
    const visible = new Set<string>([hideUnrelatedToModel]);
    for (const relation of focusedRelations) {
      visible.add(relation.fromTable);
      visible.add(relation.toTable);
    }
    return new Set(tables.map((table) => table.name).filter((tableName) => !visible.has(tableName)));
  }, [tables, focusedRelations, hideUnrelatedToModel]);

  const diagram = useMemo(() => {
    return buildDiagram(tables, focusedRelations, hiddenTables);
  }, [tables, focusedRelations, hiddenTables]);
  const nodeTypes = useMemo(() => ({ tableNode: TableNode }), []);

  const staticSnapshot = staticModelsOverview as StaticModelsOverview;
  const staticSignature = useMemo(
    () => JSON.stringify({
      tables: (staticSnapshot.tables ?? []).map((table) => table.name).sort(),
      relations: (staticSnapshot.relations ?? [])
        .map((relation) => `${relation.fromTable}.${relation.fromField}->${relation.toTable}.${relation.toField ?? "id"}`)
        .sort(),
    }),
    [staticSnapshot],
  );

  const liveSignature = useMemo(
    () => JSON.stringify({
      tables: tables.map((table) => table.name).sort(),
      relations: relations
        .map((relation) => `${relation.fromTable}.${relation.fromField}->${relation.toTable}.${relation.toField ?? "id"}`)
        .sort(),
    }),
    [tables, relations],
  );

  useEffect(() => {
    if (snapshotBaselineSignature === "") {
      setSnapshotBaselineSignature(staticSignature);
    }
  }, [snapshotBaselineSignature, staticSignature]);

  useEffect(() => {
    if (!isAdmin || usingStaticFallback || tables.length === 0) {
      setSnapshotSyncState("unknown");
      return;
    }
    const baseline = snapshotBaselineSignature || staticSignature;
    setSnapshotSyncState(liveSignature === baseline ? "in-sync" : "out-of-sync");
  }, [isAdmin, usingStaticFallback, tables.length, liveSignature, staticSignature, snapshotBaselineSignature]);

  useEffect(() => {
    const handler = (event: Event) => {
      const custom = event as CustomEvent<{ snapshotKey?: string }>;
      if (custom.detail?.snapshotKey === "models-overview") {
        setSnapshotBaselineSignature(liveSignature);
        setSnapshotSyncState("in-sync");
      }
    };
    window.addEventListener("docs-snapshot-refreshed", handler as EventListener);
    return () => window.removeEventListener("docs-snapshot-refreshed", handler as EventListener);
  }, [liveSignature]);

  const toggleModelFilter = (model: string) => {
    setEdgeModelFilter((prev) => ({
      ...prev,
      [model]: !(prev[model] ?? true),
    }));
  };

  const toggleHideUnrelated = (model: string) => {
    setHideUnrelatedToModel((prev) => (prev === model ? null : model));
  };

  const copySchema = async () => {
    try {
      await navigator.clipboard.writeText(sqlSchema);
      setCopyState("copied");
      setTimeout(() => setCopyState("idle"), 1500);
    } catch {
      setCopyState("failed");
      setTimeout(() => setCopyState("idle"), 1500);
    }
  };

  if (loading) {
    return <Text type={TextTypes.Body5}>Loading model overview...</Text>;
  }

  if (error) {
    return (
      <Text type={TextTypes.Body5} color="destructive">
        {error}
      </Text>
    );
  }

  return (
    <div className="space-y-3">
      {usingStaticFallback ? (
        <Text type={TextTypes.Body6} color="muted">
          Showing a static schema snapshot. Sign in as admin to view live model metadata and SQL.
        </Text>
      ) : null}
      {isAdmin && !usingStaticFallback && snapshotSyncState === "out-of-sync" ? (
        <Text type={TextTypes.Body6} color="destructive">
          Static fallback data is out of sync with live admin APIs.
        </Text>
      ) : null}
      {isAdmin && !usingStaticFallback && snapshotSyncState === "in-sync" ? (
        <Text type={TextTypes.Body6} color="muted">
          Static fallback data is in sync.
        </Text>
      ) : null}
      <div className="flex items-center justify-end gap-2">
        <Text type={TextTypes.Body6} color="muted">Need SQL for external tooling?</Text>
        <Button size="sm" variant="ghost" onClick={copySchema} disabled={!sqlSchema}>
          {copyState === "copied" ? "Copied" : copyState === "failed" ? "Copy failed" : "Copy SQLite schema"}
        </Button>
      </div>
      <div className="h-[560px] overflow-hidden rounded-md border border-border md:h-[760px]">
        <ReactFlowProvider>
          <div className="relative h-full w-full touch-pan-y">
            {relationModels.length > 0 ? (
              <div className="absolute right-3 top-3 z-10 max-h-[70%] w-64 overflow-auto rounded-md border border-border bg-card/95 p-3 shadow-sm backdrop-blur">
                <Text type={TextTypes.Body6} className="mb-2 font-semibold">
                  Foreign Key Model Filters
                </Text>
                <div className="mb-2 grid grid-cols-[1fr_auto] gap-x-2 px-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                  <span>Model</span>
                  <span>Hide unrelated</span>
                </div>
                <div className="space-y-2">
                  {relationModels.map((model) => {
                    const checked = edgeModelFilter[model] !== false;
                    const color = pickModelColor(model);
                    const hideUnrelatedChecked = hideUnrelatedToModel === model;
                    return (
                      <div key={model} className="grid grid-cols-[1fr_auto] items-center gap-x-2 text-xs">
                        <label className="flex cursor-pointer items-center gap-2">
                          <span
                            aria-hidden
                            className="inline-block h-2.5 w-2.5 rounded-full"
                            style={{ backgroundColor: color }}
                          />
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => toggleModelFilter(model)}
                            className="h-3.5 w-3.5"
                          />
                          <span className="truncate" title={model}>
                            {model}
                          </span>
                        </label>
                        <input
                          type="checkbox"
                          checked={hideUnrelatedChecked}
                          onChange={() => toggleHideUnrelated(model)}
                          className="h-3.5 w-3.5"
                          title={`Hide models unrelated to ${model}`}
                        />
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}

            <ReactFlow
              nodes={diagram.nodes}
              edges={diagram.edges}
              nodeTypes={nodeTypes}
              fitView
              minZoom={0.3}
              maxZoom={2}
              panOnDrag={!isMobileRuntime}
              zoomOnScroll={!isMobileRuntime}
              zoomOnPinch={!isMobileRuntime}
              preventScrolling={!isMobileRuntime}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable={false}
              proOptions={{ hideAttribution: true }}
            >
              <Background />
              <Controls showInteractive={false} />
            </ReactFlow>
          </div>
        </ReactFlowProvider>
      </div>
    </div>
  );
}
