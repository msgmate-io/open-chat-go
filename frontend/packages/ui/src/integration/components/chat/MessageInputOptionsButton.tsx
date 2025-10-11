import { useState } from "react";
import { Check, ClipboardCopy } from "lucide-react";
import {
  DropdownMenuItem,
} from "@open-chat-go/ui";

/**
 * Menu items for the composer options dropdown (attach file menu) that craft
 * and copy the `open-chat run` CLI command for the current chat/input state.
 * Must be rendered inside a DropdownMenu (e.g. ComposerOptionsMenu).
 */
export function MessageInputOptionsMenuItems({
  getCommand,
}: {
  getCommand: () => string | null;
}) {
  const [copied, setCopied] = useState(false);

  const command = getCommand();
  const canCopy = Boolean(command && command.trim());

  const handleCopy = async () => {
    if (!command) {
      return;
    }
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (insecure context or permission denied): no-op.
    }
  };

  return (
    <DropdownMenuItem onClick={() => void handleCopy()} disabled={!canCopy} className="gap-2">
      {copied ? (
        <Check className="size-4" />
      ) : (
        <ClipboardCopy className="size-4" />
      )}
      {copied ? "Copied!" : "Copy CLI command"}
    </DropdownMenuItem>
  );
}
