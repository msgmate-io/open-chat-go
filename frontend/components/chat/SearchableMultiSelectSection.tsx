import { useMemo, useState } from "react";
import { Badge, Checkbox, Collapsible, CollapsibleContent, CollapsibleTrigger, Input, Text, TextTypes } from "@open-chat-go/ui";

type SearchableMultiSelectSectionProps = {
  title: string;
  description?: string;
  items: string[];
  selectedItems: string[];
  onToggle: (item: string, nextChecked: boolean) => void;
  emptyText: string;
  searchPlaceholder?: string;
  collapsedByDefault?: boolean;
  monospaceItems?: boolean;
  maxListHeightClassName?: string;
};

export function SearchableMultiSelectSection({
  title,
  description,
  items,
  selectedItems,
  onToggle,
  emptyText,
  searchPlaceholder = "Search...",
  collapsedByDefault = false,
  monospaceItems = true,
  maxListHeightClassName = "max-h-64",
}: SearchableMultiSelectSectionProps) {
  const [open, setOpen] = useState(!collapsedByDefault);
  const [search, setSearch] = useState("");

  const selectedSet = useMemo(() => new Set(selectedItems), [selectedItems]);
  const filteredItems = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) {
      return items;
    }
    return items.filter((item) => item.toLowerCase().includes(query));
  }, [items, search]);

  return (
    <Collapsible open={open} onOpenChange={setOpen} className="rounded-xl border border-border/70 bg-background">
      <CollapsibleTrigger asChild>
        <button
          type="button"
          className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left active:bg-muted/40"
        >
          <div className="min-w-0">
            <Text type={TextTypes.Body6} bold>{title}</Text>
            {description ? <Text type={TextTypes.Body7} color="muted">{description}</Text> : null}
          </div>
          <div className="flex items-center gap-2">
            <Badge variant="outline">{selectedItems.length} selected</Badge>
            <Text type={TextTypes.Body7} color="muted">{open ? "Hide" : "Show"}</Text>
          </div>
        </button>
      </CollapsibleTrigger>

      <CollapsibleContent className="border-t border-border/60 px-4 pb-4 pt-3">
        <div className="space-y-3">
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={searchPlaceholder}
          />

          <div className={`overflow-y-auto rounded-lg border border-border/60 bg-muted/10 p-2 ${maxListHeightClassName}`}>
            {filteredItems.length === 0 ? (
              <Text type={TextTypes.Body6} color="muted" className="px-2 py-3">{emptyText}</Text>
            ) : (
              <div className="space-y-1">
                {filteredItems.map((item) => {
                  const checked = selectedSet.has(item);
                  return (
                    <label
                      key={item}
                      className="flex cursor-pointer items-center gap-3 rounded-md border border-transparent px-2 py-2 active:bg-muted/40"
                    >
                      <Checkbox
                        checked={checked}
                        onCheckedChange={(next) => onToggle(item, Boolean(next))}
                        aria-label={`Toggle ${item}`}
                      />
                      <span className={`min-w-0 break-all text-sm ${monospaceItems ? "font-mono" : ""}`}>{item}</span>
                    </label>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
