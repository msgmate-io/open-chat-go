import { useMemo, useState } from "react";

/**
 * ci#1101: a coder TUI interaction is frequently driven by a TUI on the
 * operator's machine that reaches the sandbox over a local ssh link. When that
 * TUI goes away (terminal killed, ssh link dropped, laptop asleep) the
 * interaction must not keep running upstream invisibly: the backend turns the
 * lease expiry into a finished failure turn and this widget renders it, offering
 * the upstream sandboxes the work can be continued in.
 *
 * The metadata payload only ever carries identifiers and labels - never an
 * attach token, kubeconfig, owner identity or any other user data.
 */
export type TuiDetachedCandidate = {
  sandbox_uuid?: string;
  provider?: string;
  label?: string;
  running?: boolean;
  expires_at?: string;
};

export type TuiDetachedEvent = {
  reason?: string;
  chat_uuid?: string;
  interaction_uuid?: string;
  session_id?: string;
  sandbox_uuid?: string;
  transport?: string;
  detached_at?: string;
  default_sandbox?: string;
  candidates?: TuiDetachedCandidate[];
};

export type TuiContinueRequest = {
  sandboxUUID: string;
  event: TuiDetachedEvent;
};

/** The sandbox a resume should propose first; mirrors the backend default. */
export function defaultDetachedCandidate(event: TuiDetachedEvent): string {
  const candidates = (event.candidates ?? []).filter(
    (candidate) => typeof candidate.sandbox_uuid === "string" && candidate.sandbox_uuid.trim() !== "",
  );
  const ranIn = candidates.find((candidate) => candidate.sandbox_uuid === event.sandbox_uuid);
  if (ranIn?.sandbox_uuid) return ranIn.sandbox_uuid;
  const running = candidates.find((candidate) => candidate.running === true);
  if (running?.sandbox_uuid) return running.sandbox_uuid;
  return candidates[0]?.sandbox_uuid?.trim() ?? "";
}

/**
 * Resolves the detached-TUI failure payload from a message's metadata. Returns
 * null for every other message so the widget never hijacks unrelated turns.
 */
export function resolveTuiDetachedEvent(
  metaData: Record<string, unknown> | undefined,
): TuiDetachedEvent | null {
  if (!metaData) return null;
  const payload = metaData.tui_detached;
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const event = payload as TuiDetachedEvent;
  const reason = typeof event.reason === "string" ? event.reason : "";
  if (reason !== "" && reason !== "tui_detached") return null;
  return event;
}

function candidateTitle(candidate: TuiDetachedCandidate): string {
  const label = (candidate.label ?? "").trim();
  if (label !== "") return label;
  const provider = (candidate.provider ?? "").trim();
  return provider !== "" ? `${provider} sandbox` : "upstream sandbox";
}

export function TuiDetachedEventWidget({
  event,
  onContinue,
  busy: busyProp = false,
}: {
  event: TuiDetachedEvent;
  /**
   * Continue the interaction in the selected upstream sandbox. The integration
   * owning the chat wires this to the backend resume endpoint; without a handler
   * the picker stays visible but disabled, so the failure is still surfaced.
   */
  onContinue?: (request: TuiContinueRequest) => void | Promise<void>;
  busy?: boolean;
}) {
  const candidates = useMemo(
    () =>
      (event.candidates ?? []).filter(
        (candidate) =>
          typeof candidate.sandbox_uuid === "string" && candidate.sandbox_uuid.trim() !== "",
      ),
    [event.candidates],
  );
  const [selected, setSelected] = useState<string>(() => defaultDetachedCandidate(event));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const detachedAt = (event.detached_at ?? "").trim();
  const transport = (event.transport ?? "ssh").trim() || "ssh";
  const canContinue = Boolean(onContinue) && candidates.length > 0 && selected !== "" && !busy && !busyProp;

  const handleContinue = async () => {
    if (!onContinue || selected === "") return;
    setBusy(true);
    setError(null);
    try {
      await onContinue({ sandboxUUID: selected, event });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not continue the interaction.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="my-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs"
      data-testid="tui-detached-widget"
    >
      <div className="font-medium">Interaction stopped: the driving TUI detached</div>
      <div className="mt-0.5 break-words text-muted-foreground">
        The coder TUI that drove this interaction over a local {transport} link went away
        {detachedAt ? ` at ${detachedAt}` : ""}. Nothing was continued upstream, so no work kept
        running unseen.
      </div>

      {candidates.length === 0 ? (
        <div className="mt-1.5 text-muted-foreground">
          No upstream sandbox is available to continue it in right now. Resume once a sandbox is
          listed again.
        </div>
      ) : (
        <div className="mt-2">
          <div className="text-muted-foreground">Continue the interaction in an upstream sandbox:</div>
          <div className="mt-1 flex flex-col gap-1" role="radiogroup" aria-label="upstream sandbox">
            {candidates.map((candidate) => {
              const sandboxUUID = (candidate.sandbox_uuid ?? "").trim();
              const isSelected = sandboxUUID === selected;
              return (
                <label
                  key={sandboxUUID}
                  className="flex cursor-pointer items-center gap-2 rounded border border-border/60 px-2 py-1"
                >
                  <input
                    type="radio"
                    name={`tui-detached-${sandboxUUID}`}
                    value={sandboxUUID}
                    checked={isSelected}
                    disabled={busy || busyProp}
                    onChange={() => setSelected(sandboxUUID)}
                  />
                  <span className="min-w-0">
                    <span className="font-medium">{candidateTitle(candidate)}</span>
                    <span className="ml-1 font-mono text-[11px] text-muted-foreground">
                      {sandboxUUID}
                    </span>
                    {candidate.running ? (
                      <span className="ml-1 text-muted-foreground">(work in flight)</span>
                    ) : null}
                  </span>
                </label>
              );
            })}
          </div>
          <button
            type="button"
            className="mt-1.5 rounded border border-border px-2 py-1 font-medium disabled:opacity-50"
            onClick={() => void handleContinue()}
            disabled={!canContinue}
            data-testid="tui-detached-continue"
          >
            {busy || busyProp ? "Continuing…" : "Continue there"}
          </button>
          {!onContinue ? (
            <div className="mt-1 text-muted-foreground">
              Continuing is not wired up in this client; open the chat in a client that can attach to
              the sandbox above.
            </div>
          ) : null}
        </div>
      )}

      {error ? (
        <div className="mt-1 text-destructive" role="alert">
          {error}
        </div>
      ) : null}
    </div>
  );
}
