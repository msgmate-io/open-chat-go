import type { ComponentType } from "react";

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

export interface ChatUIExtension {
  /**
   * Discriminator value: the "chat_backend" key of the chat's shared config
   * (or of the selected model configuration on the start page). The "backend"
   * key keeps its meaning as the LLM provider and is not used here.
   */
  chatBackend: string;
  /** Custom chat details panel; replaces InteractionChatInfo when provided. */
  ChatDetails?: ComponentType<ChatUIDetailsProps>;
  /** Message input with two modes: before the chat started and after. */
  MessageInput?: {
    isPreStart: (ctx: ChatUIContext) => boolean;
    PreStart?: ComponentType<ChatUIInputProps>;
    Active?: ComponentType<ChatUIInputProps>;
  };
}
