export type MobileServerConfig = {
  id: string;
  label: string;
  upstreamUrl: string;
  routeApiWsToUpstream: boolean;
  runtimeEnv: Record<string, string>;
  isAuthenticated?: boolean;
  hasOpencodeConfig?: boolean;
  opencodeConfigName?: string;
  opencodeConfigUpdatedAtUnix?: number;
};

export type MobileAppConfig = {
  servers: MobileServerConfig[];
  activeServerId: string;
};

type MobileBridge = {
  isMobileApp?: () => boolean;
  isDeviceOnline?: () => boolean;
  getConfig?: () => string;
  applyConfig?: (upstreamUrl: string, routeApiWsToUpstream: boolean) => string;
  addServer?: (
    url: string,
    label: string,
    routeApiWsToUpstream: boolean,
    runtimeEnvJson: string,
  ) => string;
  setActiveServer?: (serverId: string) => string;
  setServerRuntimeEnv?: (serverId: string, runtimeEnvJson: string) => string;
  getServerLogs?: (serverId: string, maxLines: number) => string;
  removeServer?: (serverId: string) => string;
  selectServerOpencodeConfig?: (serverId: string) => string;
  clearServerOpencodeConfig?: (serverId: string) => string;
  updateServer?: (
    serverId: string,
    url: string,
    label: string,
    routeApiWsToUpstream: boolean,
    runtimeEnvJson: string,
  ) => string;
  restartServer?: (serverId: string) => string;
  getServerOpencodeConfig?: (serverId: string, maxChars: number) => string;
  resetServerData?: (serverId: string) => string;
  backupServerData?: (serverId: string) => string;
  getPendingJsonImports?: () => string;
  applyPendingJsonImportAsDefault?: (index: number) => string;
  registerPendingJsonImportAsServer?: (index: number) => string;
  clearPendingJsonImports?: () => string;
  getRuntimeErrorState?: () => string;
  clearRuntimeErrorState?: () => string;
  retryActiveServerStart?: () => string;
};

export type PendingMobileJsonImport = {
  index: number;
  fileName: string;
  previewContent: string;
  hostUrl: string;
  parseError?: string;
  wasPreviewTruncated: boolean;
  canSetAsDefault: boolean;
  canApply: boolean;
};

export type MobileRuntimeErrorState = {
  source: string;
  message: string;
  serverId: string;
  serverLabel: string;
  upstreamUrl: string;
  failingUrl: string;
  webErrorCode?: number;
  happenedAtUnixMs: number;
};

declare global {
  interface Window {
    OpenChatMobileBridge?: MobileBridge;
  }
}

const defaultConfig: MobileAppConfig = {
  servers: [
    {
      id: "local-server",
      label: "Local app server",
      upstreamUrl: "http://localhost:1984",
      routeApiWsToUpstream: false,
      runtimeEnv: {},
    },
    {
      id: "msgmate-default",
      label: "msgmate.io",
      upstreamUrl: "https://msgmate.io",
      routeApiWsToUpstream: true,
      runtimeEnv: {},
    },
  ],
  activeServerId: "msgmate-default",
};

export function isMobileAppRuntime(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  const userAgent = window.navigator?.userAgent ?? "";
  const isAndroidWebView = /Android/i.test(userAgent) && /\bwv\b/i.test(userAgent);
  if (!isAndroidWebView) {
    return false;
  }
  return Boolean(window.OpenChatMobileBridge?.isMobileApp?.());
}

export function isDeviceOnlineRuntime(): boolean {
  if (typeof window === "undefined") {
    return true;
  }

  const activeServer = getActiveMobileServerConfig();
  if (activeServer && !activeServer.routeApiWsToUpstream) {
    return true;
  }

  try {
    const mobileCheck = window.OpenChatMobileBridge?.isDeviceOnline;
    if (typeof mobileCheck === "function") {
      return Boolean(mobileCheck());
    }
  } catch {
  }

  return navigator.onLine;
}

export function getActiveMobileServerConfig(): MobileServerConfig | null {
  const config = readMobileAppConfig();
  return config.servers.find((server) => server.id === config.activeServerId) || config.servers[0] || null;
}

