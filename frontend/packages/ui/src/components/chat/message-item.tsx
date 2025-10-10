import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { Bot, Clipboard, Loader2, RefreshCcw, User } from "lucide-react";

import { cn } from "../../lib/utils";
import { Text, TextTypes } from "../text";
import { Icon } from "../icon";
import { Button } from "../button";

export function ShinyText({ children }: { children: ReactNode }) {
  return (
    <Text type={TextTypes.Body6} tag="span" bold className="shiny-text">
      {children}
    </Text>
  );
}

export function MessageAvatar({
  variant = "user",
  src,
  label,
  className,
}: {
  variant?: "user" | "bot" | "self";
  src?: string;
  label?: string;
  className?: string;
}) {
  if (src) {
    return (
      <img
        src={src}
        alt={label ?? ""}
        className={cn(
          "size-10 shrink-0 rounded-full object-cover ring-2 ring-border shadow-sm",
          className
        )}
      />
    );
  }

  const IconComponent = variant === "bot" ? Bot : User;

  return (
    <div
      className={cn(
        "flex size-10 shrink-0 items-center justify-center rounded-full bg-muted ring-2 ring-border shadow-sm",
        variant === "bot" && "bg-brand/10 text-brand",
        className
      )}
      aria-hidden={!label}
      title={label}
    >
      <IconComponent className="size-4" />
    </div>
  );
}

export function MessageSenderLabel({
  children,
  meta,
  className,
}: {
  children: ReactNode;
  meta?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-1 flex flex-wrap items-center gap-x-2 gap-y-0.5", className)}>
      <Text type={TextTypes.Body6} tag="span" bold>
        {children}
      </Text>
      {meta ? (
        <Text type={TextTypes.Body7} color="muted" tag="span">
          {meta}
        </Text>
      ) : null}
    </div>
  );
}

export function MessageBubble({
  variant = "bot",
  children,
  className,
}: {
  variant?: "user" | "bot" | "outgoing" | "pending";
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        variant === "user" && "message-bubble-user",
        variant === "bot" && "message-bubble-bot",
        variant === "outgoing" && "message-bubble-outgoing",
        variant === "pending" && "message-bubble-pending",
        className
      )}
    >
      {children}
    </div>
  );
}

export function MessageContent({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <div className={cn("chat-markdown", className)}>{children}</div>;
}

export function MessageRow({
  children,
  align = "start",
  className,
}: {
  children: ReactNode;
  align?: "start" | "end";
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex w-full max-w-full gap-3 px-3 py-2 md:px-4",
        align === "end" && "justify-end",
        className
      )}
    >
      {children}
    </div>
  );
}

export function MessageToolbar({
  children,
  stats,
  className,
}: {
  children?: ReactNode;
  stats?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("message-toolbar mt-3", className)}>
      <div className="flex items-center gap-1">{children}</div>
      {stats ? (
        <Text type={TextTypes.Body7} color="muted" className="ml-auto">
          {stats}
        </Text>
      ) : null}
    </div>
  );
}

export function MessageToolbarButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick?: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="size-8 text-muted-foreground hover:text-foreground"
      aria-label={label}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}

export function MessageStats({
  message,
}: {
  message: {
    is_generating?: boolean;
    text?: string;
    meta_data?: {
      cancelled?: boolean;
      total_time?: string | number;
      token_usage?: {
        prompt_tokens?: number;
        completion_tokens?: number;
        total_tokens?: number;
      };
    };
  };
}) {
  const usage = message.meta_data?.token_usage;
  const totalTime = message.meta_data?.total_time;
  const parsedTotalTime = useMemo(() => {
    if (totalTime === undefined || totalTime === null) return null;
    const parsed = parseFloat(String(totalTime));
    return Number.isFinite(parsed) ? parsed : null;
  }, [totalTime]);

  const [syncedBaseSeconds, setSyncedBaseSeconds] = useState<number>(parsedTotalTime ?? 0);
  const [syncedAtMs, setSyncedAtMs] = useState<number>(Date.now());
  const [nowMs, setNowMs] = useState<number>(Date.now());

  useEffect(() => {
    setSyncedBaseSeconds(parsedTotalTime ?? 0);
    setSyncedAtMs(Date.now());
    setNowMs(Date.now());
  }, [parsedTotalTime]);

  useEffect(() => {
    if (!message.is_generating) return;
    const id = window.setInterval(() => setNowMs(Date.now()), 250);
    return () => window.clearInterval(id);
  }, [message.is_generating]);

  const displaySeconds = message.is_generating
    ? syncedBaseSeconds + Math.max(0, (nowMs - syncedAtMs) / 1000)
    : parsedTotalTime;

  const displaySecondsText = displaySeconds !== null && displaySeconds !== undefined
    ? `${displaySeconds.toFixed(message.is_generating ? 1 : 2)}s`
    : null;

  const tokensPerSecond =
    parsedTotalTime && usage?.completion_tokens
      ? (usage.completion_tokens / parsedTotalTime).toFixed(2)
      : null;

  if (message.is_generating) {
    return (
      <span className="inline-flex items-center gap-1">
        <Loader2 className="size-3 animate-spin" />
        <span>{displaySecondsText ? `Generating ${displaySecondsText}` : "Generating"}</span>
      </span>
    );
  }

  if (message.meta_data?.cancelled) {
    return <>Cancelled</>;
  }

  if (!message.text || !usage) {
    return null;
  }

  return (
    <>
      {usage.prompt_tokens} prompt · {usage.completion_tokens} completion · {usage.total_tokens} total
      {displaySecondsText ? ` · ${displaySecondsText}` : null}
      {tokensPerSecond ? ` · ${tokensPerSecond} tok/s` : null}
    </>
  );
}

