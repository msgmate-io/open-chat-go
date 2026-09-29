import { useMemo, useState } from "react";
import { AlertTriangle, Check, ChevronDown, Loader2 } from "lucide-react";
import { cn } from "../../lib/utils";
import {
  Badge,
  Button,
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  Text,
  TextTypes,
  Checkbox,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  Input,
} from "@open-chat-go/ui";

export type ConfirmableAction = {
  action_id: string;
  target_tool_name: string;
  status?: "pending" | "executed" | "failed" | "rejected";
  input?: Record<string, unknown>;
  title?: string;
  description?: string;
  confirm_label?: string;
  danger_level?: "low" | "medium" | "high" | string;
  result?: unknown;
  execution_error?: string;
  continue_after_execute?: boolean;
};

type ConfirmableActionExecuteResponse = {
  success: boolean;
  status?: string;
  error?: string;
};

export function ConfirmableActionWidget({
  chatUUID,
  messageUUID,
  action,
  executionBlockedReason,
  onExecuted,
}: {
  chatUUID: string;
  messageUUID: string;
  action: ConfirmableAction;
  executionBlockedReason?: string;
  onExecuted?: () => void;
}) {
  const [inputText, setInputText] = useState(JSON.stringify(action.input ?? {}, null, 2));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState((action.status ?? "pending") === "pending");
  const [continueAfterExecute, setContinueAfterExecute] = useState(Boolean(action.continue_after_execute));
  const [unlockOpen, setUnlockOpen] = useState(false);
  const [unlockPassphrase, setUnlockPassphrase] = useState("");
  const [unlockBusy, setUnlockBusy] = useState(false);
  const [unlockError, setUnlockError] = useState<string | null>(null);

  const status = action.status ?? "pending";
  const isPending = (action.status ?? "pending") === "pending";
  const isExecuted = status === "executed";
  const isDanger = action.danger_level === "high";
  const executionBlocked = !!executionBlockedReason;

  const parsedInput = useMemo(() => {
    try {
      return JSON.parse(inputText) as Record<string, unknown>;
    } catch {
      return null;
    }
  }, [inputText]);

  const inputPreview = useMemo(() => {
    const source = parsedInput ?? action.input ?? {};
    if (!source || typeof source !== "object" || Array.isArray(source)) {
      return "No params";
    }
    const entries = Object.entries(source as Record<string, unknown>);
    if (entries.length === 0) return "No params";
    const first = entries[0];
    const firstValue = typeof first[1] === "string" ? first[1] : JSON.stringify(first[1]);
    if (entries.length === 1) {
      return `${first[0]}: ${firstValue}`;
    }
    return `${first[0]}: ${firstValue} +${entries.length - 1} more`;
  }, [action.input, parsedInput]);

  const serverUUID = useMemo(() => {
    const source = parsedInput ?? action.input;
    if (!source || typeof source !== "object" || Array.isArray(source)) {
      return "";
    }
    const raw = (source as Record<string, unknown>).server_uuid;
    return typeof raw === "string" ? raw.trim() : "";
  }, [action.input, parsedInput]);

  const resultText = useMemo(() => {
    if (action.result === undefined || action.result === null) return "";
    if (typeof action.result === "string") return action.result;
    try {
      return JSON.stringify(action.result, null, 2);
    } catch {
      return String(action.result);
    }
  }, [action.result]);

  const resultPreview = useMemo(() => {
    if (!resultText) return "No output";
    const compact = resultText.replace(/\s+/g, " ").trim();
    if (compact.length <= 80) return compact;
    return `${compact.slice(0, 80)}...`;
  }, [resultText]);

  const isUnlockRequiredError = (message: string) => message.toLowerCase().includes("unlock") && message.toLowerCase().includes("passphrase");

  const executeAction = async () => {
    if (!parsedInput) {
      setError("Input must be valid JSON.");
      return;
    }
    if (executionBlocked) {
      setError(executionBlockedReason || "Action cannot be executed yet.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const response = await fetch(
        `/api/v1/chats/${chatUUID}/messages/${messageUUID}/confirm-actions/${action.action_id}/execute`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ input: parsedInput, continue_after_execute: continueAfterExecute }),
        }
      );
      let failureMessage = "Execution failed.";
      const contentType = (response.headers.get("content-type") || "").toLowerCase();
      if (contentType.includes("application/json")) {
        const payload = (await response.json()) as ConfirmableActionExecuteResponse;
        if (response.ok && payload.success) {
          onExecuted?.();
          return;
        }
        if (payload.error) {
          failureMessage = payload.error;
        }
      } else {
        const textMessage = await response.text();
        if (textMessage.trim() !== "") {
          failureMessage = textMessage;
        }
      }

      setError(failureMessage);
      if (serverUUID && isUnlockRequiredError(failureMessage)) {
        setUnlockOpen(true);
      }
      if (!response.ok) {
        if (response.status === 409) {
          onExecuted?.();
        }
        return;
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Execution failed");
    } finally {
      setBusy(false);
    }
  };

  const unlockAndRetry = async () => {
    if (!serverUUID) {
      setUnlockError("This action has no server_uuid to unlock.");
      return;
    }
    if (!unlockPassphrase.trim()) {
      setUnlockError("Passphrase is required.");
      return;
    }
    setUnlockBusy(true);
    setUnlockError(null);
    try {
      const response = await fetch(`/api/v1/integrations/ssh/servers/${encodeURIComponent(serverUUID)}/unlock`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ passphrase: unlockPassphrase, chat_uuid: chatUUID }),
      });
      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || "Failed to unlock SSH key.");
      }
      setUnlockOpen(false);
      setUnlockPassphrase("");
      setError(null);
      await executeAction();
    } catch (err) {
      setUnlockError(err instanceof Error ? err.message : "Failed to unlock SSH key.");
    } finally {
      setUnlockBusy(false);
    }
  };

  return (
    <Collapsible open={open} onOpenChange={setOpen} id={`confirmable-action-${action.action_id}`}>
      <div className="mt-2 rounded-lg border border-border/70 bg-card/60 p-2">
        <CollapsibleTrigger className="flex w-full items-start gap-2 text-left text-sm hover:opacity-90">
          <span className="mt-0.5 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
            Confirm
          </span>
          <code className="break-all text-xs">{action.target_tool_name}</code>
          <Badge
            variant={isPending ? "secondary" : "outline"}
            className={cn(
              "ml-auto mr-1 text-[10px] uppercase",
              isPending ? "border-amber-300/70 bg-amber-50 text-amber-700" : "",
              isExecuted ? "border-emerald-300/70 bg-emerald-50 text-emerald-700" : ""
            )}
          >
            {status}
          </Badge>
          {isDanger ? <AlertTriangle className="mt-0.5 size-3.5 text-amber-500" /> : null}
          <ChevronDown className={`mt-0.5 size-4 shrink-0 transition-transform ${open ? "rotate-180" : ""}`} />
        </CollapsibleTrigger>

        <CollapsibleContent className="CollapsibleContent transition-all duration-300">
          <div className="mt-2 space-y-2">
            <Text type={TextTypes.Body7} color="muted">
              {action.description || `Execute tool '${action.target_tool_name}' after review.`}
            </Text>

            {isPending ? (
              <>
                <Collapsible id={`confirmable-action-input-${action.action_id}`}>
                  <CollapsibleTrigger className="flex w-full items-center justify-between rounded-md border border-border/60 bg-muted/20 px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted/30">
                    <span className="font-medium">Proposed params</span>
                    <span className="ml-2 truncate text-right">{inputPreview}</span>
                  </CollapsibleTrigger>
                  <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                    <pre className="scrollbar-hidden mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border/60 bg-muted/30 p-2 text-xs leading-5">
                      {inputText}
                    </pre>
                  </CollapsibleContent>
                </Collapsible>

                {executionBlocked ? (
                  <Text type={TextTypes.Body7} color="destructive">{executionBlockedReason}</Text>
                ) : null}

                <textarea
                  className="min-h-16 w-full rounded-md border border-border/70 bg-background p-2 font-mono text-xs"
                  value={inputText}
                  onChange={(event) => setInputText(event.target.value)}
                  disabled={!isPending || busy || executionBlocked}
                />

                <label className="flex items-center gap-2">
                  <Checkbox
                    checked={continueAfterExecute}
                    onCheckedChange={(checked) => setContinueAfterExecute(checked === true)}
                    disabled={!isPending || busy || executionBlocked}
                  />
                  <Text type={TextTypes.Body7} color="muted">Continue interaction after execution</Text>
                </label>
              </>
            ) : null}

            {error ? <Text type={TextTypes.Body7} color="destructive">{error}</Text> : null}
            {action.execution_error ? <Text type={TextTypes.Body7} color="destructive">{action.execution_error}</Text> : null}

            {serverUUID && (error || action.execution_error) && isUnlockRequiredError(`${error || ""}\n${action.execution_error || ""}`) ? (
              <Button type="button" size="sm" variant="outline" onClick={() => setUnlockOpen(true)} disabled={busy}>
                Unlock SSH key
              </Button>
            ) : null}

            {isExecuted ? (
              <Collapsible id={`confirmable-action-result-${action.action_id}`}>
                <CollapsibleTrigger className="flex w-full items-center justify-between rounded-md border border-border/60 bg-muted/20 px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted/30">
                  <span className="font-medium"><Check className="mr-1 inline size-3" />Execution result</span>
                  <span className="ml-2 truncate text-right">{resultPreview}</span>
                </CollapsibleTrigger>
                <CollapsibleContent className="CollapsibleContent transition-all duration-300">
                  <pre className="mt-1 max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border/60 bg-muted/30 p-2 text-xs leading-5">
                    <code>{resultText || "No output"}</code>
                  </pre>
                </CollapsibleContent>
              </Collapsible>
            ) : null}

            {isPending ? (
              <div className="flex items-center gap-2">
                <Button type="button" size="sm" onClick={executeAction} disabled={busy || !parsedInput || executionBlocked}>
                  {busy ? <Loader2 className="mr-1 size-4 animate-spin" /> : null}
                  {action.confirm_label || "Confirm and execute"}
                </Button>
              </div>
            ) : null}
          </div>
        </CollapsibleContent>
      </div>

      <Dialog open={unlockOpen} onOpenChange={setUnlockOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Unlock SSH Key</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <Text type={TextTypes.Body6} color="muted">
              Enter the SSH key passphrase to unlock this server for this chat.
            </Text>
            <Input
              type="password"
              value={unlockPassphrase}
              onChange={(event) => setUnlockPassphrase(event.target.value)}
              placeholder="SSH key passphrase"
              disabled={unlockBusy}
            />
            {unlockError ? <Text type={TextTypes.Body7} color="destructive">{unlockError}</Text> : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setUnlockOpen(false)} disabled={unlockBusy}>Cancel</Button>
              <Button onClick={unlockAndRetry} disabled={unlockBusy}>{unlockBusy ? "Unlocking..." : "Unlock and retry"}</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </Collapsible>
  );
}