export function readMobileAppConfig(): MobileAppConfig {
  if (typeof window === "undefined") {
    return defaultConfig;
  }
  const raw = window.OpenChatMobileBridge?.getConfig?.();
  if (!raw) {
    return defaultConfig;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<MobileAppConfig>;
    const servers = Array.isArray(parsed.servers)
      ? parsed.servers
          .map((server) => {
            if (!server || typeof server !== "object") {
              return null;
            }
            const candidate = server as Partial<MobileServerConfig>;
            if (
              typeof candidate.id !== "string" ||
              typeof candidate.label !== "string" ||
              typeof candidate.upstreamUrl !== "string" ||
              typeof candidate.routeApiWsToUpstream !== "boolean"
            ) {
              return null;
            }
            return {
              id: candidate.id.trim(),
              label: candidate.label.trim() || candidate.upstreamUrl.trim(),
              upstreamUrl: candidate.upstreamUrl.trim(),
              routeApiWsToUpstream: candidate.routeApiWsToUpstream,
              runtimeEnv: parseRuntimeEnv((candidate as { runtimeEnv?: unknown }).runtimeEnv),
              isAuthenticated:
                typeof (candidate as { isAuthenticated?: unknown }).isAuthenticated === "boolean"
                  ? Boolean((candidate as { isAuthenticated?: unknown }).isAuthenticated)
                  : undefined,
              hasOpencodeConfig:
                typeof (candidate as { hasOpencodeConfig?: unknown }).hasOpencodeConfig === "boolean"
                  ? Boolean((candidate as { hasOpencodeConfig?: unknown }).hasOpencodeConfig)
                  : undefined,
              opencodeConfigName:
                typeof (candidate as { opencodeConfigName?: unknown }).opencodeConfigName === "string"
                  ? String((candidate as { opencodeConfigName?: string }).opencodeConfigName).trim()
                  : undefined,
              opencodeConfigUpdatedAtUnix:
                typeof (candidate as { opencodeConfigUpdatedAtUnix?: unknown }).opencodeConfigUpdatedAtUnix === "number"
                  ? Number((candidate as { opencodeConfigUpdatedAtUnix?: number }).opencodeConfigUpdatedAtUnix)
                  : undefined,
            };
          })
          .filter((server): server is MobileServerConfig => {
            return Boolean(server?.id && server.upstreamUrl);
          })
      : [];

    if (servers.length === 0) {
      return defaultConfig;
    }

    const activeServerId =
      typeof parsed.activeServerId === "string" &&
      servers.some((server) => server.id === parsed.activeServerId)
        ? parsed.activeServerId
        : servers[0].id;

    return {
      servers,
      activeServerId,
    };
  } catch {
    return defaultConfig;
  }
}

export function applyMobileAppConfig(config: MobileAppConfig): { ok: boolean; message?: string } {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const activeServer = config.servers.find((server) => server.id === config.activeServerId);
  if (!activeServer) {
    return { ok: false, message: "Active server not found" };
  }
  const raw = window.OpenChatMobileBridge?.applyConfig?.(
    activeServer.upstreamUrl,
    activeServer.routeApiWsToUpstream,
  );
  if (!raw) {
    return { ok: false, message: "Mobile bridge unavailable" };
  }
  try {
    const parsed = JSON.parse(raw) as { ok?: boolean; message?: string };
    return {
      ok: Boolean(parsed.ok),
      message: typeof parsed.message === "string" ? parsed.message : undefined,
    };
  } catch {
    return { ok: false, message: "Invalid mobile bridge response" };
  }
}

type BridgeResult = { ok: boolean; message?: string; serverId?: string };

function parseRuntimeEnv(value: unknown): Record<string, string> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }

  const entries = Object.entries(value as Record<string, unknown>)
    .map(([key, rawValue]) => {
      const normalizedKey = key.trim().toUpperCase();
      if (!normalizedKey) {
        return null;
      }
      return [normalizedKey, String(rawValue ?? "").trim()] as const;
    })
    .filter((entry): entry is readonly [string, string] => Boolean(entry));

  return Object.fromEntries(entries);
}

function parseBridgeResult(raw: string | undefined): BridgeResult {
  if (!raw) {
    return { ok: false, message: "Mobile bridge unavailable" };
  }
  try {
    const parsed = JSON.parse(raw) as { ok?: boolean; message?: string; serverId?: string };
    return {
      ok: Boolean(parsed.ok),
      message: typeof parsed.message === "string" ? parsed.message : undefined,
      serverId: typeof parsed.serverId === "string" ? parsed.serverId : undefined,
    };
  } catch {
    return { ok: false, message: "Invalid mobile bridge response" };
  }
}

