import Markdown from "react-markdown";
import logoUrl from "../../assets/logo.png?inline";
import Prism from "prismjs";
import { useEffect, useMemo, useState } from "react";
import { cn } from "../../lib/utils";
import "prismjs/themes/prism-tomorrow.css";
import "prismjs/components/prism-python";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-json";
import React from "react";
import {
  BotMessageShell,
  Button,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  FileAttachmentsList,
  Icon,
  PendingMessageItem,
  ShinyText,
  UserMessageShell,
} from "@open-chat-go/ui";
import { ConfirmableActionWidget, type ConfirmableAction } from "./ConfirmableActionWidget";
import { ChatUIMessageExtras } from "./ChatUIMessageExtras";
import {
  ProviderRetryEventWidget,
  resolveProviderRetryEvent,
} from "./ProviderRetryEventWidget";
import { mutate } from "swr";
import { MoreHorizontal } from "lucide-react";

export { PendingMessageItem, ShinyText };

const CodeWrapper = ({ children }: { children: React.ReactNode }) => {
  return <div>{children}</div>;
};

const BootingIndicator = ({ label = "Msgmate Booting" }: { label?: string }) => {
  return (
    <ShinyText>
      <span className="inline-flex items-end gap-1">
        <span>{label}</span>
        <span className="inline-flex gap-0.5" aria-hidden="true">
          <span className="inline-block animate-bounce [animation-delay:0ms]">.</span>
          <span className="inline-block animate-bounce [animation-delay:150ms]">.</span>
          <span className="inline-block animate-bounce [animation-delay:300ms]">.</span>
        </span>
      </span>
    </ShinyText>
  )
}

const CodeBlock = ({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) => {
  const language = className?.replace("language-", "") || "text";
  const codeRef = React.useRef<HTMLElement>(null);

  useEffect(() => {
    if (typeof children === "string" && children.includes("\n") && codeRef.current) {
      try {
        Prism.highlightElement(codeRef.current);
      } catch (error) {
        console.error("Prism highlighting error:", error);
      }
    }
  }, [children, language]);

  const handleCopy = () => {
    const code = typeof children === "string" ? children : String(children);
    navigator.clipboard.writeText(code);
  };

  const isMultiLine = typeof children === "string" && children.includes("\n");

  return isMultiLine ? (
    <pre className="relative my-3 overflow-x-auto rounded-xl border border-border bg-muted/60 p-4 text-sm">
      <button
        onClick={handleCopy}
        className="absolute top-2 right-2 rounded p-1 text-xs hover:bg-secondary"
        title="Copy code"
        aria-label="Copy code"
      >
        <Icon name="clipboard" size="sm" />
      </button>
      <code
        ref={codeRef}
        className={cn(className, "block", isMultiLine && "whitespace-pre")}
        data-language={language}
      >
        {children}
      </code>
    </pre>
  ) : (
    <code data-language={language}>{children}</code>
  );
};

export function MessageLink({
  children,
  props,
}: {
  children: React.ReactNode;
  props: React.AnchorHTMLAttributes<HTMLAnchorElement>;
}) {
  return (
    <a className="font-medium text-primary underline-offset-4 hover:underline" {...props}>
      {children}
    </a>
  );
}

class MessageMarkdownErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean; error?: Error }
> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error("Markdown rendering error:", error);
    console.error("Error info:", errorInfo);
  }

  render() {
    if (this.state.hasError) {
      return <div>couldn't render: {this.state.error?.message}</div>;
    }

    return this.props.children;
  }
}

