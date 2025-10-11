"use client"

import { useEffect, useMemo, useState } from "react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import { CheckCircle2, Circle, EllipsisVertical, Plus } from "lucide-react";
import { Input } from "@/components/Input";
import {
  addMobileServer,
  backupMobileServerData,
  clearMobileServerOpencodeConfig,
  type MobileServerConfig,
  readMobileAppConfig,
  readMobileServerOpencodeConfig,
  readMobileServerLogs,
  removeMobileServer,
  restartMobileServer,
  resetMobileServerData,
  selectMobileServerOpencodeConfig,
  setActiveMobileServer,
  updateMobileServer,
} from "@open-chat-go/ui";

type Props = {
  showOnlyWhenAuthenticated?: boolean;
  onStatus?: (message: string) => void;
  className?: string;
};

type EnvRow = { key: string; value: string };

const localServerID = "local-server";
const hostedServerID = "msgmate-default";

function toEnvRows(runtimeEnv: Record<string, string>): EnvRow[] {
  const rows = Object.entries(runtimeEnv)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value }));
  return rows.length > 0 ? rows : [{ key: "", value: "" }];
}

function rowsToRuntimeEnv(rows: EnvRow[]): Record<string, string> {
  const env: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim().toUpperCase();
    if (!key) {
      continue;
    }
    env[key] = row.value.trim();
  }
  return env;
}

function formatServerMode(server: MobileServerConfig): string {
  if (!server.routeApiWsToUpstream) {
    return "Local mode";
  }
  return "Proxy /api + /ws";
}

