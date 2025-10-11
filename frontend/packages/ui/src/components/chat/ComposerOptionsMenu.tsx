import type { ReactNode } from "react";
import { MoreHorizontal } from "lucide-react";

import { cn } from "../../lib/utils";
import { Button } from "../button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "../dropdown-menu";

/**
 * Compact "…" options dropdown for the message composer footer. Rendered left
 * of the send button; children become the menu items (attach file, tools
 * status, copy CLI command, voice session, …).
 */
export function ComposerOptionsMenu({
  children,
  className,
  contentClassName,
}: {
  children?: ReactNode;
  className?: string;
  contentClassName?: string;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn(
            "size-9 shrink-0 rounded-full text-muted-foreground hover:bg-muted hover:text-foreground md:size-10",
            className,
          )}
          aria-label="Message input options"
          title="Message input options"
        >
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" side="top" className={cn("w-48", contentClassName)}>
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