export function MessageMarkdown({ children }: { children: string | null | undefined }) {
  return (
    <MessageMarkdownErrorBoundary>
      <Markdown
        components={{
          blockquote: ({ children, ...props }) => (
            <blockquote {...props}>{children}</blockquote>
          ),
          h1: ({ children, ...props }) => <h1 {...props}>{children}</h1>,
          h2: ({ children, ...props }) => <h2 {...props}>{children}</h2>,
          h3: ({ children, ...props }) => <h3 {...props}>{children}</h3>,
          h4: ({ children, ...props }) => <h4 {...props}>{children}</h4>,
          h5: ({ children, ...props }) => <h5 {...props}>{children}</h5>,
          h6: ({ children, ...props }) => <h6 {...props}>{children}</h6>,
          strong: ({ children, ...props }) => <strong {...props}>{children}</strong>,
          a: ({ children, ...props }) => <MessageLink props={props}>{children}</MessageLink>,
          p: ({ children }) => <p>{children}</p>,
          code: ({ children, ...props }) => <CodeBlock {...props}>{children}</CodeBlock>,
          pre: ({ children, ...props }) => <CodeWrapper {...props}>{children}</CodeWrapper>,
        }}
      >
        {children || ""}
      </Markdown>
    </MessageMarkdownErrorBoundary>
  );
}

function mapAttachments(message: { meta_data?: { attachments?: Array<Record<string, unknown>> } }) {
  const rawAttachments = message?.meta_data?.attachments || [];
  return rawAttachments.map((att) => ({
    fileId: att.file_id as string,
    fileName: (att.file_name || att.display_name || "Unknown file") as string,
    displayName: att.display_name as string | undefined,
    mimeType: att.mime_type as string | undefined,
    size: att.file_size as number | undefined,
  }));
}

function wrapInBlockquote(children: React.ReactNode) {
  return <blockquote className="my-2 text-sm">{children}</blockquote>;
}

type MessageToolCall = {
  id?: string;
  name?: string;
  arguments?: unknown;
  result?: unknown;
  status?: string;
  error?: string;
};

type ToolCallStatus = "ongoing" | "succeeded" | "failed" | "pending_confirmation";

function resolveToolCallStatus(toolCall: MessageToolCall): ToolCallStatus {
  const rawStatus = typeof toolCall?.status === "string" ? toolCall.status.trim().toLowerCase() : "";
  if (rawStatus === "ongoing") return "ongoing";
  if (rawStatus === "succeeded") return "succeeded";
  if (rawStatus === "failed") return "failed";
  if (rawStatus === "pending_confirmation") return "pending_confirmation";

  const errorText = typeof toolCall?.error === "string" ? toolCall.error.trim() : "";
  if (errorText) {
    return "failed";
  }

  if (typeof toolCall?.result === "string") {
    const normalized = toolCall.result.trim().toLowerCase();
    if (normalized.includes(" failed with error:")) {
      return "failed";
    }
    if (normalized.includes("\"type\":\"confirm-action\"")) {
      return "pending_confirmation";
    }
    if (normalized.length > 0) {
      return "succeeded";
    }
  }

  if (hasRenderableValue(toolCall?.result)) {
    return "succeeded";
  }

  return "ongoing";
}

function toolStatusClasses(status: ToolCallStatus): string {
  if (status === "succeeded") {
    return "bg-emerald-500/10 text-emerald-700 border-emerald-500/30";
  }
  if (status === "failed") {
    return "bg-rose-500/10 text-rose-700 border-rose-500/30";
  }
  if (status === "pending_confirmation") {
    return "bg-amber-500/10 text-amber-700 border-amber-500/30";
  }
  return "bg-sky-500/10 text-sky-700 border-sky-500/30";
}

function toolStatusLabel(status: ToolCallStatus): string {
  if (status === "succeeded") return "Succeeded";
  if (status === "failed") return "Failed";
  if (status === "pending_confirmation") return "Needs confirmation";
  return "Ongoing";
}

function isCompletedToolStatus(status: ToolCallStatus): boolean {
  return status === "succeeded" || status === "failed";
}

function safeJSONStringify(value: unknown, pretty = false): string {
  try {
    return JSON.stringify(value, null, pretty ? 2 : 0);
  } catch {
    return String(value);
  }
}

