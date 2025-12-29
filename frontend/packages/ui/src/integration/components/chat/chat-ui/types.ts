import type { ComponentType } from "react";
import type { ConfirmableAction } from "../ConfirmableActionWidget";

export interface ChatUIAttachment {
  file_id: string;
  display_name?: string;
}

export interface ChatUISendPayload {
  text: string;
  tool_init?: Record<string, unknown>;
  /** Controlled additions to the chat's shared config during creation. */
  shared_config?: Record<string, unknown>;
  attachments?: ChatUIAttachment[];
}

export interface ChatUIContact {
  contact_token?: string;
  name?: string;
  is_automated?: boolean;
  user_uuid?: string;
  profile_data?: Record<string, unknown>;
}

export interface ChatUIContext {
  // Existing chat (all null/empty on the start page)
  chatUUID: string | null;
  chat: any | null;
  user: any | null;
  messages: { rows: any[] } | null;
  status: any | null;
  isBotResponding: boolean;
  stopBotResponse: () => void;
  // Start page (all null/empty in the chat view)
  contact: ChatUIContact | null;
  selectedModel: string;
  selectedModelConfig: Record<string, unknown>;
  // Creates the chat (start page) or sends the next message (chat view).
  onSendMessage: (payload: ChatUISendPayload) => void;
  isLoading: boolean;
  navigateTo: (to: string) => void;
}

export interface ChatUIInputProps {
  ctx: ChatUIContext;
  text: string;
  setText: (text: string) => void;
}

export interface ChatUIDetailsProps {
  ctx: ChatUIContext;
  showMobileHeaderControls: boolean;
  leftPannelCollapsed: boolean;
  onToggleSidebar: () => void;
}

export interface ChatUIToolCall {
  id?: string;
  name?: string;
  arguments?: unknown;
  result?: unknown;
  status?: string;
  error?: string;
}

export interface ChatUIMessageExtrasProps {
  /** Raw tool calls of the finished message (private widgets parse these). */
  toolCalls: ChatUIToolCall[];
  /** Raw message meta_data; private widgets read their own keys. */
  meta: Record<string, unknown>;
  chatUUID?: string;
  /** UUID of the message the extras are rendered for (widgets persist to it). */
  messageUUID?: string;
  interactionState?: string | null;
  /** Invalidate chat data after widget side effects. */
  onMutate: () => void;
}

export type ChatUIMessageRenderer = ComponentType<ChatUIMessageExtrasProps>;

/**
 * Props passed to a chat-backend's custom assistant message body. It lets an
 * integration render the assistant text and its tool calls the way it wants
 * (for example inline, in the order the tools ran) instead of the core's single
 * summarised tool-call block. The core still renders the thoughts blocks and
 * the confirmable-action widgets around it.
 */
export interface ChatUIMessageBodyProps {
  /** UUID of the message being rendered. */
  messageUUID: string;
  /** Assistant text of this message (reasoning/think tags already stripped). */
  text: string;
  /** Tool calls of this message, in the order the model emitted them. */
  toolCalls: ChatUIToolCall[];
  /** Raw message meta_data; custom views read their own keys. */
  meta: Record<string, unknown>;
  /** True while the message is still streaming (not yet marked finished). */
  isGenerating: boolean;
  interactionState?: string | null;
  /**
   * UUID of the chat, for bodies that render confirmable-action widgets and
   * therefore call the confirm/reject API. Present for live chats; absent on
   * the read-only docs previews.
   */
  chatUUID?: string;
  /**
   * Confirmable actions attached to this message (the proposals the model
   * paused on, with their execution status and result). Supplied when the
   * extension declares `inlineConfirmableActions`, so the body can render them
   * inline with its tool calls.
   */
  confirmableActions?: ConfirmableAction[];
  /**
   * When set, the message is still syncing and the confirmable actions must not
   * be executed yet; pass it to the confirm widget's `executionBlockedReason`.
   */
  executionBlockedReason?: string;
  /** Invalidate chat data after a widget side effect (confirm/reject). */
  onMutate?: () => void;
}

export type ChatUIMessageBody = ComponentType<ChatUIMessageBodyProps>;

/**
 * Context a chat UI extension predicate is resolved against. It lets an
 * integration select its views for a chat that has no explicit `chat_backend`
 * key (eg a tool-only agent whose bots never set one), by matching on the chat
 * object and/or the tool calls of the message being rendered.
 */
export interface ChatUIExtensionMatchContext {
  /** The chat's chat_backend key (or legacy backend), if any. */
  chatBackend?: string;
  /** The full chat object (partner/config/status) when available. */
  chat?: any;
  /** Tool calls of the message being rendered (message-body matching only). */
  toolCalls?: ChatUIToolCall[];
}

/**
 * Props passed to a chat search action. Integrations register actions here to
 * surface a contextual button next to the chat sidebar search (e.g. "Ask chat
 * finder bot"). The action component decides its own visibility (for example by
 * checking whether its automation is enabled).
 */
export interface ChatSearchActionProps {
  /** Current, non-empty search query. */
  query: string;
  /** Navigate to an app route, e.g. a freshly created chat. */
  navigateTo: (to: string) => void;
}

/**
 * A chat sidebar search action contributed by an integration.
 */
export interface ChatSearchAction {
  /** Stable id used for React keys and de-duplication. */
  id: string;
  /** Ordering hint; lower values render first. */
  order?: number;
  Component: ComponentType<ChatSearchActionProps>;
}

export interface ChatUIExtension {
  /**
   * Discriminator value: the "chat_backend" key of the chat's shared config
   * (or of the selected model configuration on the start page). The "backend"
   * key keeps its meaning as the LLM provider and is not used here.
   */
  chatBackend: string;
  /**
   * Optional predicate that selects this extension for a chat even when it has
   * no `chat_backend` key (eg a tool-only agent whose bots never set one). The
   * keyed chatBackend lookup still wins when a chat has an explicit key.
   */
  match?: (ctx: ChatUIExtensionMatchContext) => boolean;
  /** Custom chat details panel; replaces InteractionChatInfo when provided. */
  ChatDetails?: ComponentType<ChatUIDetailsProps>;
  /**
   * Optional custom renderer for the assistant message body and its tool calls.
   * When provided it replaces the default summarised tool-call block and the
   * markdown body, so an integration can render tool calls inline, in order, as
   * they stream in. The thoughts blocks are still rendered by the core, and the
   * confirmable-action widgets too unless the extension sets
   * `inlineConfirmableActions`.
   */
  MessageBody?: ChatUIMessageBody;
  /**
   * When true, the extension's `MessageBody` owns the message's confirmable
   * actions (typically rendering the confirm/execute widget inline with its
   * tool calls). The core then suppresses its own confirmable-action widgets,
   * the "review and confirm" hint and the standalone
   * `confirmable_action_execute` event bubbles (requested/completed/failed/
   * rejected), so the confirmation is rendered completely in-line.
   */
  inlineConfirmableActions?: boolean;
  /** Message input with two modes: before the chat started and after. */
  MessageInput?: {
    isPreStart: (ctx: ChatUIContext) => boolean;
    PreStart?: ComponentType<ChatUIInputProps>;
    Active?: ComponentType<ChatUIInputProps>;
  };
}