export function PendingMessageItem({
  text = "Reasoning…",
  avatarSrc,
}: {
  text?: string;
  avatarSrc?: string;
}) {
  return (
    <MessageRow>
      <MessageAvatar variant="bot" src={avatarSrc} label="Assistant" className="hidden md:flex" />
      <div className="min-w-0 flex-1">
        <MessageSenderLabel meta="typing">Assistant</MessageSenderLabel>
        <MessageBubble variant="pending">
          <ShinyText>{text}</ShinyText>
        </MessageBubble>
      </div>
    </MessageRow>
  );
}

export function DefaultBotToolbar({
  message,
  onCopy,
  onRegenerate,
}: {
  message: Parameters<typeof MessageStats>[0]["message"];
  onCopy?: () => void;
  onRegenerate?: () => void;
}) {
  return (
    <MessageToolbar stats={<MessageStats message={message} />}>
      <MessageToolbarButton label="Copy message" onClick={onCopy}>
        <Clipboard className="size-4" />
      </MessageToolbarButton>
      <MessageToolbarButton label="Regenerate response" onClick={onRegenerate}>
        <RefreshCcw className="size-4" />
      </MessageToolbarButton>
    </MessageToolbar>
  );
}

export function UserMessageShell({
  senderLabel,
  senderMeta,
  avatarLabel,
  avatarVariant = "user",
  children,
  footer,
  align = "start",
}: {
  senderLabel: ReactNode;
  senderMeta?: ReactNode;
  avatarLabel?: string;
  avatarVariant?: "user" | "bot" | "self";
  children: ReactNode;
  footer?: ReactNode;
  align?: "start" | "end";
}) {
  return (
    <MessageRow align={align}>
      {align === "start" ? (
        <MessageAvatar variant={avatarVariant} label={avatarLabel} className="hidden md:flex" />
      ) : null}
      <div className={cn("min-w-0 flex-1", align === "end" && "max-w-[85%]")}>
        {align === "start" ? <MessageSenderLabel meta={senderMeta}>{senderLabel}</MessageSenderLabel> : null}
        <MessageBubble variant={align === "end" ? "outgoing" : "user"}>
          <MessageContent>{children}</MessageContent>
          {footer}
        </MessageBubble>
      </div>
    </MessageRow>
  );
}

export function BotMessageShell({
  senderLabel = "Assistant",
  senderMeta,
  avatarSrc,
  avatarFooter,
  children,
  footer,
  align = "start",
  showIdentity = true,
}: {
  senderLabel?: ReactNode;
  senderMeta?: ReactNode;
  avatarSrc?: string;
  avatarFooter?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  align?: "start" | "end";
  showIdentity?: boolean;
}) {
  return (
    <MessageRow align={align}>
      {align === "start" && showIdentity ? (
        <div className="hidden shrink-0 flex-col items-center gap-1 md:flex">
          <MessageAvatar variant="bot" src={avatarSrc} label={String(senderLabel)} />
          {avatarFooter ? <div className="flex w-full justify-center">{avatarFooter}</div> : null}
        </div>
      ) : null}
      <div className={cn("min-w-0 flex-1", align === "end" && "max-w-[85%]")}>
        {align === "start" && showIdentity ? <MessageSenderLabel meta={senderMeta}>{senderLabel}</MessageSenderLabel> : null}
        <MessageBubble variant={align === "end" ? "outgoing" : "bot"}>
          <MessageContent>{children}</MessageContent>
          {footer}
        </MessageBubble>
      </div>
    </MessageRow>
  );
}
