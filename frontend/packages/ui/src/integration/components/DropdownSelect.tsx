import type { ReactNode } from "react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@open-chat-go/ui";

export type DropdownSelectItem = {
  value: string;
  label: ReactNode;
  description?: ReactNode;
  hint?: ReactNode;
  disabled?: boolean;
};

export function DropdownSelect({
  value,
  onValueChange,
  items,
  placeholder,
  className,
  disabled,
}: {
  value: string;
  onValueChange: (value: string) => void;
  items: DropdownSelectItem[];
  placeholder?: string;
  className?: string;
  disabled?: boolean;
}) {
  const selected = items.find((item) => item.value === value);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        <button
          type="button"
          className={cn(
            "flex w-full items-center justify-between gap-2 rounded-md border border-input bg-background px-3 py-2 text-left text-sm shadow-sm transition-colors hover:bg-accent/40 focus:outline-none focus:ring-1 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-60",
            className,
          )}
        >
          <span className="flex min-w-0 items-center gap-2 truncate">
            {selected ? (
              selected.label
            ) : (
              <span className="text-muted-foreground">{placeholder || "Select..."}</span>
            )}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">▾</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 min-w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto">
        {items.map((item) => (
          <DropdownMenuItem
            key={item.value}
            disabled={item.disabled}
            onSelect={() => onValueChange(item.value)}
            className="flex w-full cursor-pointer items-center justify-between gap-3"
          >
            <div className="min-w-0 flex-1 space-y-0.5">
              <div className="truncate">{item.label}</div>
              {item.description ? (
                <div className="truncate text-xs text-muted-foreground">{item.description}</div>
              ) : null}
            </div>
            {item.hint ? <div className="shrink-0">{item.hint}</div> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