export function MobileServerSelector({ showOnlyWhenAuthenticated = false, onStatus, className }: Props) {
  const [servers, setServers] = useState<MobileServerConfig[]>([]);
  const [activeServerId, setActiveServerId] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorTab, setEditorTab] = useState<"general" | "environment" | "logs">("general");
  const [editingServer, setEditingServer] = useState<MobileServerConfig | null>(null);
  const [label, setLabel] = useState("");
  const [url, setURL] = useState("");
  const [routeProxy, setRouteProxy] = useState(true);
  const [envRows, setEnvRows] = useState<EnvRow[]>([{ key: "", value: "" }]);
  const [logs, setLogs] = useState("(no logs loaded yet)");
  const [isSelectingConfig, setIsSelectingConfig] = useState(false);
  const [configPreviewOpen, setConfigPreviewOpen] = useState(false);
  const [configPreviewTitle, setConfigPreviewTitle] = useState("open-code.json");
  const [configPreviewContent, setConfigPreviewContent] = useState("");
  const [configPreviewTruncated, setConfigPreviewTruncated] = useState(false);
  const [resetConfirmOpen, setResetConfirmOpen] = useState(false);
  const [resetFinalConfirmOpen, setResetFinalConfirmOpen] = useState(false);

  const refresh = () => {
    const config = readMobileAppConfig();
    setServers(config.servers);
    setActiveServerId(config.activeServerId);
  };

  useEffect(() => {
    refresh();
  }, []);

  useEffect(() => {
    if (typeof window === "undefined" || !editorOpen || !editingServer) {
      return;
    }

    const refreshFromNative = () => {
      refresh();
      setIsSelectingConfig(false);
    };

    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        refreshFromNative();
      }
    };

    window.addEventListener("focus", refreshFromNative);
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.removeEventListener("focus", refreshFromNative);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [editorOpen, editingServer]);

  const activeServer = useMemo(
    () => servers.find((server) => server.id === activeServerId) || servers[0],
    [activeServerId, servers],
  );

  const hasAuthenticated = useMemo(
    () => servers.some((server) => server.isAuthenticated === true),
    [servers],
  );

  const openCreate = () => {
    setEditingServer(null);
    setLabel("");
    setURL("https://");
    setRouteProxy(true);
    setEnvRows([{ key: "", value: "" }]);
    setEditorTab("general");
    setLogs("(no logs loaded yet)");
    setEditorOpen(true);
  };

  const openEdit = (server: MobileServerConfig) => {
    setEditingServer(server);
    setLabel(server.label);
    setURL(server.upstreamUrl);
    setRouteProxy(server.routeApiWsToUpstream);
    setEnvRows(toEnvRows(server.runtimeEnv));
    setEditorTab("general");
    setResetConfirmOpen(false);
    setResetFinalConfirmOpen(false);
    const result = readMobileServerLogs(server.id, 400);
    if (result.ok) {
      setLogs(result.logs || "(no logs yet)");
    } else {
      setLogs(result.message || "Failed to load logs");
    }
    setEditorOpen(true);
  };

  const selectServer = (serverId: string) => {
    const result = setActiveMobileServer(serverId);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to switch server");
      return;
    }
    onStatus?.("Server switched");
    refresh();
  };

  const saveServer = () => {
    const runtimeEnv = rowsToRuntimeEnv(envRows);
    if (editingServer) {
      const result = updateMobileServer(editingServer.id, {
        label,
        url,
        routeApiWsToUpstream: routeProxy,
        runtimeEnv,
      });
      if (!result.ok) {
        onStatus?.(result.message || "Failed to update server");
        return;
      }
      onStatus?.("Server updated");
    } else {
      const result = addMobileServer(url, label, routeProxy, runtimeEnv);
      if (!result.ok) {
        onStatus?.(result.message || "Failed to add server");
        return;
      }
      onStatus?.("Server added");
    }
    setEditorOpen(false);
    refresh();
  };

  const removeEditingServer = () => {
    if (!editingServer) {
      return;
    }
    const result = removeMobileServer(editingServer.id);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to remove server");
      return;
    }
    onStatus?.("Server removed");
    setEditorOpen(false);
    refresh();
  };

  const requestResetServerData = () => {
    if (!editingServer) {
      return;
    }
    setResetConfirmOpen(true);
  };

  const confirmResetServerData = () => {
    if (!editingServer) {
      return;
    }
    const result = resetMobileServerData(editingServer.id);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to reset server data");
      return;
    }
    onStatus?.(result.message || "Server data reset");
    setResetConfirmOpen(false);
    setResetFinalConfirmOpen(false);
    setEditorOpen(false);
    refresh();
  };

  const backupEditingServerData = () => {
    if (!editingServer) {
      return;
    }
    const result = backupMobileServerData(editingServer.id);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to back up server data");
      return;
    }
    onStatus?.(result.message || "Backup ready in share sheet");
  };

  const viewEditingServerConfig = () => {
    if (!editingServer) {
      return;
    }
    const result = readMobileServerOpencodeConfig(editingServer.id);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to load config file");
      return;
    }
    setConfigPreviewTitle(result.fileName || "open-code.json");
    setConfigPreviewContent(result.content || "");
    setConfigPreviewTruncated(Boolean(result.truncated));
    setConfigPreviewOpen(true);
  };

  const restartEditingServer = () => {
    if (!editingServer) {
      return;
    }
    const result = restartMobileServer(editingServer.id);
    if (!result.ok) {
      onStatus?.(result.message || "Failed to restart server");
      return;
    }
    onStatus?.(result.message || "Server restarted");
    refresh();
  };

  if (servers.length === 0) {
    return null;
  }

  if (showOnlyWhenAuthenticated && !hasAuthenticated) {
    return null;
  }

  const isSystemServer = editingServer?.id === localServerID || editingServer?.id === hostedServerID;
  const currentEditingServer = editingServer
    ? servers.find((server) => server.id === editingServer.id) || editingServer
    : null;
  const hasSelectedOpencodeConfig = Boolean(currentEditingServer?.hasOpencodeConfig);
  const selectedOpencodeConfigName =
    currentEditingServer?.opencodeConfigName?.trim() || "open-code.json";

  return (
    <div className={className}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="w-full rounded-xl border border-border bg-background px-3 py-2 text-left hover:bg-muted/50"
          >
            <div className="flex items-center justify-between gap-2">
              <div className="min-w-0">
                <Text type={TextTypes.Body5}>{activeServer?.label || "Select server"}</Text>
                <Text type={TextTypes.Body6} color="muted" className="truncate">
                  {activeServer?.upstreamUrl}
                </Text>
              </div>
              {activeServer?.isAuthenticated ? (
                <span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-600">
                  <CheckCircle2 className="h-3 w-3" />
                  Auth
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
                  <Circle className="h-3 w-3" />
                  No auth
                </span>
              )}
            </div>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center" className="w-[min(92vw,560px)] rounded-xl p-2">
          <div className="space-y-2">
            {servers.map((server) => {
              const isActive = server.id === activeServerId;
              return (
                <div
                  key={server.id}
                  className={[
                    "flex items-center gap-2 rounded-lg border p-2 transition-colors",
                    isActive
                      ? "border-primary/70 bg-gradient-to-r from-primary/12 via-primary/8 to-transparent shadow-sm"
                      : "border-border bg-background",
                  ].join(" ")}
                >
                  <button
                    type="button"
                    className="min-w-0 flex-1 text-left"
                    onClick={() => selectServer(server.id)}
                  >
                    <div className="flex items-center gap-2">
                      <Text type={TextTypes.Body5}>{server.label}</Text>
                      {isActive && (
                        <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary">Active</span>
                      )}
                      {server.isAuthenticated ? (
                        <span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-600">
                          <CheckCircle2 className="h-3 w-3" />
                          Auth
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 rounded-full border border-border px-2 py-0.5 text-xs text-muted-foreground">
                          <Circle className="h-3 w-3" />
                          No auth
                        </span>
                      )}
                    </div>
                    <Text type={TextTypes.Body6} color="muted" className="truncate">
                      {server.upstreamUrl}
                    </Text>
                    <Text type={TextTypes.Body6} color="muted">
                      {formatServerMode(server)}
                    </Text>
                  </button>
                  <Button type="button" variant="outline" size="sm" onClick={() => openEdit(server)}>
                    <EllipsisVertical className="h-4 w-4" />
                  </Button>
                </div>
              );
            })}
            <Button type="button" variant="outline" className="w-full" onClick={openCreate}>
              <Plus className="mr-1 h-4 w-4" />
              Add new server
            </Button>
          </div>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={editorOpen} onOpenChange={setEditorOpen}>
        <DialogContent className="h-[min(86vh,760px)] w-[min(96vw,720px)] max-w-[720px] overflow-hidden p-0">
          <div className="flex h-full min-h-0 flex-col">
            <DialogHeader className="border-b border-border/70 px-5 pb-3 pt-5">
              <DialogTitle>{editingServer ? "Edit server" : "Add server"}</DialogTitle>
              <DialogDescription>
                Configure title, URL, runtime environment and logs for this server instance.
              </DialogDescription>
            </DialogHeader>

            <div className="flex min-h-0 flex-1 flex-col px-5 pb-4 pt-3">
              <div className="flex flex-wrap gap-1.5 rounded-full border border-border/70 bg-muted/30 p-1">
                <Button
                  type="button"
                  size="sm"
                  variant={editorTab === "general" ? undefined : "ghost"}
                  className="h-7 rounded-full px-3 text-xs"
                  onClick={() => setEditorTab("general")}
                >
                  General
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant={editorTab === "environment" ? undefined : "ghost"}
                  className="h-7 rounded-full px-3 text-xs"
                  onClick={() => setEditorTab("environment")}
                >
                  Environment
                </Button>
                {editingServer && (
                  <Button
                    type="button"
                    size="sm"
                    variant={editorTab === "logs" ? undefined : "ghost"}
                    className="h-7 rounded-full px-3 text-xs"
                    onClick={() => setEditorTab("logs")}
                  >
                    Logs
                  </Button>
                )}
              </div>

              <div className="mt-3 min-h-0 flex-1 overflow-y-auto pr-1">
                {editorTab === "general" && (
                  <div className="space-y-3">
                    <Input value={label} onChange={(event) => setLabel(event.target.value)} placeholder="Server title" className="border" />
                    <Input
                      value={url}
                      onChange={(event) => setURL(event.target.value)}
                      placeholder="https://msgmate.io"
                      className="border"
                      disabled={Boolean(isSystemServer)}
                    />
                    <label className="flex items-center gap-2 text-sm text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={routeProxy}
                        onChange={(event) => setRouteProxy(event.target.checked)}
                        disabled={Boolean(isSystemServer)}
                      />
                      Proxy `/api` and `/ws` to this upstream
                    </label>

                    {editingServer && (
                      <div className="space-y-2 rounded-xl border border-border/70 bg-muted/20 p-3">
                        <Text type={TextTypes.Body6} bold>
                          Server data tools
                        </Text>
                        <Text type={TextTypes.Body7} color="muted">
                          Create a backup zip for sharing, or permanently reset this server&apos;s database data.
                        </Text>
                        <div className="flex flex-wrap items-center gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-8 rounded-full px-3 text-xs"
                            onClick={restartEditingServer}
                          >
                            Restart server
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-8 rounded-full px-3 text-xs"
                            onClick={backupEditingServerData}
                          >
                            Backup server data
                          </Button>
                          <Button
                            type="button"
                            variant="destructive"
                            size="sm"
                            className="h-8 rounded-full px-3 text-xs"
                            onClick={requestResetServerData}
                          >
                            Reset server data
                          </Button>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {editorTab === "environment" && (
                  <div className="space-y-4">
                    <div className="space-y-2">
                      {envRows.map((row, index) => (
                        <div key={`server-env-${index}`} className="flex flex-wrap items-center gap-2 sm:flex-nowrap">
                          <Input
                            value={row.key}
                            onChange={(event) => {
                              setEnvRows((prev) => prev.map((item, i) => (i === index ? { ...item, key: event.target.value } : item)));
                            }}
                            placeholder="ENV_KEY"
                            className="border"
                          />
                          <Input
                            value={row.value}
                            onChange={(event) => {
                              setEnvRows((prev) => prev.map((item, i) => (i === index ? { ...item, value: event.target.value } : item)));
                            }}
                            placeholder="value"
                            className="border"
                          />
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-8 rounded-full px-3 text-xs"
                            onClick={() => {
                              setEnvRows((prev) => {
                                const next = prev.filter((_, i) => i !== index);
                                return next.length > 0 ? next : [{ key: "", value: "" }];
                              });
                            }}
                          >
                            Remove
                          </Button>
                        </div>
                      ))}
                      <Button type="button" variant="outline" size="sm" className="h-8 rounded-full px-3 text-xs" onClick={() => setEnvRows((prev) => [...prev, { key: "", value: "" }])}>
                        Add variable
                      </Button>
                    </div>

                    <div className="space-y-2 rounded-xl border border-border/70 bg-muted/20 p-3">
                      <Text type={TextTypes.Body6} bold>
                        OpenCode config file
                      </Text>
                      <Text type={TextTypes.Body7} color="muted">
                        Select an `open-code.json` file and the app will inject `OPENCODE_CONFIG_PATH` at runtime.
                      </Text>
                      {editingServer ? (
                        <>
                          <Text type={TextTypes.Body7} color="muted" className="break-all">
                            {hasSelectedOpencodeConfig ? `Selected: ${selectedOpencodeConfigName}` : "No file selected"}
                          </Text>
                          <div className="flex flex-wrap items-center gap-2">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="h-8 rounded-full px-3 text-xs"
                              disabled={isSelectingConfig}
                              onClick={() => {
                                const result = selectMobileServerOpencodeConfig(editingServer.id);
                                if (!result.ok) {
                                  onStatus?.(result.message || "Failed to open config picker");
                                  return;
                                }
                                setIsSelectingConfig(true);
                                onStatus?.(result.message || "Select a config file from the picker");
                              }}
                            >
                              {hasSelectedOpencodeConfig ? "Change file" : "Select file"}
                            </Button>
                            {hasSelectedOpencodeConfig && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="h-8 rounded-full px-3 text-xs"
                                onClick={viewEditingServerConfig}
                              >
                                View config
                              </Button>
                            )}
                            {hasSelectedOpencodeConfig && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="h-8 rounded-full px-3 text-xs"
                                onClick={() => {
                                  const result = clearMobileServerOpencodeConfig(editingServer.id);
                                  if (!result.ok) {
                                    onStatus?.(result.message || "Failed to clear config file");
                                    return;
                                  }
                                  onStatus?.(result.message || "Config file removed");
                                  refresh();
                                }}
                              >
                                Remove file
                              </Button>
                            )}
                          </div>
                        </>
                      ) : (
                        <Text type={TextTypes.Body7} color="muted">
                          Save the server first to attach a config file.
                        </Text>
                      )}
                    </div>
                  </div>
                )}

                {editorTab === "logs" && editingServer && (
                  <div className="space-y-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="h-8 rounded-full px-3 text-xs"
                      onClick={() => {
                        const result = readMobileServerLogs(editingServer.id, 400);
                        if (!result.ok) {
                          onStatus?.(result.message || "Failed to load server logs");
                          return;
                        }
                        setLogs(result.logs || "(no logs yet)");
                      }}
                    >
                      Refresh logs
                    </Button>
                    <textarea
                      readOnly
                      className="h-[360px] w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
                      value={logs}
                    />
                  </div>
                )}
              </div>

              <div className="mt-4 flex items-center justify-between gap-2 border-t border-border/70 pt-3">
                <div>
                  {editingServer && editingServer.id !== localServerID && editingServer.id !== hostedServerID && (
                    <Button type="button" variant="outline" size="sm" className="h-8 rounded-full px-3 text-xs" onClick={removeEditingServer}>
                      Remove server
                    </Button>
                  )}
                </div>
                <Button type="button" size="sm" className="h-8 rounded-full px-4" onClick={saveServer}>
                  {editingServer ? "Save changes" : "Create server"}
                </Button>
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={resetConfirmOpen} onOpenChange={setResetConfirmOpen}>
        <DialogContent className="w-[min(92vw,460px)] rounded-xl">
          <DialogHeader>
            <DialogTitle>Reset server data?</DialogTitle>
            <DialogDescription>
              This will permanently delete all local database data for this server. This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setResetConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              onClick={() => {
                setResetConfirmOpen(false);
                setResetFinalConfirmOpen(true);
              }}
            >
              Continue
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={resetFinalConfirmOpen} onOpenChange={setResetFinalConfirmOpen}>
        <DialogContent className="w-[min(92vw,460px)] rounded-xl">
          <DialogHeader>
            <DialogTitle>Final confirmation</DialogTitle>
            <DialogDescription>
              Last check: delete this server&apos;s local data now?
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setResetFinalConfirmOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" size="sm" onClick={confirmResetServerData}>
              Yes, delete data
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={configPreviewOpen} onOpenChange={setConfigPreviewOpen}>
        <DialogContent className="h-[min(86vh,760px)] w-[min(96vw,820px)] max-w-[820px] overflow-hidden p-0">
          <div className="flex h-full min-h-0 flex-col">
            <DialogHeader className="border-b border-border/70 px-5 pb-3 pt-5">
              <DialogTitle>{configPreviewTitle}</DialogTitle>
              <DialogDescription>
                Selected OpenCode config file preview.
              </DialogDescription>
            </DialogHeader>
            <div className="min-h-0 flex-1 px-5 pb-4 pt-3">
              <textarea
                readOnly
                className="h-full w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
                value={configPreviewContent}
              />
              {configPreviewTruncated && (
                <Text type={TextTypes.Body7} color="muted" className="mt-2">
                  Preview truncated to keep the app responsive.
                </Text>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
