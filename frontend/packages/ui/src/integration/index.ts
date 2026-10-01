// Integration-facing React surface.
//
// This module hosts the UI building blocks that integration-owned Vike pages
// (and the per-backend chat extensions) compile against. It is re-exported from
// the package root (`@open-chat-go/ui`) so integration repositories can import
// everything they need from a single, stable specifier without depending on the
// public `./frontend` application internals.
//
// Keep this file's exports additive: external integration repos depend on them.

// --- primitives / widgets -------------------------------------------------
export { DataTable } from "./components/DataTable";
export { DropdownSelect, type DropdownSelectItem } from "./components/DropdownSelect";
export { IntegrationPageShell } from "./components/IntegrationPageShell";
export { MsgmateTokenIcon } from "./components/MsgmateTokenIcon";
export { SshShellTerminal } from "./components/SshShellTerminal";
export { ToolInitFields } from "./components/ToolInitFields";

export { IntegrationOverviewView } from "./components/chat/IntegrationOverviewView";
export { AdminAccessGate } from "./components/admin/AdminAccessGate";
export { ModelsCatalogView } from "./components/models/ModelsCatalogView";

// --- chat core ------------------------------------------------------------
export { ChatBase, useSidePanelCollapse } from "./components/chat/ChatBase";
export { MessagesView, MessagesScroll } from "./components/chat/MessagesView";
export { VoiceChatPage } from "./components/chat/VoiceChatPage";
// `PendingMessageItem` and `ShinyText` are already exported from the base
// chat barrel; this module must not re-export them.
export {
  MessageItem,
  UserMessageItem,
  BotMessageItem,
  MessageMarkdown,
  MessageLink,
} from "./components/chat/MessageItem";
export { ConfirmableActionWidget } from "./components/chat/ConfirmableActionWidget";
export type { ConfirmableAction } from "./components/chat/ConfirmableActionWidget";
export { ProviderRetryEventWidget, resolveProviderRetryEvent, type ProviderRetryEvent } from "./components/chat/ProviderRetryEventWidget";
export { InteractionChatInfo } from "./components/chat/InteractionChatInfo";
export { MessageInputOptionsMenuItems } from "./components/chat/MessageInputOptionsButton";
export {
  usePartialMessageStore,
  type PartialMessage,
  type PartialMessageState,
} from "./components/chat/PartialMessages";
export { WebsocketHandler, WebsocketHandlerBase } from "./components/WebsocketHandler";

// --- chat-UI extension seam ----------------------------------------------
export {
  registerChatUIExtension,
  registerChatUIMessageRenderer,
  registerChatUIHelper,
  resolveChatUIExtension,
  resolveChatUIHelper,
} from "./components/chat/chat-ui/registry";
export type {
  ChatUIAttachment,
  ChatUIContact,
  ChatUIContext,
  ChatUIDetailsProps,
  ChatUIExtension,
  ChatUIInputProps,
  ChatUIMessageExtrasProps,
  ChatUIMessageRenderer,
  ChatUISendPayload,
  ChatUIToolCall,
} from "./components/chat/chat-ui/types";
export { ChatUIMessageExtras } from "./components/chat/ChatUIMessageExtras";

// --- layout / theme -------------------------------------------------------
export {
  ResizableTilingLayout,
  StaticDesktopLayout,
  readSavedDesktopLayout,
  useDesktopConfig,
  isMobileViewport,
} from "./components/ResizableTilingLayout";
export { ConnectedThemeSelector } from "./components/ConnectedThemeSelector";
export { useThemeStore } from "./components/ThemeToggle";

// --- lib / hooks ----------------------------------------------------------
// Whole-module re-exports for the utility modules: they have no cross-module
// name collisions and integration repos depend on their full surface.
export * from "./lib/utils";
export * from "./lib/page-metadata";
export * from "./hooks/use-current-user";
export * from "./hooks/use-terminal-zoom";
export * from "./lib/mobile-app";
export * from "./lib/tool-init-registry";
export * from "./lib/tool-init";

export * from "./lib/open-chat-run";
export * from "./lib/terminal-zoom";
export { default as logoUrl } from "./assets/logo.png?inline";