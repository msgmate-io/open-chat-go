import type {
  ChatSearchAction,
  ChatSidebarHeaderAction,
  ChatUIExtension,
  ChatUIExtensionMatchContext,
  ChatUIMessageRenderer,
} from "./types";

const extensions = new Map<string, ChatUIExtension>();
const messageRenderers: ChatUIMessageRenderer[] = [];
const helpers = new Map<string, unknown>();
const searchActions = new Map<string, ChatSearchAction>();
const sidebarHeaderActions = new Map<string, ChatSidebarHeaderAction>();

export function registerChatUIExtension(extension: ChatUIExtension): void {
  const key = (extension.chatBackend || "").trim().toLowerCase();
  if (!key) {
    return;
  }
  extensions.set(key, extension);
}

export function resolveChatUIExtension(chatBackend: string | null | undefined): ChatUIExtension | null {
  const key = (chatBackend || "").trim().toLowerCase();
  if (!key) {
    return null;
  }
  return extensions.get(key) ?? null;
}

/**
 * Resolves the chat UI extension for a chat. The keyed chatBackend lookup wins
 * when the chat declares one; otherwise the first extension whose `match`
 * predicate accepts the context is returned. This lets a tool-only integration
 * (whose chats never set a chat_backend key) still provide custom views.
 */
export function resolveChatUIExtensionForChat(
  ctx: ChatUIExtensionMatchContext | null | undefined
): ChatUIExtension | null {
  const keyed = resolveChatUIExtension(ctx?.chatBackend);
  if (keyed) {
    return keyed;
  }
  for (const extension of extensions.values()) {
    if (!extension.match) {
      continue;
    }
    try {
      if (extension.match(ctx ?? {})) {
        return extension;
      }
    } catch {
      // A throwing predicate must not break chat rendering.
    }
  }
  return null;
}

export function registerChatUIMessageRenderer(renderer: ChatUIMessageRenderer): void {
  messageRenderers.push(renderer);
}

export function listChatUIMessageRenderers(): ChatUIMessageRenderer[] {
  return [...messageRenderers];
}

export function registerChatSearchAction(action: ChatSearchAction): void {
  const key = (action.id || "").trim();
  if (!key || !action.Component) {
    return;
  }
  searchActions.set(key, { ...action, id: key });
}

export function listChatSearchActions(): ChatSearchAction[] {
  return [...searchActions.values()].sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
}

export function registerChatSidebarHeaderAction(action: ChatSidebarHeaderAction): void {
  const key = (action.id || "").trim();
  if (!key || !action.Component) {
    return;
  }
  sidebarHeaderActions.set(key, { ...action, id: key });
}

export function listChatSidebarHeaderActions(): ChatSidebarHeaderAction[] {
  return [...sidebarHeaderActions.values()].sort((a, b) => (a.order ?? 0) - (b.order ?? 0));
}

export function registerChatUIHelper(name: string, helper: unknown): void {
  const key = name.trim().toLowerCase();
  if (key) {
    helpers.set(key, helper);
  }
}

export function resolveChatUIHelper(name: string): unknown | undefined {
  return helpers.get(name.trim().toLowerCase());
}
