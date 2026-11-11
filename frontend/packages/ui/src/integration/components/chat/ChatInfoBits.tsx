import { useState } from "react";
import { ChevronDown, Link2 } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@open-chat-go/ui";
import { cn } from "../../lib/utils";

/**
 * Per-interaction information bits (ci#1027).
 *
 * Integrations (eg the msgmate coder) attach a compact label/value strip to an
 * interaction through the chat's server-side settings (`settings.info_bits`), so
 * the shared chat UI can surface the context a chat was started from - the
 * triggering GitHub issue, the repo, the sandbox - without the integration
 * forking the chat header.
 *
 * The strip is deliberately small: one collapsed row of chips that expands in
 * place. Bits without an `href` render as plain text, and only sanitized targets
 * (http(s)/mailto/site-relative) reach the anchor, so a stored bit can never
 * inject a `javascript:` link.
 */
export interface ChatInfoBit {
  label: string;
  value?: string;
  href?: string;
}

interface ChatInfoBitsProps {
  bits?: ChatInfoBit[] | null;
  /**
   * Start expanded. The strip defaults to collapsed so a chat header stays one
   * compact row on mobile, but an explicit `defaultOpen` lets a detail surface
   * show the bits immediately.
   */
  defaultOpen?: boolean;
  className?: string;
}

// Keep the client-side guard in sync with the server-side cap in
// backend/api/chats/Settings.go (maxChatInfoBits).
const MAX_RENDERED_BITS = 12;

function safeHref(raw?: string): string | undefined {
  const href = (raw || "").trim();
  if (!href) return undefined;
  const lower = href.toLowerCase();
  if (lower.startsWith("http://") || lower.startsWith("https://") || lower.startsWith("mailto:")) return href;
  // Site-relative targets only; `//host` is protocol-relative and therefore an
  // off-site link, which is not what a chat bit should be able to do.
  if (href.startsWith("/") && !href.startsWith("//")) return href;
  return undefined;
}

function normalizeBits(bits?: ChatInfoBit[] | null): ChatInfoBit[] {
  if (!Array.isArray(bits)) return [];
  const out: ChatInfoBit[] = [];
  for (const bit of bits) {
    const label = (bit?.label || "").trim();
    if (!label) continue;
    const href = safeHref(bit?.href);
    const value = (bit?.value || "").trim() || href || "";
    if (!value) continue;
    out.push({ label, value, href });
    if (out.length >= MAX_RENDERED_BITS) break;
  }
  return out;
}

export function ChatInfoBits({ bits, defaultOpen = false, className }: ChatInfoBitsProps) {
  const [open, setOpen] = useState(defaultOpen);
  const items = normalizeBits(bits);
  if (items.length === 0) return null;

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className={cn("w-full text-xs", className)}
      data-testid="chat-info-bits"
    >
      <div className="flex items-center gap-2">
        <CollapsibleTrigger
          className="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          aria-label={open ? "Hide chat information" : "Show chat information"}
        >
          <Link2 className="size-3" aria-hidden />
          Info
          <span className="text-muted-foreground/70">({items.length})</span>
          <ChevronDown className={cn("size-3 transition-transform", open && "rotate-180")} aria-hidden />
        </CollapsibleTrigger>
        {open ? null : (
          // A collapsed strip still shows the first bit, so the most important
          // context (usually the issue) is reachable without expanding.
          <span className="min-w-0 truncate text-muted-foreground">{items[0].value}</span>
        )}
      </div>
      <CollapsibleContent>
        <ul className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {items.map((bit, index) => (
            <li
              key={`${bit.label}-${index}`}
              className="flex min-w-0 max-w-full items-center gap-1 rounded-md border border-border/60 bg-muted/30 px-1.5 py-0.5"
            >
              <span className="shrink-0 font-medium text-muted-foreground">{bit.label}</span>
              {bit.href ? (
                <a
                  href={bit.href}
                  target={bit.href.startsWith("/") ? undefined : "_blank"}
                  rel="noopener noreferrer"
                  className="min-w-0 truncate text-foreground underline decoration-border underline-offset-2 hover:decoration-foreground"
                  title={bit.value}
                >
                  {bit.value}
                </a>
              ) : (
                <span className="min-w-0 truncate text-foreground" title={bit.value}>
                  {bit.value}
                </span>
              )}
            </li>
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  );
}

export default ChatInfoBits;
