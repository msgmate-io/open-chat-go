import { ArrowUp, Loader2 } from "lucide-react";

import { cn } from "../../lib/utils";
import { Button } from "../button";

export function SendMessageButton({
  onClick,
  isLoading = false,
  disabled = false,
}: {
  onClick: () => void;
  isLoading?: boolean;
  disabled?: boolean;
}) {
  return (
    <Button
      type="button"
      variant="brand"
      size="icon"
      onClick={onClick}
      disabled={isLoading || disabled}
      aria-label="Send message"
      className={cn(
        "size-9 shrink-0 rounded-full shadow-sm transition-transform hover:scale-[1.02] hover:shadow-md disabled:opacity-50"
      )}
    >
      {isLoading ? (
        <Loader2 className="size-4 animate-spin" />
      ) : (
        <ArrowUp className="size-4" />
      )}
    </Button>
  );
}
