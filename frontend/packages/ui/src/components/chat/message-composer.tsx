import {
  forwardRef,
  useEffect,
  useRef,
  type ClipboardEventHandler,
  type ReactNode,
  type RefObject,
} from "react";
import { Square } from "lucide-react";

import { cn } from "../../lib/utils";
import { Text, TextTypes } from "../text";
import { Button } from "../button";
import { SendMessageButton } from "./SendMessageButton";

export interface MessageComposerBotConfig {
  reasoning?: boolean;
  tools?: string[];
  backend?: string;
}

export interface MessageComposerProps {
  text: string;
  setText: (text: string) => void;
  onSendMessage?: () => void;
  isLoading?: boolean;
  isBotResponding?: boolean;
  stopBotResponse?: () => void;
  maxHeight?: number;
  minHeight?: number;
  placeholder?: string;
  footer?: ReactNode;
  botConfig?: MessageComposerBotConfig | null;
  attachmentsPreview?: ReactNode;
  hasAttachments?: boolean;
  footerOptions?: ReactNode;
  className?: string;
  onPaste?: ClipboardEventHandler<HTMLTextAreaElement>;
}

export function CancelResponseButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="icon"
      onClick={onClick}
      aria-label="Stop response"
      className="size-9 shrink-0 rounded-full border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
    >
      <Square className="size-4 fill-current" />
    </Button>
  );
}

function useAutoResizeTextarea(
  ref: RefObject<HTMLTextAreaElement | null>,
  text: string,
  minHeight: number,
  maxHeight: number
) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const resize = () => {
      const node = ref.current;
      if (!node) return;

      node.style.height = "auto";
      const nextHeight = Math.max(minHeight, Math.min(node.scrollHeight, maxHeight));
      node.style.height = `${nextHeight}px`;
      node.style.overflowY = node.scrollHeight > maxHeight ? "auto" : "hidden";
    };

    resize();
    const rafId = requestAnimationFrame(resize);

    let observer: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(resize);
      observer.observe(el);
    }

    window.addEventListener("resize", resize);
    return () => {
      cancelAnimationFrame(rafId);
      window.removeEventListener("resize", resize);
      observer?.disconnect();
    };
  }, [ref, text, minHeight, maxHeight]);
}

export const MessageComposer = forwardRef<HTMLTextAreaElement, MessageComposerProps>(
  function MessageComposer(
    {
      text,
      setText,
      onSendMessage = () => {},
      isLoading = false,
      isBotResponding = false,
      stopBotResponse = () => {},
      maxHeight = 300,
      minHeight = 26,
      placeholder = "Send a message…",
      footer,
      attachmentsPreview,
      hasAttachments = false,
      footerOptions,
      className,
      onPaste,
    },
    ref
    ) {
      const localRef = useRef<HTMLTextAreaElement | null>(null);

    const textareaRef = (node: HTMLTextAreaElement | null) => {
      localRef.current = node;
      if (typeof ref === "function") ref(node);
      else if (ref) ref.current = node;
    };

    useAutoResizeTextarea(localRef, text, minHeight, maxHeight);

    const canSend = Boolean(text.trim()) || hasAttachments;

    const footerContent = footer ?? (
      <Text type={TextTypes.Body7} color="muted" className="hidden px-4 text-center md:block">
        Msgmate.io uses magic — be sceptical and verify information.
      </Text>
    );

    const handleSendMessage = () => {
      if (!canSend) return;
      onSendMessage();
    };

    return (
      <div
        className={cn("message-composer-shell flex w-full shrink-0 flex-col items-center gap-2", className)}
      >
        <div className="message-composer">
          <textarea
            ref={textareaRef}
            value={text}
            placeholder={placeholder}
            rows={1}
            onChange={(event) => setText(event.target.value)}
            onPaste={onPaste}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                handleSendMessage();
              }
            }}
            className="message-composer-textarea"
            style={{ minHeight, maxHeight }}
          />

          {attachmentsPreview}

          <div className="message-composer-footer">
            <div className="flex min-w-0 items-end gap-1.5">
              {footerOptions}
              {isBotResponding ? (
                <CancelResponseButton onClick={stopBotResponse} />
              ) : (
                <SendMessageButton
                  onClick={handleSendMessage}
                  isLoading={isLoading}
                  disabled={!canSend}
                />
              )}
            </div>
          </div>
        </div>

        {footerContent}
      </div>
    );
  }
);
