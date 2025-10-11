import { useEffect, useMemo, useState } from "react";
import { navigate } from "vike/client/router";
import { Button, Text, TextTypes } from "@open-chat-go/ui";
import { MobileServerSelector } from "@/components/mobile/MobileServerSelector";
import {
  clearMobileRuntimeErrorState,
  isMobileAppRuntime,
  readMobileRuntimeErrorState,
  retryActiveMobileServerStart,
  type MobileRuntimeErrorState,
} from "@open-chat-go/ui";

export default function Page() {
  const [state, setState] = useState<MobileRuntimeErrorState | null>(null);
  const [status, setStatus] = useState("");

  const refresh = () => {
    const result = readMobileRuntimeErrorState();
    if (!result.ok) {
      setStatus(result.message || "Failed to load runtime error details");
      setState(null);
      return;
    }

    setState(result.hasError ? result.state : null);
    setStatus(result.hasError ? "" : "No runtime error is currently recorded.");
  };

  useEffect(() => {
    if (!isMobileAppRuntime()) {
      navigate("/mobile/config");
      return;
    }
    refresh();
  }, []);

  const happenedAtText = useMemo(() => {
    if (!state?.happenedAtUnixMs) {
      return "";
    }
    try {
      return new Date(state.happenedAtUnixMs).toLocaleString();
    } catch {
      return "";
    }
  }, [state?.happenedAtUnixMs]);

  const retry = () => {
    const result = retryActiveMobileServerStart();
    if (!result.ok) {
      setStatus(result.message || "Failed to restart backend");
      refresh();
      return;
    }

    clearMobileRuntimeErrorState();
    setStatus(result.message || "Server restarted");
    navigate("/login");
  };

  const dismiss = () => {
    clearMobileRuntimeErrorState();
    navigate("/mobile/config");
  };

  return (
    <div className="min-h-full bg-secondary text-secondary-foreground">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-4 px-4 py-8 sm:py-12">
        <div className="rounded-3xl border border-secondary-foreground/20 bg-secondary/70 p-6 shadow-xl backdrop-blur">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Server runtime problem
          </Text>
          <Text type={TextTypes.Body5} color="muted" className="mt-2">
            The app recovered into safe local mode so you can adjust server settings and retry.
          </Text>

          <div className="mt-4 rounded-2xl border border-secondary-foreground/20 bg-background/70 p-4">
            <Text type={TextTypes.Body6} bold>
              {state?.serverLabel || "Unknown server"}
            </Text>
            <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">
              {state?.upstreamUrl || "No server URL available"}
            </Text>
            <Text type={TextTypes.Body7} color="muted" className="mt-2">
              {state?.message || "Runtime error details are unavailable."}
            </Text>
            {typeof state?.webErrorCode === "number" && (
              <Text type={TextTypes.Body7} color="muted" className="mt-1">
                WebView code: {state.webErrorCode}
              </Text>
            )}
            {state?.failingUrl && (
              <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">
                Failed URL: {state.failingUrl}
              </Text>
            )}
            {happenedAtText && (
              <Text type={TextTypes.Body7} color="muted" className="mt-1">
                Last failure: {happenedAtText}
              </Text>
            )}
          </div>

          <div className="mt-4 flex flex-col gap-2 sm:flex-row">
            <Button type="button" onClick={retry}>
              Retry active server
            </Button>
            <Button type="button" variant="outline" onClick={() => navigate("/mobile/config")}>
              Open server settings
            </Button>
            <Button type="button" variant="ghost" onClick={dismiss}>
              Dismiss
            </Button>
          </div>

          <div className="mt-5 border-t border-secondary-foreground/15 pt-4">
            <Text type={TextTypes.Body6} color="muted" className="mb-2">
              Quick server switch
            </Text>
            <MobileServerSelector onStatus={setStatus} />
          </div>

          {status && (
            <Text type={TextTypes.Body6} color="muted" className="mt-3">
              {status}
            </Text>
          )}
        </div>
      </div>
    </div>
  );
}
