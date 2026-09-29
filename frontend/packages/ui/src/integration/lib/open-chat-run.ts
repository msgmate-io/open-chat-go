export type OpenChatRunChatConfig = Record<string, unknown>;

// Keys that are owned by the bot runtime defaults (or handled separately via
// tool_init) and must not leak into the copied run command's config overrides.
const RUN_CONFIG_STRIP_KEYS = ["tools", "integrations", "mcp_tools", "system_prompt", "tool_init"];

export function sanitizeRunChatConfig(
  config: Record<string, unknown> | null | undefined,
): OpenChatRunChatConfig {
  const out: OpenChatRunChatConfig = {};
  for (const [key, value] of Object.entries(config ?? {})) {
    if (RUN_CONFIG_STRIP_KEYS.includes(key)) {
      continue;
    }
    if (value === undefined || value === null || value === "") {
      continue;
    }
    out[key] = value;
  }
  return out;
}

export function shellSingleQuote(value: string): string {
  return `'${value.replace(/'/g, "'\\''")}'`;
}

export type OpenChatRunCommandSpec = {
  botIdentifier: string;
  message: string;
  chatConfig?: OpenChatRunChatConfig;
  toolInit?: Record<string, unknown>;
};

export function buildOpenChatRunCommand(spec: OpenChatRunCommandSpec): string {
  const parts = ["open-chat run"];

  if (typeof window !== "undefined") {
    const hostname = window.location.hostname;
    if (hostname) {
      parts.push(`--host ${shellSingleQuote(hostname)}`);
    }
    const port = window.location.port;
    if (port) {
      parts.push(`--port ${shellSingleQuote(port)}`);
    }
  }

  parts.push(`--bot ${shellSingleQuote(spec.botIdentifier)}`);
  parts.push(`--message ${shellSingleQuote(spec.message)}`);

  const chatConfig: OpenChatRunChatConfig = { ...(spec.chatConfig ?? {}) };
  if (spec.toolInit && Object.keys(spec.toolInit).length > 0) {
    chatConfig.tool_init = spec.toolInit;
  }
  if (Object.keys(chatConfig).length > 0) {
    parts.push(`--chat-config ${shellSingleQuote(JSON.stringify(chatConfig))}`);
  }

  return parts.join(" ");
}

export type OpenChatRunChatLike = {
  partner?: { uuid?: string; name?: string } | null;
  config?: Record<string, unknown> | null;
};

/**
 * Builds an `open-chat run` command that re-runs an existing chat's bot
 * interaction with the chat's current shared config (model, backend,
 * chat_backend, endpoint, persisted tool_init, ...). Returns null when the
 * chat has no bot partner or the message is empty.
 */
export function buildChatRunCommand(
  chat: OpenChatRunChatLike | null | undefined,
  message: string,
): string | null {
  const botIdentifier = chat?.partner?.uuid ?? chat?.partner?.name ?? "";
  if (!botIdentifier || !message.trim()) {
    return null;
  }
  const config = chat?.config ?? {};
  const toolInitRaw = config["tool_init"];
  const toolInit =
    toolInitRaw && typeof toolInitRaw === "object" && !Array.isArray(toolInitRaw)
      ? (toolInitRaw as Record<string, unknown>)
      : undefined;
  return buildOpenChatRunCommand({
    botIdentifier,
    message: message.trim(),
    chatConfig: sanitizeRunChatConfig(config),
    toolInit,
  });
}