function hasRenderableValue(value: unknown): boolean {
  if (value === undefined || value === null) {
    return false;
  }
  if (typeof value === "string") {
    return value.trim().length > 0;
  }
  if (Array.isArray(value)) {
    return value.length > 0;
  }
  if (typeof value === "object") {
    return Object.keys(value as Record<string, unknown>).length > 0;
  }
  return true;
}

function toolValuePreview(value: unknown): string {
  if (value === undefined || value === null) {
    return "";
  }
  if (typeof value === "string") {
    const compact = value.replace(/\s+/g, " ").trim();
    if (compact.length <= 72) {
      return compact;
    }
    return `${compact.slice(0, 72)}...`;
  }
  const compact = safeJSONStringify(value, false);
  if (compact.length <= 72) {
    return compact;
  }
  return `${compact.slice(0, 72)}...`;
}

function ToolCallLineSection({
  id,
  label,
  value,
}: {
  id: string;
  label: string;
  value: unknown;
}) {
  if (!hasRenderableValue(value)) {
    return null;
  }

  const preview = toolValuePreview(value);
  const pretty = typeof value === "string" ? value : safeJSONStringify(value, true);

  return (
    <Collapsible id={id}>
      <CollapsibleTrigger className="flex w-full items-center justify-between rounded-md border border-border/60 bg-muted/20 px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted/30">
        <span className="font-medium">{label}</span>
        <span className="ml-2 truncate text-right">{preview}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="CollapsibleContent transition-all duration-300">
        <pre className="scrollbar-hidden mt-1 max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border/60 bg-muted/30 p-2 text-xs leading-5">
          {pretty}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ToolCallsGroup({
  messageUUID,
  toolCalls,
  header,
  defaultOpen = false,
}: {
  messageUUID: string;
  toolCalls: MessageToolCall[];
  header: React.ReactNode;
  defaultOpen?: boolean;
}) {
  if (toolCalls.length === 0) {
    return null;
  }

  return (
    <Collapsible defaultOpen={defaultOpen} id={`tool-calls-${messageUUID}`}>
      <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">{header}</CollapsibleTrigger>
      <CollapsibleContent className="CollapsibleContent transition-all duration-300">
        <div className="space-y-2">
          {toolCalls.map((toolCall, index) => {
            const toolName = typeof toolCall?.name === "string" ? toolCall.name.trim() : "";
            const status = resolveToolCallStatus(toolCall);
            const cardId = `${messageUUID}-tool-call-${index}`;
            return (
              <Collapsible
                key={cardId}
                defaultOpen={!isCompletedToolStatus(status)}
                id={`tool-call-card-${cardId}`}
              >
                <div className="rounded-lg border border-border/70 bg-card/60 p-2">
                  <CollapsibleTrigger className="flex w-full items-start gap-2 text-left text-sm hover:opacity-90">
                    <span className="mt-0.5 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                      Tool
                    </span>
                    <code className="break-all text-xs">{toolName || "unknown_tool"}</code>
                    <span
                      className={cn(
                        "ml-auto rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
                        toolStatusClasses(status)
                      )}
                    >
                      {toolStatusLabel(status)}
                    </span>
                  </CollapsibleTrigger>
                  <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                    <div className="mt-2 space-y-1">
                      <ToolCallLineSection
                        id={`tool-call-params-${messageUUID}-${index}`}
                        label="Params"
                        value={toolCall?.arguments}
                      />
                      <ToolCallLineSection
                        id={`tool-call-result-${messageUUID}-${index}`}
                        label="Response"
                        value={toolCall?.result}
                      />
                      <ToolCallLineSection
                        id={`tool-call-error-${messageUUID}-${index}`}
                        label="Error"
                        value={toolCall?.error}
                      />
                    </div>
                  </CollapsibleContent>
                </div>
              </Collapsible>
            );
          })}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function parseSendAt(sendAt?: string): Date | null {
  if (!sendAt) return null;
  const parsed = new Date(sendAt);
  if (!Number.isNaN(parsed.getTime())) return parsed;
  const normalized = sendAt.replace(" UTC", "Z");
  const fallback = new Date(normalized);
  if (!Number.isNaN(fallback.getTime())) return fallback;
  return null;
}

function formatRelativeTime(sendAt?: string): string | undefined {
  const date = parseSendAt(sendAt);
  if (!date) return undefined;
  const diffSeconds = Math.round((date.getTime() - Date.now()) / 1000);
  const abs = Math.abs(diffSeconds);
  if (abs < 10) return "now";
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (abs < 60) return rtf.format(diffSeconds, "second");
  if (abs < 3600) return rtf.format(Math.round(diffSeconds / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diffSeconds / 3600), "hour");
  return rtf.format(Math.round(diffSeconds / 86400), "day");
}

function extractThoughtsFromThinkTags(text?: string): { cleanText: string; thoughts: string[] } {
  if (!text) {
    return { cleanText: "", thoughts: [] };
  }

  const thinkStart = "<think>";
  const thinkEnd = "</think>";
  const thoughts: string[] = [];
  let cleanBuilder = "";
  let cursor = 0;
  let isThinking = false;
  let currentThought = "";

  while (cursor < text.length) {
    if (!isThinking) {
      const startIdx = text.indexOf(thinkStart, cursor);
      if (startIdx === -1) {
        cleanBuilder += text.slice(cursor);
        break;
      }

      cleanBuilder += text.slice(cursor, startIdx);
      cursor = startIdx + thinkStart.length;
      isThinking = true;
      currentThought = "";
      continue;
    }

    const endIdx = text.indexOf(thinkEnd, cursor);
    if (endIdx === -1) {
      // In-progress streaming thought without a closing tag yet.
      currentThought += text.slice(cursor);
      cursor = text.length;
      break;
    }

    currentThought += text.slice(cursor, endIdx);
    const normalized = currentThought.trim();
    if (normalized.length > 0) {
      thoughts.push(normalized);
    }
    currentThought = "";
    cursor = endIdx + thinkEnd.length;
    isThinking = false;
  }

  if (isThinking) {
    const normalized = currentThought.trim();
    if (normalized.length > 0) {
      thoughts.push(normalized);
    }
  }

  if (thoughts.length === 0 && cleanBuilder.includes(thinkEnd) && !cleanBuilder.includes(thinkStart)) {
    const orphanParts = cleanBuilder.split(thinkEnd);
    if (orphanParts.length > 1) {
      const orphanThought = (orphanParts.shift() || "").trim();
      if (orphanThought.length > 0) {
        thoughts.push(orphanThought);
      }
      cleanBuilder = orphanParts.join(" ");
    }
  }

  const cleanText = cleanBuilder
    .replace(/<think>/gi, "")
    .replace(/<\/think>/gi, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();

  return { cleanText, thoughts };
}

function ConfirmableEventIndicator({
  message,
}: {
  message: { text?: string; meta_data?: Record<string, unknown> };
}) {
  const event = message.meta_data?.confirmable_action_execution as Record<string, unknown> | undefined;
  const phase = (message.meta_data?.event_phase as string | undefined) || "completed";
  const targetTool = String(event?.target_tool_name || "tool");
  const action = phase === "requested" ? "Execution requested" : phase === "failed" ? "Execution failed" : "Execution completed";
  const text = typeof message.text === "string" ? message.text.trim() : "";
  const looksJSON = (text.startsWith("{") && text.endsWith("}")) || (text.startsWith("[") && text.endsWith("]"));
  return (
    <div className="my-2 rounded-md border bg-muted/30 px-3 py-2 text-xs">
      <span className="font-medium">{action}:</span> `{targetTool}`
      {text ? (
        looksJSON ? (
          <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border/60 bg-muted/20 p-2">
            {text}
          </pre>
        ) : (
          <div className="mt-1 whitespace-pre-wrap break-all">{text}</div>
        )
      ) : null}
    </div>
  );
}

function asObject(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function ToolInitViewer({
  message,
}: {
  message: { meta_data?: Record<string, unknown> };
}) {
  const [isOpen, setIsOpen] = React.useState(false);
  const update = asObject(message?.meta_data?.tool_init_update);
  const effective = asObject(message?.meta_data?.tool_init_effective);
  const hasData = Boolean(update || effective);

  if (!hasData) {
    return null;
  }

  return (
    <>
      <div className="mt-2 flex justify-end">
        <button
          type="button"
          className="text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
          onClick={() => setIsOpen(true)}
        >
          Tool init
        </button>
      </div>
      <Dialog open={isOpen} onOpenChange={setIsOpen}>
        <DialogContent className="max-h-[80vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Tool init data on this message</DialogTitle>
          </DialogHeader>
          {update ? (
            <div className="mb-3">
              <p className="mb-1 text-xs font-medium text-muted-foreground">Update sent with this message</p>
              <pre className="max-h-60 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
                {JSON.stringify(update, null, 2)}
              </pre>
            </div>
          ) : null}
          {effective ? (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">Effective tool init after this message</p>
              <pre className="max-h-60 overflow-auto rounded-lg border border-border/60 bg-muted/30 p-3 text-xs leading-5">
                {JSON.stringify(effective, null, 2)}
              </pre>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}

function BotMessageActionsMenu({
  onCopy,
  onRegenerate,
  canCopy,
  canRegenerate,
}: {
  onCopy?: () => void;
  onRegenerate?: () => void | Promise<void>;
  canCopy: boolean;
  canRegenerate: boolean;
}) {
  if (!canCopy && !canRegenerate) {
    return null;
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-6 rounded-full text-muted-foreground hover:text-foreground"
          aria-label="Message actions"
        >
          <MoreHorizontal className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="center" side="right" className="w-44">
        <DropdownMenuItem onClick={onCopy} disabled={!canCopy}>Copy message</DropdownMenuItem>
        {canRegenerate ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => void onRegenerate?.()}>Regenerate response</DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function UserMessageItem({
  message,
  chat,
  selfIsSender = false,
}: {
  message: {
    uuid: string;
    text?: string;
    send_at?: string;
    meta_data?: {
      attachments?: Array<Record<string, unknown>>;
      event_type?: string;
      event_phase?: string;
      confirmable_action_execution?: Record<string, unknown>;
    };
  };
  chat: { partner?: { is_bot?: boolean; name?: string } };
  selfIsSender?: boolean;
}) {
  const isBotChat = chat?.partner?.is_bot;
  const attachments = mapAttachments(message);
  const senderLabel = selfIsSender ? "You" : `${chat?.partner?.name ?? "User"}`;
  const avatarVariant = selfIsSender ? "self" : isBotChat ? "bot" : "user";
  const senderMeta = useMemo(() => formatRelativeTime(message.send_at), [message.send_at]);
  const isConfirmableEvent = message.meta_data?.event_type === "confirmable_action_execute";
  const providerRetryEvent = resolveProviderRetryEvent(message.meta_data as Record<string, unknown> | undefined);

  return (
    <UserMessageShell
      key={message.uuid}
      senderLabel={senderLabel}
      senderMeta={senderMeta}
      avatarLabel={senderLabel}
      avatarVariant={avatarVariant}
    >
      {providerRetryEvent ? (
        <ProviderRetryEventWidget event={providerRetryEvent} />
      ) : isConfirmableEvent ? (
        <ConfirmableEventIndicator message={message} />
      ) : (
        <MessageMarkdown>{message?.text}</MessageMarkdown>
      )}
      <FileAttachmentsList attachments={attachments} />
      <ToolInitViewer message={message as { meta_data?: Record<string, unknown> }} />
    </UserMessageShell>
  );
}

export function BotMessageItem({
  message,
  chat,
  chatUUID,
  rerunSourceMessageUUID,
  selfIsSender = false,
  showToolbar = true,
  showBotIdentity = true,
  interactionState,
}: {
  message: {
    uuid: string;
    text?: string;
    send_at?: string;
    thoughts?: string[];
    tool_calls?: Array<{ name: string; arguments: unknown; result?: unknown }>;
    is_generating?: boolean;
    meta_data?: {
      finished?: boolean;
      cancelled?: boolean;
      queue_state?: string;
      thinking_time?: string;
      thinking_steps?: Array<{ text?: string; duration?: string }>;
      total_time?: string | number;
      attachments?: Array<Record<string, unknown>>;
      token_usage?: {
        prompt_tokens?: number;
        completion_tokens?: number;
        total_tokens?: number;
      };
      confirmable_actions?: ConfirmableAction[];
      event_type?: string;
      event_phase?: string;
      confirmable_action_execution?: Record<string, unknown>;
      backend?: string;
    };
  };
  chat: { partner?: { name?: string } };
  chatUUID?: string;
  rerunSourceMessageUUID?: string;
  selfIsSender?: boolean;
  showToolbar?: boolean;
  showBotIdentity?: boolean;
  interactionState?: string | null;
}) {
  const metaFinished = message?.meta_data?.finished;
  const attachments = mapAttachments(message);
  const senderMeta = useMemo(() => formatRelativeTime(message.send_at), [message.send_at]);
  const isConfirmableEvent = message.meta_data?.event_type === "confirmable_action_execute";
  const providerRetryEvent = resolveProviderRetryEvent(message.meta_data as Record<string, unknown> | undefined);
  const thinkTagExtraction = useMemo(() => extractThoughtsFromThinkTags(message.text), [message.text]);
  const normalizedMessageText = thinkTagExtraction.thoughts.length > 0 ? thinkTagExtraction.cleanText : (message.text || "");

  const handleCopy = () => {
    if (normalizedMessageText) {
      navigator.clipboard.writeText(normalizedMessageText);
    }
  };

  const handleRegenerate = async () => {
    if (!chatUUID || !rerunSourceMessageUUID) {
      return;
    }

    try {
      const response = await fetch(`/api/v1/chats/${chatUUID}/messages/${rerunSourceMessageUUID}/rerun`, {
        method: "POST",
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || "Failed to rerun response");
      }

      await Promise.all([
        mutate(`/api/v1/chats/${chatUUID}/messages/list`),
        mutate(`/api/v1/chats/list`),
        mutate(`/api/v1/chats/${chatUUID}`),
      ]);
    } catch (error) {
      console.error("Failed to rerun message:", error);
    }
  };

  if (selfIsSender) {
    return (
      <UserMessageShell key={message.uuid} senderLabel="You" align="end">
        {isConfirmableEvent ? <ConfirmableEventIndicator message={message} /> : <MessageMarkdown>{normalizedMessageText}</MessageMarkdown>}
        <FileAttachmentsList attachments={attachments} />
        <ToolInitViewer message={message as { meta_data?: Record<string, unknown> }} />
      </UserMessageShell>
    );
  }

  const fallbackConfirmableActions: ConfirmableAction[] = (message.tool_calls || [])
    .filter((toolCall: any) => toolCall?.requires_confirmation && toolCall?.confirmation)
    .map((toolCall: any) => ({
      action_id: toolCall?.id,
      target_tool_name: toolCall?.confirmation?.target_tool_name || toolCall?.name,
      input: toolCall?.confirmation?.suggested_inputs || {},
      status: "pending",
      title: toolCall?.confirmation?.title,
      description: toolCall?.confirmation?.description,
      confirm_label: toolCall?.confirmation?.confirm_label,
      danger_level: toolCall?.confirmation?.danger_level,
      continue_after_execute: toolCall?.confirmation?.continue_after_execute,
    }));

  const confirmableActions = (message.meta_data?.confirmable_actions || fallbackConfirmableActions).filter(
    (action) => !!action?.action_id && !!action?.target_tool_name
  );
  const thinkingSteps = (message?.meta_data?.thinking_steps || [])
    .map((step) => ({
      text: typeof step?.text === "string" ? step.text.trim() : "",
      duration: typeof step?.duration === "string" ? step.duration : "",
    }))
    .filter((step) => step.text.length > 0);
  const nonEmptyThoughts = [...(message.thoughts || []), ...thinkTagExtraction.thoughts]
    .map((thought) => (typeof thought === "string" ? thought.trim() : ""))
    .filter((thought) => thought.length > 0)
    .filter((thought, index, allThoughts) => allThoughts.indexOf(thought) === index);
  const isPartialMessageUUID = typeof message.uuid === "string" && message.uuid.startsWith("partial_");
  const isQueuedMessageUUID = typeof message.uuid === "string" && message.uuid.startsWith("queued_");
  const isQueuePending = message?.meta_data?.queue_state === "queued" || isQueuedMessageUUID;
  const toolCalls = (message?.tool_calls || []) as MessageToolCall[];
  const executionBlockedReason = isPartialMessageUUID
    ? "Action is still syncing. Wait for the final message to appear, then execute."
    : undefined;
  const canCopyMessage = normalizedMessageText.trim().length > 0;
  const canRegenerate = Boolean(chatUUID && rerunSourceMessageUUID);
  const showMessageActions = showToolbar && (canCopyMessage || canRegenerate);

  return (
    <BotMessageShell
      key={message.uuid}
      senderLabel={chat?.partner?.name ?? "Assistant"}
      senderMeta={senderMeta}
      avatarSrc={logoUrl}
      showIdentity={showBotIdentity}
      avatarFooter={showMessageActions ? (
        <BotMessageActionsMenu
          onCopy={handleCopy}
          onRegenerate={handleRegenerate}
          canCopy={canCopyMessage}
          canRegenerate={canRegenerate}
        />
      ) : null}
    >
      {isQueuePending &&
        !metaFinished &&
        normalizedMessageText === "" &&
        nonEmptyThoughts.length === 0 &&
        (message?.tool_calls?.length ?? 0) === 0 && <BootingIndicator label="Msgmate Agent Queued" />}

      {!isQueuePending &&
        !metaFinished &&
        normalizedMessageText === "" &&
        nonEmptyThoughts.length === 0 &&
        (message?.tool_calls?.length ?? 0) === 0 && <BootingIndicator label="Msgmate Booting" />}

      {!metaFinished &&
        nonEmptyThoughts.length > 0 && (
          <Collapsible open id={`thoughts-${message.uuid}`}>
            <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">
              <ShinyText>Thinking… ({message?.meta_data?.thinking_time})</ShinyText>
            </CollapsibleTrigger>
            <CollapsibleContent className="CollapsibleContent transition-all duration-300">
              {nonEmptyThoughts.map((thought, index) => (
                <div key={index}>{wrapInBlockquote(thought)}</div>
              ))}
            </CollapsibleContent>
          </Collapsible>
        )}

      {!metaFinished && toolCalls.length > 0 && (
        <ToolCallsGroup
          messageUUID={message.uuid}
          toolCalls={toolCalls}
          defaultOpen
          header={
            <ShinyText>
              Calling tools (
              {toolCalls.length > 1 ? `${toolCalls.length} tools` : `'${toolCalls[0]?.name || "tool"}'`}
              )
            </ShinyText>
          }
        />
      )}

      {metaFinished && thinkingSteps.length > 0
        ? thinkingSteps.map((step, index) => (
            <Collapsible key={`${message.uuid}-thinking-step-${index}`} id={`thought-step-${message.uuid}-${index}`}>
              <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">
                Thought {index + 1}{step.duration ? ` (${step.duration})` : ""}
              </CollapsibleTrigger>
              <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                {wrapInBlockquote(step.text)}
              </CollapsibleContent>
            </Collapsible>
          ))
        : metaFinished && nonEmptyThoughts.length > 0 && (
            <Collapsible id={`thoughts-${message.uuid}`}>
              <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">
                Thoughts{message?.meta_data?.thinking_time ? ` for ${message?.meta_data?.thinking_time}` : ""}
              </CollapsibleTrigger>
              <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                {nonEmptyThoughts.map((thought, index) => (
                  <div key={index}>{wrapInBlockquote(thought)}</div>
                ))}
              </CollapsibleContent>
            </Collapsible>
          )}

      {metaFinished && toolCalls.length > 0 && (
        <ToolCallsGroup
          messageUUID={message.uuid}
          toolCalls={toolCalls}
          header={toolCalls.length === 1 ? <>Tool call</> : <>Tool calls ({toolCalls.length}x)</>}
        />
      )}

      {chatUUID && message.uuid && confirmableActions.length > 0
        ? confirmableActions.map((action) => (
            <ConfirmableActionWidget
              key={`${message.uuid}-${action.action_id}`}
              chatUUID={chatUUID}
              messageUUID={message.uuid}
              action={action}
              executionBlockedReason={executionBlockedReason}
              onExecuted={() => {
                mutate(`/api/v1/chats/${chatUUID}/messages/list`);
                mutate(`/api/v1/chats/list`);
              }}
            />
          ))
        : null}

      {!isConfirmableEvent &&
      !message.text?.trim() &&
      confirmableActions.length > 0 &&
      nonEmptyThoughts.length === 0 ? (
        <p className="my-2 text-sm text-muted-foreground">Review and confirm the proposed action below.</p>
      ) : null}

      {isConfirmableEvent ? (
        <ConfirmableEventIndicator message={message} />
      ) : providerRetryEvent ? (
        <ProviderRetryEventWidget event={providerRetryEvent} />
      ) : (
        <MessageMarkdown>{normalizedMessageText}</MessageMarkdown>
      )}
      <FileAttachmentsList attachments={attachments} />
      <ChatUIMessageExtras
        toolCalls={toolCalls}
        meta={(message.meta_data ?? {}) as Record<string, unknown>}
        chatUUID={chatUUID}
        messageUUID={message.uuid}
        interactionState={interactionState}
        onMutate={() => {
          mutate(`/api/v1/chats/${chatUUID}/messages/list`);
          mutate(`/api/v1/chats/list`);
        }}
      />
    </BotMessageShell>
  );
}

export function MessageItem({
  message,
  chat,
  chatUUID,
  rerunSourceMessageUUID,
  selfIsSender = false,
  isBotChat = false,
  showToolbar = true,
  showBotIdentity = true,
  interactionState,
}: {
  message: Parameters<typeof BotMessageItem>[0]["message"];
  chat: Parameters<typeof BotMessageItem>[0]["chat"];
  chatUUID?: string;
  rerunSourceMessageUUID?: string;
  selfIsSender?: boolean;
  isBotChat?: boolean;
  showToolbar?: boolean;
  showBotIdentity?: boolean;
  interactionState?: string | null;
}) {
  if (isBotChat) {
    return (
      <BotMessageItem
        message={message}
        chat={chat}
        chatUUID={chatUUID}
        rerunSourceMessageUUID={rerunSourceMessageUUID}
        selfIsSender={selfIsSender}
        showToolbar={showToolbar}
        showBotIdentity={showBotIdentity}
        interactionState={interactionState}
      />
    );
  }

  return <UserMessageItem message={message} chat={chat} selfIsSender={selfIsSender} />;
}
