import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
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
  LoadingSpinner,
  PendingMessageItem,
  ShinyText,
  UserMessageShell,
} from "@open-chat-go/ui";
import { ConfirmableActionWidget, type ConfirmableAction } from "./ConfirmableActionWidget";
import { ChatUIMessageExtras } from "./ChatUIMessageExtras";
import { ToolValueText } from "./ToolCallDetail";
import {
  formatToolDuration,
  isCompletedToolStatus,
  plural,
  resolveToolCallStatus,
  toolCallDurationMs,
  toolCallStartedAtMs,
  toolCallSummary,
  toolDisplayLabel,
  toolResultPreview,
  toolStatusBadgeClasses,
  toolStatusDotClasses,
  toolStatusLabel,
  type ToolCallLike,
  type ToolCallStatus,
} from "./toolCallSummary";
import {
  buildTurnBlocks,
  parseTurnTimeline,
  type TurnBlock,
  type TurnThoughtBlock,
} from "./turnTimeline";
import { resolveChatUIExtensionForChat } from "./chat-ui/registry";
import {
  ProviderRetryEventWidget,
  resolveProviderRetryEvent,
} from "./ProviderRetryEventWidget";
import { optimisticResolveActionTask, revalidateChatData } from "../../lib/chat-cache";
import { latestPendingConfirmableActionId } from "../../lib/confirmable-actions";
import { isQueuePendingMessage, resolveStatusIndicator } from "../../lib/status-indicator";
import { queuedReplyHintLabel } from "../../../lib/delivery-mode";
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
        remarkPlugins={[remarkGfm]}
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
          table: ({ children, ...props }) => (
            <div className="my-3 overflow-x-auto">
              <table className="chat-markdown-table" {...props}>{children}</table>
            </div>
          ),
          thead: ({ children, ...props }) => <thead {...props}>{children}</thead>,
          tbody: ({ children, ...props }) => <tbody {...props}>{children}</tbody>,
          tr: ({ children, ...props }) => <tr {...props}>{children}</tr>,
          th: ({ children, ...props }) => <th {...props}>{children}</th>,
          td: ({ children, ...props }) => <td {...props}>{children}</td>,
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