export function addMobileServer(
  url: string,
  label: string,
  routeApiWsToUpstream: boolean,
  runtimeEnv: Record<string, string> = {},
): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.addServer?.(
    url,
    label,
    routeApiWsToUpstream,
    JSON.stringify(parseRuntimeEnv(runtimeEnv)),
  );
  return parseBridgeResult(raw);
}

export function setActiveMobileServer(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.setActiveServer?.(serverId);
  return parseBridgeResult(raw);
}

export function removeMobileServer(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.removeServer?.(serverId);
  return parseBridgeResult(raw);
}

export function selectMobileServerOpencodeConfig(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.selectServerOpencodeConfig?.(serverId);
  return parseBridgeResult(raw);
}

export function clearMobileServerOpencodeConfig(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.clearServerOpencodeConfig?.(serverId);
  return parseBridgeResult(raw);
}

export function setMobileServerRuntimeEnv(
  serverId: string,
  runtimeEnv: Record<string, string>,
): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.setServerRuntimeEnv?.(
    serverId,
    JSON.stringify(parseRuntimeEnv(runtimeEnv)),
  );
  return parseBridgeResult(raw);
}

export function updateMobileServer(
  serverId: string,
  payload: {
    url: string;
    label: string;
    routeApiWsToUpstream: boolean;
    runtimeEnv: Record<string, string>;
  },
): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.updateServer?.(
    serverId,
    payload.url,
    payload.label,
    payload.routeApiWsToUpstream,
    JSON.stringify(parseRuntimeEnv(payload.runtimeEnv)),
  );
  return parseBridgeResult(raw);
}

export function readMobileServerLogs(
  serverId: string,
  maxLines = 300,
): { ok: boolean; logs: string; active: boolean; message?: string } {
  if (typeof window === "undefined") {
    return { ok: false, logs: "", active: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.getServerLogs?.(serverId, maxLines);
  if (!raw) {
    return { ok: false, logs: "", active: false, message: "Mobile bridge unavailable" };
  }
  try {
    const parsed = JSON.parse(raw) as {
      ok?: boolean;
      logs?: string;
      active?: boolean;
      message?: string;
    };
    return {
      ok: Boolean(parsed.ok),
      logs: typeof parsed.logs === "string" ? parsed.logs : "",
      active: Boolean(parsed.active),
      message: typeof parsed.message === "string" ? parsed.message : undefined,
    };
  } catch {
    return { ok: false, logs: "", active: false, message: "Invalid mobile bridge response" };
  }
}

export function restartMobileServer(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.restartServer?.(serverId);
  return parseBridgeResult(raw);
}

export function resetMobileServerData(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.resetServerData?.(serverId);
  return parseBridgeResult(raw);
}

export function backupMobileServerData(serverId: string): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.backupServerData?.(serverId);
  return parseBridgeResult(raw);
}

export function readMobileServerOpencodeConfig(
  serverId: string,
  maxChars = 200000,
): { ok: boolean; content?: string; fileName?: string; truncated?: boolean; message?: string } {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.getServerOpencodeConfig?.(serverId, maxChars);
  if (!raw) {
    return { ok: false, message: "Mobile bridge unavailable" };
  }

  try {
    const parsed = JSON.parse(raw) as {
      ok?: boolean;
      content?: string;
      fileName?: string;
      truncated?: boolean;
      message?: string;
    };
    return {
      ok: Boolean(parsed.ok),
      content: typeof parsed.content === "string" ? parsed.content : undefined,
      fileName: typeof parsed.fileName === "string" ? parsed.fileName : undefined,
      truncated: typeof parsed.truncated === "boolean" ? parsed.truncated : undefined,
      message: typeof parsed.message === "string" ? parsed.message : undefined,
    };
  } catch {
    return { ok: false, message: "Invalid mobile bridge response" };
  }
}

export function readPendingMobileJsonImports(): {
  ok: boolean;
  items: PendingMobileJsonImport[];
  message?: string;
} {
  if (typeof window === "undefined") {
    return { ok: false, items: [], message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.getPendingJsonImports?.();
  if (!raw) {
    return { ok: false, items: [], message: "Mobile bridge unavailable" };
  }

  try {
    const parsed = JSON.parse(raw) as {
      ok?: boolean;
      message?: string;
      items?: Array<{
        index?: unknown;
        fileName?: unknown;
        previewContent?: unknown;
        hostUrl?: unknown;
        parseError?: unknown;
        wasPreviewTruncated?: unknown;
        canSetAsDefault?: unknown;
        canApply?: unknown;
      }>;
    };

    const items = Array.isArray(parsed.items)
      ? parsed.items
          .map((item) => {
            if (!item || typeof item !== "object") {
              return null;
            }
            const index = typeof item.index === "number" ? item.index : -1;
            const fileName = typeof item.fileName === "string" ? item.fileName.trim() : "";
            if (index < 0 || !fileName) {
              return null;
            }

            return {
              index,
              fileName,
              previewContent: typeof item.previewContent === "string" ? item.previewContent : "",
              hostUrl: typeof item.hostUrl === "string" ? item.hostUrl.trim() : "",
              parseError:
                typeof item.parseError === "string" && item.parseError.trim()
                  ? item.parseError.trim()
                  : undefined,
              wasPreviewTruncated: Boolean(item.wasPreviewTruncated),
              canSetAsDefault: Boolean(item.canSetAsDefault),
              canApply: Boolean(item.canApply),
            } satisfies PendingMobileJsonImport;
          })
          .filter((item): item is PendingMobileJsonImport => Boolean(item))
      : [];

    return {
      ok: Boolean(parsed.ok),
      items,
      message: typeof parsed.message === "string" ? parsed.message : undefined,
    };
  } catch {
    return { ok: false, items: [], message: "Invalid mobile bridge response" };
  }
}

export function applyPendingMobileJsonImportAsDefault(index: number): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.applyPendingJsonImportAsDefault?.(index);
  return parseBridgeResult(raw);
}

export function registerPendingMobileJsonImportAsServer(index: number): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.registerPendingJsonImportAsServer?.(index);
  return parseBridgeResult(raw);
}

export function clearPendingMobileJsonImports(): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.clearPendingJsonImports?.();
  return parseBridgeResult(raw);
}

