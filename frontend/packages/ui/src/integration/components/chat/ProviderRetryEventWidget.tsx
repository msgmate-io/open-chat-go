import { useEffect, useState } from "react";
import { LoadingSpinner } from "@open-chat-go/ui";

export type ProviderRetryEvent = {
  attempt?: number;
  max_attempts?: number;
  status?: string; // "retry_scheduled" | "giving_up"
  error_detail?: string;
  retry_at?: string; // RFC3339 timestamp of the upcoming retry
};

function secondsUntil(retryAt: string | undefined): number | null {
  if (!retryAt) return null;
  const target = Date.parse(retryAt);
  if (Number.isNaN(target)) return null;
  return Math.max(0, Math.ceil((target - Date.now()) / 1000));
}

export function ProviderRetryEventWidget({ event }: { event: ProviderRetryEvent }) {
  const attempt = typeof event.attempt === "number" ? event.attempt : 1;
  const maxAttempts = typeof event.max_attempts === "number" ? event.max_attempts : attempt;
  const status = event.status || "retry_scheduled";
  const errorDetail = (event.error_detail || "").trim();
  const givingUp = status === "giving_up";

  const [remaining, setRemaining] = useState<number | null>(() => secondsUntil(event.retry_at || ""));
  useEffect(() => {
    if (givingUp) return;
    const update = () => setRemaining(secondsUntil(event.retry_at || ""));
    update();
    const interval = setInterval(update, 1000);
    return () => clearInterval(interval);
  }, [event.retry_at, givingUp]);

  const shortError = errorDetail.length > 200 ? `${errorDetail.slice(0, 200)}…` : errorDetail;

  return (
    <div className="my-2 flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs">
      <span className="mt-0.5 shrink-0">
        <LoadingSpinner className="h-3.5 w-3.5 text-amber-600" />
      </span>
      <div className="min-w-0">
        <div className="font-medium">
          {givingUp
            ? `Provider request failed permanently (attempt ${attempt}/${maxAttempts})`
            : `Provider request failed (attempt ${attempt}/${maxAttempts})`}
        </div>
        {shortError ? <div className="mt-0.5 break-words text-muted-foreground">{shortError}</div> : null}
        {!givingUp && remaining !== null && remaining > 0 ? (
          <div className="mt-0.5 text-muted-foreground">
            Retrying in <span className="font-mono">{remaining}s</span>…
          </div>
        ) : null}
        {!givingUp && remaining !== null && remaining <= 0 ? (
          <div className="mt-0.5 text-muted-foreground">Retrying now…</div>
        ) : null}
      </div>
    </div>
  );
}

export function resolveProviderRetryEvent(metaData: Record<string, unknown> | undefined): ProviderRetryEvent | null {
  if (!metaData) return null;
  const eventType = metaData.event_type;
  if (eventType !== "provider_retry") return null;
  const payload = metaData.provider_retry;
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  return payload as ProviderRetryEvent;
}
