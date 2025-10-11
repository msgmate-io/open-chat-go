import type { ChatUIExtension, ChatUIMessageRenderer } from "./types";

const extensions = new Map<string, ChatUIExtension>();
const messageRenderers: ChatUIMessageRenderer[] = [];
const helpers = new Map<string, unknown>();

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

export function registerChatUIHelper(name: string, helper: unknown): void {
  const key = name.trim().toLowerCase();
  if (key) {
    helpers.set(key, helper);
  }
}

export function resolveChatUIHelper(name: string): unknown | undefined {
  return helpers.get(name.trim().toLowerCase());
}