// The core rows render the same tool-call shape the shared summary helpers
// consume; keep the alias so the surrounding call sites stay unchanged.
type MessageToolCall = ToolCallLike;

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

  return (
    <Collapsible id={id}>
      <CollapsibleTrigger className="flex w-full items-center justify-between rounded-md border border-border/60 bg-muted/20 px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted/30">
        <span className="font-medium">{label}</span>
        <span className="ml-2 truncate text-right">{preview}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="CollapsibleContent transition-all duration-300">
        <div className="mt-1">
          <ToolValueText value={value} />
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

// ToolCallTiming shows a per-tool-call time affordance (ci#569): a live timer
// that ticks up while the call is running, and the recorded duration once it
// completed. It renders nothing when the backend reported no timing, so older
// messages are unchanged.
function ToolCallTiming({ toolCall, status }: { toolCall: ToolCallLike; status: ToolCallStatus }) {
  const startedAt = toolCallStartedAtMs(toolCall);
  const duration = toolCallDurationMs(toolCall);
  const [now, setNow] = useState(() => Date.now());
  // The timer only runs while the call is actually ongoing; a finished call
  // freezes into its recorded duration.
  const isLive = status === "ongoing" && duration === undefined && startedAt !== undefined;

  useEffect(() => {
    if (!isLive) {
      return;
    }
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [isLive]);

  let label = "";
  if (duration !== undefined) {
    label = formatToolDuration(duration);
  } else if (startedAt !== undefined) {
    // The start stamp comes from the server, so a client clock that is behind
    // must not render a negative elapsed time.
    label = formatToolDuration(Math.max(0, now - startedAt));
  }
  if (!label) {
    return null;
  }

  return (
    <span
      data-testid="tool-call-timing"
      className="shrink-0 tabular-nums text-[10px] text-muted-foreground"
      title={isLive ? "Elapsed time" : "Duration"}
      aria-label={isLive ? `Running for ${label}` : `Took ${label}`}
    >
      {label}
    </span>
  );
}

// ToolCallCard renders one tool call: a status dot, the friendly tool label, the
// per-tool parameter summary, a short result preview, the call's timing and the
// status badge, with the detailed parameters/result/error one click away. It is
// the single row used both by the summarised tool-call group and by the ordered
// turn sequence (thoughts between the calls).
function ToolCallCard({
  messageUUID,
  toolCall,
  index,
  terminal = false,
}: {
  messageUUID: string;
  toolCall: MessageToolCall;
  index: number;
  /**
   * The message is finished/cancelled: a call still left at "ongoing" was
   * interrupted by the stop and must not render as a live running call
   * (ci#668).
   */
  terminal?: boolean;
}) {
  const status = resolveToolCallStatus(toolCall, { terminal });
  const cardId = `${messageUUID}-tool-call-${index}`;
  const label = toolDisplayLabel(toolCall?.name);
  const summary = toolCallSummary(toolCall);
  const preview = toolResultPreview(toolCall);

  return (
    <Collapsible defaultOpen={!isCompletedToolStatus(status)} id={`tool-call-card-${cardId}`}>
      <div className="overflow-hidden rounded-lg border border-border/70 bg-card/60">
        <CollapsibleTrigger className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-muted/30">
          <span
            className={cn("size-1.5 shrink-0 rounded-full", toolStatusDotClasses(status))}
            aria-hidden="true"
          />
          <span className="shrink-0 font-medium">{label}</span>
          {summary ? (
            <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground">
              {summary}
            </span>
          ) : null}
          <span className="ml-auto flex shrink-0 items-center gap-1">
            {preview ? (
              <span className="hidden text-[10px] text-muted-foreground sm:inline">{preview}</span>
            ) : null}
            <ToolCallTiming toolCall={toolCall} status={status} />
            <span
              className={cn(
                "rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
                toolStatusBadgeClasses(status)
              )}
            >
              {toolStatusLabel(status)}
            </span>
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="CollapsibleContent transition-all duration-300">
          <div className="space-y-1 border-t border-border/60 px-2 py-2">
            <ToolCallLineSection
              id={`tool-call-params-${messageUUID}-${index}`}
              label="Parameters"
              value={toolCall?.arguments}
            />
            <ToolCallLineSection
              id={`tool-call-result-${messageUUID}-${index}`}
              label="Result"
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
}

// TurnThoughtBlockView renders one reasoning block the agent produced, in place
// in the ordered turn sequence (ci#569), so the user can follow what the agent
// was thinking between its tool calls. A block that is still being produced keeps
// a pulsing marker.
function TurnThoughtBlockView({ text, durationMs, streaming }: TurnThoughtBlock) {
  const duration = durationMs !== undefined ? formatToolDuration(durationMs) : "";
  return (
    <div
      data-testid="turn-thought"
      className="rounded-lg border border-dashed border-border/70 bg-muted/15 px-2.5 py-1.5"
    >
      <div className="mb-0.5 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            streaming ? "animate-pulse bg-sky-500" : "bg-muted-foreground/40"
          )}
          aria-hidden="true"
        />
        <span>{streaming ? "Thinking" : "Thought"}</span>
        {duration ? <span className="normal-case tabular-nums tracking-normal">{duration}</span> : null}
      </div>
      <div className="scrollbar-hidden max-h-72 overflow-auto whitespace-pre-wrap break-words text-xs leading-5 text-muted-foreground">
        {text}
      </div>
    </div>
  );
}

// TurnNarrationBlockView renders a block of assistant *narration* that the model
// emitted before a tool call ("Now let me check the migration."). Unlike a
// reasoning block it is user-visible prose, so it renders as normal message text
// rather than a quiet thought box — that is what makes a long tool-using turn
// readable instead of a wall of call rows (ci#569).
function TurnNarrationBlockView({ text, streaming }: TurnThoughtBlock) {
  return (
    <div
      data-testid="turn-narration"
      className="flex items-start gap-1.5 px-0.5 text-sm leading-6"
    >
      <span className="min-w-0 flex-1 break-words">
        <MessageMarkdown>{text}</MessageMarkdown>
      </span>
      {streaming ? (
        <span className="mt-2 size-1.5 shrink-0 animate-pulse rounded-full bg-sky-500" aria-hidden="true" />
      ) : null}
    </div>
  );
}

// InterruptedNote gives a cancelled/interrupted turn a clear affordance instead
// of letting it read like a completed reply (ci#643). A turn that was stopped
// after it had already dispatched a run of tool calls carries the real work on
// `tool_calls`, so the note says how many calls ran before the stop and points
// the reader at them; a cancelled turn is never silent about being interrupted.
function InterruptedNote({
  toolCallCount,
  cancelled,
}: {
  toolCallCount: number;
  cancelled: boolean;
}) {
  if (!cancelled) {
    return null;
  }
  const label =
    toolCallCount > 0
      ? `Interrupted after ${toolCallCount} tool ${toolCallCount === 1 ? "call" : "calls"}`
      : "Interrupted";
  return (
    <p
      data-testid="turn-interrupted"
      className="flex items-center gap-1.5 px-0.5 text-xs text-amber-700 dark:text-amber-400"
    >
      <span className="inline-block size-1.5 shrink-0 rounded-full bg-amber-500" aria-hidden="true" />
      <span className="font-medium">{label}</span>
      {toolCallCount > 0 ? (
        <span className="text-muted-foreground">— the calls below did run before the stop.</span>
      ) : null}
    </p>
  );
}

// NoReasoningNote is shown when a finished turn dispatched tool calls without
// emitting any reasoning or narration at all. Rather than leaving the reader to
// wonder why the transcript is a bare stack of call rows, it says so explicitly
// (ci#569) — the agent produced no "thinking out loud" for these calls.
function NoReasoningNote({ toolCallCount }: { toolCallCount: number }) {
  return (
    <p
      data-testid="turn-no-reasoning"
      className="px-0.5 text-xs italic text-muted-foreground"
    >
      {toolCallCount === 1 ? "This call ran" : `These ${toolCallCount} calls ran`} without any
      reasoning or commentary from the agent.
    </p>
  );
}

// TurnSequence renders an assistant turn as its ordered blocks: the reasoning
// and narration the agent produced, the tool call it then dispatched, the next
// block, and so on (ci#569). It replaces the separate "thoughts above, tool
// calls below" layout for turns the backend reported a timeline for.
function TurnSequence({
  messageUUID,
  thoughtBlocks,
  toolCalls,
  header,
  showNarration = true,
  defaultOpen = false,
  terminal = false,
}: {
  messageUUID: string;
  thoughtBlocks: TurnThoughtBlock[];
  toolCalls: MessageToolCall[];
  header: React.ReactNode;
  /** The message is finished/cancelled (interrupted in-flight calls, ci#668). */
  terminal?: boolean;
  /**
   * Render the timeline's assistant-narration blocks. False while the turn is
   * still running: the live message body already streams that text, so showing
   * the timeline copy too would print it twice. The reasoning blocks are always
   * rendered (the body carries only the *current* bit of text, not the earlier
   * blocks the agent already produced).
   */
  showNarration?: boolean;
  defaultOpen?: boolean;
}) {
  const blocks = useMemo<TurnBlock[]>(
    () => buildTurnBlocks({ timeline: thoughtBlocks, toolCalls }),
    [thoughtBlocks, toolCalls],
  );

  if (blocks.length === 0) {
    return null;
  }

  return (
    <Collapsible defaultOpen={defaultOpen} id={`turn-sequence-${messageUUID}`}>
      <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">{header}</CollapsibleTrigger>
      <CollapsibleContent className="CollapsibleContent transition-all duration-300">
        <div className="space-y-2" data-testid="turn-sequence">
          {blocks.map((block, blockIndex) =>
            block.kind === "tool_call" ? (
              <ToolCallCard
                key={`${messageUUID}-tool-call-${block.index}`}
                messageUUID={messageUUID}
                toolCall={block.call as MessageToolCall}
                index={block.index}
                terminal={terminal}
              />
            ) : block.kind === "text" ? (
              showNarration ? (
                <TurnNarrationBlockView key={`${messageUUID}-narration-${blockIndex}`} {...block} />
              ) : null
            ) : (
              <TurnThoughtBlockView key={`${messageUUID}-thought-${blockIndex}`} {...block} />
            ),
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ToolCallsGroup({
  messageUUID,
  toolCalls,
  header,
  defaultOpen = false,
  terminal = false,
}: {
  messageUUID: string;
  toolCalls: MessageToolCall[];
  header: React.ReactNode;
  defaultOpen?: boolean;
  /** The message is finished/cancelled (interrupted in-flight calls, ci#668). */
  terminal?: boolean;
}) {
  if (toolCalls.length === 0) {
    return null;
  }

  return (
    <Collapsible defaultOpen={defaultOpen} id={`tool-calls-${messageUUID}`}>
      <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">{header}</CollapsibleTrigger>
      <CollapsibleContent className="CollapsibleContent transition-all duration-300">
        <div className="space-y-2">
          {toolCalls.map((toolCall, index) => (
            <ToolCallCard
              key={`${messageUUID}-tool-call-${index}`}
              messageUUID={messageUUID}
              toolCall={toolCall}
              index={index}
              terminal={terminal}
            />
          ))}
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
  const action =
    phase === "requested"
      ? "Execution requested"
      : phase === "failed"
        ? "Execution failed"
        : phase === "rejected"
          ? "Execution rejected"
          : "Execution completed";
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
      /** ci#656: server marker for a message parked behind a live turn. */
      queue_state?: string;
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
  // ci#656: a message sent into a chat whose bot turn is still running is
  // persisted immediately but consumed later. Label it so the sender knows it
  // was accepted and is waiting, instead of looking ignored.
  const queuedHint = queuedReplyHintLabel(isQueuePendingMessage(message));

  return (
    <UserMessageShell
      key={message.uuid}
      senderLabel={senderLabel}
      senderMeta={senderMeta}
      avatarLabel={senderLabel}
      avatarVariant={avatarVariant}
      footer={
        queuedHint ? (
          <div
            className="text-muted-foreground mt-1 text-xs"
            data-testid="queued-reply-hint"
          >
            {queuedHint}
          </div>
        ) : undefined
      }
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
  shellless = false,
  renderExtras = true,
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
  chat: { partner?: { name?: string }; config?: { chat_backend?: string; backend?: string } };
  chatUUID?: string;
  rerunSourceMessageUUID?: string;
  selfIsSender?: boolean;
  showToolbar?: boolean;
  showBotIdentity?: boolean;
  interactionState?: string | null;
  /** Render only the message content (no shell/header); used by grouped bubbles. */
  shellless?: boolean;
  /** Render the per-message integration extras (default true). */
  renderExtras?: boolean;
}) {
  // A cancelled turn is terminal too: the backend sets `finished` and
  // `cancelled` together, but treat `cancelled` as authoritative so an
  // interrupted turn never renders as still-running and never hides the tool
  // calls it performed (ci#643).
  const isCancelled = message?.meta_data?.cancelled === true;
  const metaFinished = Boolean(message?.meta_data?.finished) || isCancelled;
  const attachments = mapAttachments(message);
  const senderMeta = useMemo(() => formatRelativeTime(message.send_at), [message.send_at]);
  const isConfirmableEvent = message.meta_data?.event_type === "confirmable_action_execute";
  const providerRetryEvent = resolveProviderRetryEvent(message.meta_data as Record<string, unknown> | undefined);
  const thinkTagExtraction = useMemo(() => extractThoughtsFromThinkTags(message.text), [message.text]);
  const normalizedMessageText = thinkTagExtraction.thoughts.length > 0 ? thinkTagExtraction.cleanText : (message.text || "");

  // The chat UI extension is resolved here (not just for the message body) so
  // an extension that renders its confirmable actions inline can also suppress
  // the confirmation lifecycle messages. Those event messages duplicate the
  // inline confirm/execute widget the extension renders on the source message,
  // so they are hidden when the extension opts in.
  const uiExtension = resolveChatUIExtensionForChat({
    chatBackend: chat?.config?.chat_backend ?? chat?.config?.backend,
    chat,
    toolCalls: message?.tool_calls || [],
  });
  const inlineConfirmableActions = Boolean(uiExtension?.inlineConfirmableActions);
  if (inlineConfirmableActions && isConfirmableEvent) {
    return null;
  }

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

      await revalidateChatData(chatUUID);
    } catch (error) {
      console.error("Failed to rerun message:", error);
    }
  };

  if (selfIsSender) {
    // ci#656: a message whose reply is parked behind the chat's live turn
    // carries the server's `queue_state` marker. Render it as pending instead
    // of letting it look lost while the running turn finishes. The predicate is
    // the transcript's existing isQueuePendingMessage; the shared helper only
    // names the text, so the two cannot drift apart.
    const queuedReplyHint = queuedReplyHintLabel(isQueuePendingMessage(message));
    return (
      <UserMessageShell
        key={message.uuid}
        senderLabel="You"
        align="end"
        footer={
          queuedReplyHint ? (
            <div className="mt-1 text-xs text-muted-foreground" data-testid="queued-reply-hint">
              {queuedReplyHint}
            </div>
          ) : null
        }
      >
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
  // The latest still-unconfirmed action is the one blocking the chat, so its
  // widget auto-expands (the user may still collapse it manually).
  const lastPendingActionId = latestPendingConfirmableActionId(confirmableActions);
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
  const timelineThoughts = useMemo(
    () => parseTurnTimeline(message?.meta_data as Record<string, unknown> | undefined),
    [message.meta_data],
  );
  // A turn the backend reported an ordered timeline for is rendered as its
  // sequence of reasoning blocks and tool calls (ci#569). Extensions that
  // provide their own message body keep rendering their own rows.
  const usesTurnSequence = timelineThoughts.length > 0 && !uiExtension?.MessageBody;
  const isPartialMessageUUID = typeof message.uuid === "string" && message.uuid.startsWith("partial_");
  const isQueuePending = isQueuePendingMessage(message);
  const toolCalls = (message?.tool_calls || []) as MessageToolCall[];
  const toolCallLabel = toolCalls.length === 1 ? "1 tool call" : `${toolCalls.length} tool calls`;
  // The timeline mixes two kinds of block: the model's reasoning ("thought")
  // and its user-visible narration between calls ("text"). Both make the turn
  // readable, so the header reports them separately instead of lumping the
  // prose in as "thoughts" (ci#569).
  const thoughtBlockCount = timelineThoughts.filter((block) => block.kind === "thought").length;
  const narrationBlockCount = timelineThoughts.length - thoughtBlockCount;
  const timelineParts: string[] = [];
  if (thoughtBlockCount > 0) timelineParts.push(plural(thoughtBlockCount, "thought"));
  if (narrationBlockCount > 0) timelineParts.push(plural(narrationBlockCount, "commentary", "commentary"));
  const timelineLabel = timelineParts.join(" · ");
  // The header of the ordered sequence summarises what the turn did without
  // hiding it: the blocks are rendered open, the header is the collapse handle.
  const turnSequenceHeader =
    isCancelled && toolCalls.length > 0 ? (
      `Interrupted after ${toolCallLabel}`
    ) : toolCalls.length > 0 ? (
      metaFinished ? (
        <>
          {timelineLabel ? `${timelineLabel} · ` : ""}
          {toolCallLabel}
        </>
      ) : (
        <ShinyText>
          {timelineLabel ? "Thinking" : "Calling tools"} ({toolCallLabel})
        </ShinyText>
      )
    ) : metaFinished ? (
      `${timelineLabel}`
    ) : (
      <ShinyText>Thinking…</ShinyText>
    );
  // An integration (keyed on the chat's chat_backend) may supply a custom
  // message body that renders the assistant text and its tool calls inline, in
  // order. When present it replaces the default summarised tool-call block and
  // the markdown body. When the extension also sets `inlineConfirmableActions`
  // it renders the confirmable-action widgets itself and the core suppresses
  // its own widgets, the confirm hint and the lifecycle event bubbles.
  const CustomMessageBody = uiExtension?.MessageBody;
  const CustomThoughts = uiExtension?.Thoughts;
  const CustomStatusIndicator = uiExtension?.StatusIndicator;
  const hideBotAvatar = Boolean(uiExtension?.hideBotAvatar);
  const hideMessageActions = Boolean(uiExtension?.hideMessageActions);
  // While an empty, in-flight turn has nothing to render yet, show the queued /
  // booting placeholder. An integration can supply a compact replacement
  // (`StatusIndicator`); the core default stays for everything else.
  const statusIndicator = resolveStatusIndicator({
    finished: Boolean(metaFinished),
    queuePending: isQueuePending,
    hasText: normalizedMessageText.length > 0,
    hasThoughts: nonEmptyThoughts.length > 0,
    toolCallCount: message?.tool_calls?.length ?? 0,
  });
  const executionBlockedReason = isPartialMessageUUID
    ? "Action is still syncing. Wait for the final message to appear, then execute."
    : undefined;
  const canCopyMessage = normalizedMessageText.trim().length > 0;
  const canRegenerate = Boolean(chatUUID && rerunSourceMessageUUID);
  const showMessageActions = showToolbar && (canCopyMessage || canRegenerate) && !hideMessageActions;
  // Once a turn has started producing content but is not finished yet, show a
  // small animated "working" footer at the bottom of the bubble so the running
  // state is visible without scrolling back to the top placeholder.
  const showWorkingFooter = !metaFinished && !isQueuePending && statusIndicator === null;

  const content = (
    <>
      {statusIndicator
        ? CustomStatusIndicator
          ? (
              <CustomStatusIndicator kind={statusIndicator.kind} label={statusIndicator.label} />
            )
          : (
              <BootingIndicator label={statusIndicator.label} />
            )
        : null}

      {!metaFinished && nonEmptyThoughts.length > 0 && !usesTurnSequence ? (
        CustomThoughts ? (
          <CustomThoughts
            messageUUID={message.uuid}
            thoughts={nonEmptyThoughts}
            steps={thinkingSteps}
            thinkingTime={message?.meta_data?.thinking_time}
            isGenerating
            isFinished={false}
          />
        ) : (
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
        )
      ) : null}

      {!CustomMessageBody && !metaFinished && toolCalls.length > 0 && !usesTurnSequence && (
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

      {usesTurnSequence && isCancelled ? (
        <InterruptedNote toolCallCount={toolCalls.length} cancelled={isCancelled} />
      ) : null}

      {usesTurnSequence ? (
        <TurnSequence
          messageUUID={message.uuid}
          thoughtBlocks={timelineThoughts}
          toolCalls={toolCalls}
          header={turnSequenceHeader}
          // While the turn streams, its current narration *is* the message body
          // below, so the timeline renders only the reasoning blocks; once the
          // turn finished the body keeps just the tail text, so the recorded
          // narration is rendered inline in the sequence (ci#569).
          showNarration={Boolean(metaFinished)}
          terminal={metaFinished}
          defaultOpen
        />
      ) : null}

      {metaFinished && !usesTurnSequence && (thinkingSteps.length > 0 || nonEmptyThoughts.length > 0) ? (
        CustomThoughts ? (
          <CustomThoughts
            messageUUID={message.uuid}
            thoughts={nonEmptyThoughts}
            steps={thinkingSteps}
            thinkingTime={message?.meta_data?.thinking_time}
            isGenerating={false}
            isFinished
          />
        ) : thinkingSteps.length > 0 ? (
          thinkingSteps.map((step, index) => (
            <Collapsible key={`${message.uuid}-thinking-step-${index}`} id={`thought-step-${message.uuid}-${index}`}>
              <CollapsibleTrigger className="flex items-center gap-2 hover:opacity-80">
                Thought {index + 1}{step.duration ? ` (${step.duration})` : ""}
              </CollapsibleTrigger>
              <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                {wrapInBlockquote(step.text)}
              </CollapsibleContent>
            </Collapsible>
          ))
        ) : (
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
        )
      ) : null}

      {CustomMessageBody ? (
        <CustomMessageBody
          messageUUID={message.uuid}
          text={normalizedMessageText}
          toolCalls={toolCalls}
          meta={(message.meta_data ?? {}) as Record<string, unknown>}
          isGenerating={!metaFinished}
          interactionState={interactionState}
          chatUUID={chatUUID}
          confirmableActions={confirmableActions}
          executionBlockedReason={executionBlockedReason}
          onMutate={() => {
            void revalidateChatData(chatUUID);
          }}
        />
      ) : null}

      {!CustomMessageBody && metaFinished && toolCalls.length > 0 && !usesTurnSequence && (
        <>
          {/* A finished turn that ran tools without reporting any reasoning or
              narration says so, instead of leaving a bare stack of call rows
              that reads like the agent did nothing but call tools (ci#569). */}
          <InterruptedNote toolCallCount={toolCalls.length} cancelled={isCancelled} />
          {!isCancelled && thinkingSteps.length === 0 && nonEmptyThoughts.length === 0 ? (
            <NoReasoningNote toolCallCount={toolCalls.length} />
          ) : null}
          <ToolCallsGroup
            messageUUID={message.uuid}
            toolCalls={toolCalls}
            defaultOpen={isCancelled}
            header={
              isCancelled
                ? `Interrupted after ${toolCalls.length} tool ${toolCalls.length === 1 ? "call" : "calls"}`
                : toolCalls.length === 1
                  ? <>Tool call</>
                  : <>Tool calls ({toolCalls.length}x)</>
            }
          />
        </>
      )}

      {!inlineConfirmableActions && chatUUID && message.uuid && confirmableActions.length > 0
        ? confirmableActions.map((action) => (
            <ConfirmableActionWidget
              key={`${message.uuid}-${action.action_id}`}
              chatUUID={chatUUID}
              messageUUID={message.uuid}
              action={action}
              executionBlockedReason={executionBlockedReason}
              autoExpand={action.action_id === lastPendingActionId}
              onExecuted={() => {
                // Drop the task from the action-task feeds optimistically (the
                // sidebar badge and the action-task view) so a confirmed action
                // disappears immediately instead of after the next poll (ci#726).
                void optimisticResolveActionTask(chatUUID, message.uuid);
                void revalidateChatData(chatUUID);
              }}
            />
          ))
        : null}

      {!inlineConfirmableActions &&
      !isConfirmableEvent &&
      !message.text?.trim() &&
      confirmableActions.length > 0 &&
      nonEmptyThoughts.length === 0 ? (
        <p className="my-2 text-sm text-muted-foreground">Review and confirm the proposed action below.</p>
      ) : null}

      {isConfirmableEvent ? (
        <ConfirmableEventIndicator message={message} />
      ) : providerRetryEvent ? (
        <ProviderRetryEventWidget event={providerRetryEvent} />
      ) : CustomMessageBody ? null : (
        <MessageMarkdown>{normalizedMessageText}</MessageMarkdown>
      )}
      <FileAttachmentsList attachments={attachments} />
      {renderExtras ? (
        <ChatUIMessageExtras
          toolCalls={toolCalls}
          meta={(message.meta_data ?? {}) as Record<string, unknown>}
          chatUUID={chatUUID}
          messageUUID={message.uuid}
          interactionState={interactionState}
          onMutate={() => {
            void revalidateChatData(chatUUID);
          }}
        />
      ) : null}
      {showWorkingFooter ? (
        <div
          className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground"
          role="status"
          aria-live="polite"
        >
          <LoadingSpinner size={12} />
          <ShinyText>Working…</ShinyText>
        </div>
      ) : null}
    </>
  );

  if (shellless) {
    return content;
  }

  return (
    <BotMessageShell
      key={message.uuid}
      senderLabel={chat?.partner?.name ?? "Assistant"}
      senderMeta={senderMeta}
      avatarSrc={logoUrl}
      showIdentity={showBotIdentity}
      showAvatar={showBotIdentity && !hideBotAvatar}
      avatarFooter={showMessageActions ? (
        <BotMessageActionsMenu
          onCopy={handleCopy}
          onRegenerate={handleRegenerate}
          canCopy={canCopyMessage}
          canRegenerate={canRegenerate}
        />
      ) : null}
    >
      {content}
    </BotMessageShell>
  );
}

/**
 * Groups consecutive assistant messages into a single bubble: one sender header
 * and one bubble for what was a run of separate bot turns. Each message keeps
 * its own body (thoughts, tool calls, confirm widgets, text) rendered in the
 * original time order, separated by a hairline, and only the last one renders
 * the per-message integration extras so repeated cards are not stacked.
 */
export function BotMessageGroup({
  messages,
  chat,
  chatUUID,
  rerunSourceMessageUUID,
  showToolbar = true,
  showBotIdentity = true,
  interactionState,
}: {
  messages: Array<Parameters<typeof BotMessageItem>[0]["message"]>;
  chat: Parameters<typeof BotMessageItem>[0]["chat"];
  chatUUID?: string;
  rerunSourceMessageUUID?: string;
  showToolbar?: boolean;
  showBotIdentity?: boolean;
  interactionState?: string | null;
}) {
  const first = messages[0];
  const last = messages[messages.length - 1];
  const uiExtension = resolveChatUIExtensionForChat({
    chatBackend: chat?.config?.chat_backend ?? chat?.config?.backend,
    chat,
    toolCalls: last?.tool_calls || [],
  });
  const hideBotAvatar = Boolean(uiExtension?.hideBotAvatar);
  const hideMessageActions = Boolean(uiExtension?.hideMessageActions);
  const combinedText = useMemo(
    () =>
      messages
        .map((message) => {
          const extraction = extractThoughtsFromThinkTags(message.text);
          return extraction.thoughts.length > 0 ? extraction.cleanText : message.text || "";
        })
        .filter((text) => text.trim().length > 0)
        .join("\n\n"),
    [messages],
  );
  const senderMeta = useMemo(() => formatRelativeTime(last?.send_at), [last?.send_at]);
  const canCopyMessage = combinedText.trim().length > 0;
  const canRegenerate = Boolean(chatUUID && rerunSourceMessageUUID);
  const showMessageActions = showToolbar && (canCopyMessage || canRegenerate) && !hideMessageActions;

  const handleCopy = () => {
    if (combinedText) {
      navigator.clipboard.writeText(combinedText);
    }
  };

  const handleRegenerate = async () => {
    if (!chatUUID || !rerunSourceMessageUUID) {
      return;
    }
    try {
      const response = await fetch(
        `/api/v1/chats/${chatUUID}/messages/${rerunSourceMessageUUID}/rerun`,
        { method: "POST" },
      );
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || "Failed to rerun response");
      }
      await revalidateChatData(chatUUID);
    } catch (error) {
      console.error("Failed to rerun message:", error);
    }
  };

  if (messages.length === 0) {
    return null;
  }

  return (
    <BotMessageShell
      key={`group_${first?.uuid ?? "bot"}`}
      senderLabel={chat?.partner?.name ?? "Assistant"}
      senderMeta={senderMeta}
      avatarSrc={logoUrl}
      showIdentity={showBotIdentity}
      showAvatar={showBotIdentity && !hideBotAvatar}
      avatarFooter={showMessageActions ? (
        <BotMessageActionsMenu
          onCopy={handleCopy}
          onRegenerate={handleRegenerate}
          canCopy={canCopyMessage}
          canRegenerate={canRegenerate}
        />
      ) : null}
    >
      {messages.map((message, index) => (
        <div
          key={message.uuid}
          className={index > 0 ? "mt-3 border-t border-border/40 pt-3" : undefined}
        >
          <BotMessageItem
            message={message}
            chat={chat}
            chatUUID={chatUUID}
            rerunSourceMessageUUID={rerunSourceMessageUUID}
            showToolbar={showToolbar}
            showBotIdentity={false}
            interactionState={interactionState}
            shellless
            renderExtras={index === messages.length - 1}
          />
        </div>
      ))}
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
