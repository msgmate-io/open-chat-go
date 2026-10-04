import type { ChatSearchAction, ChatUIExtension, ChatUIMessageRenderer } from "./types";

const extensions = new Map<string, ChatUIExtension>();
const messageRenderers: ChatUIMessageRenderer[] = [];
const helpers = new Map<string, unknown>();
const searchActions = new Map<string, ChatSearchAction>();

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

export function registerChatUIHelper(name: string, helper: unknown): void {
  const key = name.trim().toLowerCase();
  if (key) {
    helpers.set(key, helper);
  }
}

export function resolveChatUIHelper(name: string): unknown | undefined {
  return helpers.get(name.trim().toLowerCase());
}