export function readMobileRuntimeErrorState(): {
  ok: boolean;
  hasError: boolean;
  state: MobileRuntimeErrorState | null;
  message?: string;
} {
  if (typeof window === "undefined") {
    return { ok: false, hasError: false, state: null, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.getRuntimeErrorState?.();
  if (!raw) {
    return { ok: false, hasError: false, state: null, message: "Mobile bridge unavailable" };
  }

  try {
    const parsed = JSON.parse(raw) as {
      ok?: unknown;
      hasError?: unknown;
      source?: unknown;
      message?: unknown;
      serverId?: unknown;
      serverLabel?: unknown;
      upstreamUrl?: unknown;
      failingUrl?: unknown;
      webErrorCode?: unknown;
      happenedAtUnixMs?: unknown;
    };

    if (!parsed.hasError) {
      return { ok: Boolean(parsed.ok), hasError: false, state: null };
    }

    const source = typeof parsed.source === "string" ? parsed.source.trim() : "unknown";
    const message = typeof parsed.message === "string" ? parsed.message.trim() : "Runtime failed";
    const serverId = typeof parsed.serverId === "string" ? parsed.serverId.trim() : "";
    const serverLabel = typeof parsed.serverLabel === "string" ? parsed.serverLabel.trim() : "";
    const upstreamUrl = typeof parsed.upstreamUrl === "string" ? parsed.upstreamUrl.trim() : "";
    const failingUrl = typeof parsed.failingUrl === "string" ? parsed.failingUrl.trim() : "";
    const happenedAtUnixMs =
      typeof parsed.happenedAtUnixMs === "number" ? parsed.happenedAtUnixMs : Date.now();

    if (!serverId || !upstreamUrl) {
      return { ok: false, hasError: false, state: null, message: "Invalid runtime error state" };
    }

    return {
      ok: Boolean(parsed.ok),
      hasError: true,
      state: {
        source,
        message,
        serverId,
        serverLabel: serverLabel || upstreamUrl,
        upstreamUrl,
        failingUrl,
        webErrorCode: typeof parsed.webErrorCode === "number" ? parsed.webErrorCode : undefined,
        happenedAtUnixMs,
      },
    };
  } catch {
    return { ok: false, hasError: false, state: null, message: "Invalid mobile bridge response" };
  }
}

export function clearMobileRuntimeErrorState(): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.clearRuntimeErrorState?.();
  return parseBridgeResult(raw);
}

export function retryActiveMobileServerStart(): BridgeResult {
  if (typeof window === "undefined") {
    return { ok: false, message: "Unavailable in SSR" };
  }
  const raw = window.OpenChatMobileBridge?.retryActiveServerStart?.();
  return parseBridgeResult(raw);
}
