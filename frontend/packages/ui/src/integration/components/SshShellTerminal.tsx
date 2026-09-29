import "xterm/css/xterm.css";

import { useEffect, useRef, useState } from "react";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Dialog, DialogContent, DialogHeader, DialogTitle, Input, Text, TextTypes } from "@open-chat-go/ui";

import { useTerminalZoom } from "../hooks/use-terminal-zoom";

type ShellWSMessage = {
  type: string;
  data_b64?: string;
  message?: string;
  code?: number;
  error_code?: string;
  server_uuid?: string;
  key_uuid?: string;
};

function decodeBase64ToBytes(dataB64: string): Uint8Array {
  const binary = atob(dataB64);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    out[i] = binary.charCodeAt(i);
  }
  return out;
}

const utf8Decoder = new TextDecoder();

export function SshShellTerminal({ serverUUID, subtitle }: { serverUUID: string; subtitle?: string }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<any>(null);
  const fitAddonRef = useRef<any>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [connectionState, setConnectionState] = useState("disconnected");
  const [statusMessage, setStatusMessage] = useState("");
  const [isExpanded, setIsExpanded] = useState(false);
  const [connectNonce, setConnectNonce] = useState(0);
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false);
  const [unlockPassphrase, setUnlockPassphrase] = useState("");
  const [unlockBusy, setUnlockBusy] = useState(false);
  const [unlockError, setUnlockError] = useState("");
  const [unlockKeyUUID, setUnlockKeyUUID] = useState("");
  const { level, fontSize, shrink, grow, canShrink, canGrow } = useTerminalZoom();
  const fontSizeRef = useRef(fontSize);
  fontSizeRef.current = fontSize;

  useEffect(() => {
    if (!serverUUID) return;
    if (!containerRef.current) return;

    let inputDisposable: { dispose: () => void } | null = null;
    let resizeHandler: (() => void) | null = null;
    let ws: WebSocket | null = null;
    let term: any = null;
    let fitAddon: any = null;
    let cancelled = false;

    const init = async () => {
      const xtermModule = await import("xterm");
      const fitModule = await import("xterm-addon-fit");
      const TerminalCtor = (xtermModule as any).Terminal;
      const FitAddonCtor = (fitModule as any).FitAddon;

      if (!TerminalCtor || !FitAddonCtor || cancelled || !containerRef.current) {
        return;
      }

      term = new TerminalCtor({
        cursorBlink: true,
        convertEol: true,
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, Liberation Mono, monospace",
        fontSize: fontSizeRef.current,
        theme: {
          background: "#10161f",
          foreground: "#e5edf7",
        },
      });
      fitAddon = new FitAddonCtor();
      term.loadAddon(fitAddon);
      term.open(containerRef.current);
      fitAddon.fit();
      term.focus();

      term.writeln("Connecting to SSH shell relay...\r\n");

      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const wsURL = `${protocol}//${window.location.host}/api/v1/integrations/ssh/servers/${encodeURIComponent(serverUUID)}/shell/ws`;
      ws = new WebSocket(wsURL);

      const sendResize = () => {
        if (!ws || ws.readyState !== WebSocket.OPEN) return;
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      };

      ws.onopen = () => {
        setConnectionState("open");
        setStatusMessage("");
        sendResize();
      };

      ws.onmessage = (event) => {
        try {
          const message = JSON.parse(String(event.data)) as ShellWSMessage;
          switch (message.type) {
            case "ready":
              term.writeln("\r\nConnected.\r\n");
              break;
            case "output":
              if (message.data_b64) {
                const bytes = decodeBase64ToBytes(message.data_b64);
                term.write(utf8Decoder.decode(bytes));
              }
              break;
            case "error":
              term.writeln(`\r\n[error] ${message.message || "Unknown error"}\r\n`);
              setStatusMessage(message.message || "Shell relay error");
              break;
            case "unlock_required":
              term.writeln(`\r\n[locked] ${message.message || "SSH key unlock required"}\r\n`);
              setStatusMessage(message.message || "SSH key unlock required");
              setUnlockKeyUUID(message.key_uuid || "");
              setUnlockDialogOpen(true);
              break;
            case "exit":
              term.writeln(`\r\n[exit] code=${String(message.code ?? 0)}\r\n`);
              setStatusMessage(`Shell exited with code ${String(message.code ?? 0)}`);
              break;
            default:
              break;
          }
        } catch {
          term.writeln("\r\n[error] invalid relay payload\r\n");
        }
      };

      ws.onerror = () => {
        setConnectionState("error");
        setStatusMessage("Shell websocket error");
      };

      ws.onclose = () => {
        setConnectionState("closed");
      };

      inputDisposable = term.onData((data: string) => {
        if (!ws || ws.readyState !== WebSocket.OPEN) return;
        ws.send(JSON.stringify({ type: "input", data }));
      });

      resizeHandler = () => {
        fitAddon.fit();
        sendResize();
      };
      window.addEventListener("resize", resizeHandler);

      termRef.current = term;
      fitAddonRef.current = fitAddon;
      wsRef.current = ws;
      setConnectionState("connecting");
    };

    void init();

    return () => {
      cancelled = true;
      if (resizeHandler) {
        window.removeEventListener("resize", resizeHandler);
      }
      inputDisposable?.dispose();
      if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
        ws.close();
      }
      wsRef.current = null;
      if (term) {
        term.dispose();
      }
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [serverUUID, connectNonce]);

  const submitUnlock = async () => {
    if (!unlockPassphrase.trim()) {
      setUnlockError("Passphrase is required.");
      return;
    }
    setUnlockBusy(true);
    setUnlockError("");
    try {
      const response = await fetch(`/api/v1/integrations/ssh/servers/${encodeURIComponent(serverUUID)}/unlock`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ passphrase: unlockPassphrase }),
      });
      if (!response.ok) {
        const message = await response.text();
        throw new Error(message || "Failed to unlock SSH key.");
      }
      setUnlockDialogOpen(false);
      setUnlockPassphrase("");
      setStatusMessage("SSH key unlocked. Reconnecting shell...");
      setConnectNonce((prev) => prev + 1);
    } catch (err) {
      setUnlockError(err instanceof Error ? err.message : "Failed to unlock SSH key.");
    } finally {
      setUnlockBusy(false);
    }
  };

  useEffect(() => {
    if (!termRef.current) return;
    termRef.current.options.fontSize = fontSize;
    if (!fitAddonRef.current) return;
    const timer = window.setTimeout(() => {
      try {
        fitAddonRef.current.fit();
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
          wsRef.current.send(JSON.stringify({ type: "resize", cols: termRef.current.cols, rows: termRef.current.rows }));
        }
      } catch {
        // no-op
      }
    }, 100);
    return () => window.clearTimeout(timer);
  }, [isExpanded, level, fontSize]);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Interactive Session</CardTitle>
        <CardDescription>
          Terminal stream is relayed through the SSH integration websocket. Connection: {connectionState}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className={`rounded-xl border border-border/70 bg-muted/30 p-3 ${isExpanded ? "fixed inset-0 z-50 m-0 rounded-none border-0 bg-background p-2" : ""}`}>
          <div className="mb-2 flex items-center justify-between gap-2 rounded-md border border-border/70 bg-background px-3 py-2">
            <Text type={TextTypes.Body7} color="muted">{isExpanded && statusMessage ? statusMessage : "Shell"}</Text>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" className="h-6 w-6 p-0 text-xs" title="Zoom out" disabled={!canShrink} onClick={shrink}>−</Button>
              <Button variant="outline" size="sm" className="h-6 w-6 p-0 text-xs" title="Zoom in" disabled={!canGrow} onClick={grow}>+</Button>
              <Button variant="outline" size="sm" onClick={() => setIsExpanded((prev) => !prev)}>
                {isExpanded ? "Collapse" : "Expand"}
              </Button>
            </div>
          </div>
          <div
            ref={containerRef}
            className={`w-full overflow-hidden rounded-md border border-border bg-black ${isExpanded ? "h-[calc(100dvh-88px)] min-h-[320px] [html[data-openchat-runtime=mobile]_&]:min-h-[200px]" : "h-[55vh] min-h-[300px] max-h-[560px] [html[data-openchat-runtime=mobile]_&]:min-h-[160px]"}`}
          />
        </div>
        {statusMessage ? (
          <Text type={TextTypes.Body6} color="destructive" className="mt-2">
            {statusMessage}
          </Text>
        ) : null}

        <Dialog open={unlockDialogOpen} onOpenChange={setUnlockDialogOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Unlock SSH Key</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <Text type={TextTypes.Body6} color="muted">
                This server uses a passphrase-protected SSH key.
                {unlockKeyUUID ? ` Key: ${unlockKeyUUID}` : ""}
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
                <Button variant="outline" onClick={() => setUnlockDialogOpen(false)} disabled={unlockBusy}>Cancel</Button>
                <Button onClick={submitUnlock} disabled={unlockBusy}>{unlockBusy ? "Unlocking..." : "Unlock and reconnect"}</Button>
              </div>
            </div>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
