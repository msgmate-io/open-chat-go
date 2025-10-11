import { useState } from "react";
import { Maximize2 } from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Icon,
  Text,
  TextTypes,
  Textarea,
} from "@open-chat-go/ui";

type ExpandableTextareaFieldProps = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  hint?: string;
  minHeightClassName?: string;
  expandedMinHeightClassName?: string;
  expandable?: boolean;
  monospace?: boolean;
};

export function ExpandableTextareaField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  minHeightClassName = "min-h-20",
  expandedMinHeightClassName = "min-h-[55vh]",
  expandable = false,
  monospace = false,
}: ExpandableTextareaFieldProps) {
  const [expanded, setExpanded] = useState(false);

  const textStyle = monospace ? "font-mono text-xs" : "";

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <Text type={TextTypes.Body6} bold>{label}</Text>
        {expandable ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8"
            onClick={() => setExpanded(true)}
            aria-label={`Expand ${label.toLowerCase()}`}
            title={`Expand ${label.toLowerCase()}`}
          >
            <Icon icon={Maximize2} size="sm" />
          </Button>
        ) : null}
      </div>

      <Textarea
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className={`${minHeightClassName} ${textStyle}`.trim()}
      />

      {hint ? <Text type={TextTypes.Body7} color="muted">{hint}</Text> : null}

      {expandable ? (
        <Dialog open={expanded} onOpenChange={setExpanded}>
          <DialogContent className="flex max-h-[92vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border-border/70 p-0">
            <DialogHeader className="border-b border-border/60 px-6 py-4 text-left">
              <DialogTitle>{label}</DialogTitle>
              <DialogDescription>
                Edit the full value in a larger editor. Changes are kept when you close this dialog.
              </DialogDescription>
            </DialogHeader>

            <div className="flex min-h-0 flex-1 flex-col px-6 py-4">
              <Textarea
                value={value}
                onChange={(event) => onChange(event.target.value)}
                placeholder={placeholder}
                className={`flex-1 resize-y ${expandedMinHeightClassName} ${textStyle}`.trim()}
              />
            </div>

            <DialogFooter className="border-t border-border/60 px-6 py-4 sm:items-center sm:justify-between">
              <Text type={TextTypes.Body7} color="muted">{value.length} characters</Text>
              <Button type="button" onClick={() => setExpanded(false)}>Done</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </div>
  );
}
